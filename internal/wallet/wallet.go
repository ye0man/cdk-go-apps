// Package wallet is a thin, dogfooding-oriented wrapper around the
// github.com/cashubtc/cdk-go FFI bindings. It keeps the surface small and
// idiomatic so both the CLI and the smoke runner share one code path.
package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	cdk "github.com/cashubtc/cdk-go/bindings/cdkffi"
)

const (
	// DefaultMint is the community test mint. It auto-pays BOLT11 mint quotes,
	// which makes it suitable for end-to-end CI without a Lightning backend.
	DefaultMint = "https://testnut.cashudevkit.org"
	// DefaultUnit is the only unit exercised by this app for now.
	DefaultUnit = "sat"
)

// Config is the on-disk wallet configuration.
type Config struct {
	MintURL  string `json:"mint_url"`
	Unit     string `json:"unit"`
	Mnemonic string `json:"mnemonic"`
}

// ConfigPath returns the location of the config file.
func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cashu", "config.json"), nil
}

func dataDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(dir, "cashu")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

// LoadConfig reads the config file, returning a helpful error if it is missing.
func LoadConfig() (*Config, error) {
	p, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w (run `cashu init` first)", p, err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &c, nil
}

// SaveConfig writes the config with restrictive permissions.
func SaveConfig(c *Config) error {
	p, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// NewConfig generates a fresh config with a new mnemonic.
func NewConfig(mintURL string) (*Config, error) {
	mnemonic, err := cdk.GenerateMnemonic()
	if err != nil {
		return nil, err
	}
	if mintURL == "" {
		mintURL = DefaultMint
	}
	return &Config{MintURL: mintURL, Unit: DefaultUnit, Mnemonic: mnemonic}, nil
}

// Wallet wraps a cdk Wallet handle.
type Wallet struct {
	cfg   *Config
	inner *cdk.Wallet
}

// Open opens the configured wallet using an on-disk SQLite store.
func Open(cfg *Config) (*Wallet, error) {
	dir, err := dataDir()
	if err != nil {
		return nil, err
	}
	return OpenAt(cfg, filepath.Join(dir, "wallet.sqlite"))
}

// OpenAt opens a wallet against an explicit SQLite path.
func OpenAt(cfg *Config, dbPath string) (*Wallet, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	if cfg.Mnemonic == "" {
		return nil, fmt.Errorf("config has no mnemonic; run `cashu init`")
	}
	mintURL := cfg.MintURL
	if mintURL == "" {
		mintURL = DefaultMint
	}
	inner, err := newCDKWallet(mintURL, cfg.Mnemonic, dbPath)
	if err != nil {
		return nil, err
	}
	return &Wallet{cfg: cfg, inner: inner}, nil
}

// OpenMemory opens an ephemeral in-memory wallet. Used by the smoke runner and
// offline tests so no files are touched.
func OpenMemory(mintURL, mnemonic string) (*Wallet, error) {
	if mintURL == "" {
		mintURL = DefaultMint
	}
	inner, err := newCDKWallet(mintURL, mnemonic, ":memory:")
	if err != nil {
		return nil, err
	}
	return &Wallet{cfg: &Config{MintURL: mintURL, Unit: DefaultUnit, Mnemonic: mnemonic}, inner: inner}, nil
}

func newCDKWallet(mintURL, mnemonic, dbPath string) (*cdk.Wallet, error) {
	return cdk.NewWallet(
		mintURL,
		cdk.CurrencyUnitSat{},
		mnemonic,
		cdk.WalletStoreSqlite{Path: dbPath},
		cdk.WalletConfig{},
	)
}

// Close releases the underlying FFI object.
func (w *Wallet) Close() {
	if w.inner != nil {
		w.inner.Destroy()
		w.inner = nil
	}
}

// MintURL returns the configured mint URL.
func (w *Wallet) MintURL() string { return w.cfg.MintURL }

// Balance returns the total spendable balance in sats.
func (w *Wallet) Balance() (uint64, error) {
	a, err := w.inner.TotalBalance()
	if err != nil {
		return 0, err
	}
	return a.Value, nil
}

// Balances returns total, pending and reserved balances.
func (w *Wallet) Balances() (total, pending, reserved uint64, err error) {
	t, err := w.inner.TotalBalance()
	if err != nil {
		return 0, 0, 0, err
	}
	p, err := w.inner.TotalPendingBalance()
	if err != nil {
		return 0, 0, 0, err
	}
	r, err := w.inner.TotalReservedBalance()
	if err != nil {
		return 0, 0, 0, err
	}
	return t.Value, p.Value, r.Value, nil
}

// CreateMintQuote requests a BOLT11 mint quote for amount sats.
func (w *Wallet) CreateMintQuote(amount uint64) (cdk.MintQuote, error) {
	amt := cdk.Amount{Value: amount}
	return w.inner.MintQuote(cdk.PaymentMethodBolt11{}, &amt, nil, nil)
}

// WaitForMintQuote polls until the quote is paid or issued, or the timeout
// elapses.
func (w *Wallet) WaitForMintQuote(ctx context.Context, quoteID string, timeout time.Duration) (cdk.MintQuote, error) {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var last cdk.MintQuote
	for {
		q, err := w.inner.CheckMintQuote(quoteID)
		if err != nil {
			return q, err
		}
		last = q
		switch q.State {
		case cdk.QuoteStatePaid, cdk.QuoteStateIssued:
			return q, nil
		}
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("timed out waiting for quote %s (state %s): %w", quoteID, QuoteStateString(last.State), ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

// MintQuoteIssue issues proofs for a paid quote.
func (w *Wallet) MintQuoteIssue(quoteID string) ([]cdk.Proof, error) {
	return w.inner.Mint(quoteID, cdk.SplitTargetNone{}, nil)
}

// Send prepares and confirms a send, returning the encoded token and its value.
func (w *Wallet) Send(amount uint64, sendAll bool, memo string) (encoded string, value uint64, err error) {
	var amt cdk.Amount
	if sendAll {
		b, berr := w.inner.TotalBalance()
		if berr != nil {
			return "", 0, berr
		}
		amt = b
	} else {
		amt = cdk.Amount{Value: amount}
	}

	opts := cdk.SendOptions{
		AmountSplitTarget:       cdk.SplitTargetNone{},
		SendKind:                cdk.SendKindOnlineExact{},
		IncludeFee:              false,
		P2pkLockedProofSendMode: cdk.P2pkLockedProofSendModeSwap,
	}
	prep, err := w.inner.PrepareSend(amt, opts)
	if err != nil {
		return "", 0, err
	}
	var memoPtr *string
	if memo != "" {
		memoPtr = &memo
	}
	tok, err := prep.Confirm(memoPtr)
	if err != nil {
		return "", 0, err
	}
	val, err := tok.Value()
	if err != nil {
		return "", 0, err
	}
	return tok.Encode(), val.Value, nil
}

// Receive redeems an encoded token, returning the amount credited.
func (w *Wallet) Receive(encoded string) (uint64, error) {
	tok, err := cdk.TokenDecode(strings.TrimSpace(encoded))
	if err != nil {
		return 0, err
	}
	amt, err := w.inner.Receive(tok, cdk.ReceiveOptions{AmountSplitTarget: cdk.SplitTargetNone{}})
	if err != nil {
		return 0, err
	}
	return amt.Value, nil
}

// Swap re-splits all unspent proofs in place, preserving total value (aside
// from the mint's input fee). An explicit split target is passed so the whole
// balance is re-issued rather than left as unrepresented change.
func (w *Wallet) Swap() error {
	proofs, err := w.inner.GetProofsByStates([]cdk.ProofState{cdk.ProofStateUnspent})
	if err != nil {
		return err
	}
	if len(proofs) == 0 {
		return fmt.Errorf("no unspent proofs to swap")
	}
	total, err := cdk.ProofsTotalAmount(proofs)
	if err != nil {
		return err
	}
	target := cdk.SplitTargetValue{Amount: total}
	_, err = w.inner.Swap(nil, target, proofs, nil, false)
	return err
}

// Melt pays a BOLT11 invoice, returning the finalized melt.
func (w *Wallet) Melt(invoice string) (cdk.FinalizedMelt, error) {
	q, err := w.inner.MeltQuote(cdk.PaymentMethodBolt11{}, strings.TrimSpace(invoice), nil, nil)
	if err != nil {
		return cdk.FinalizedMelt{}, err
	}
	prep, err := w.inner.PrepareMelt(q.Id)
	if err != nil {
		return cdk.FinalizedMelt{}, err
	}
	return prep.Confirm()
}

// PayRequest pays a NUT-18 payment request, returning (payment, total) amounts.
func (w *Wallet) PayRequest(encoded string) (payment, total uint64, err error) {
	pr, err := cdk.PaymentRequestFromString(strings.TrimSpace(encoded))
	if err != nil {
		return 0, 0, err
	}
	prep, err := w.inner.PreparePayRequest(pr, nil)
	if err != nil {
		return 0, 0, err
	}
	pay := prep.PaymentAmount()
	tot := prep.TotalAmount()
	if err := prep.Confirm(); err != nil {
		return 0, 0, err
	}
	return pay.Value, tot.Value, nil
}

// Transactions lists the wallet's transaction history.
func (w *Wallet) Transactions() ([]cdk.Transaction, error) {
	return w.inner.ListTransactions(nil)
}

// Restore runs NUT-09 signature restore.
func (w *Wallet) Restore() (cdk.Restored, error) {
	return w.inner.Restore()
}

// PendingSends lists operation IDs of pending (unclaimed) sends.
func (w *Wallet) PendingSends() ([]string, error) {
	return w.inner.GetPendingSends()
}

// CheckProofsSpent reports, per proof, whether the mint considers it spent.
func (w *Wallet) CheckProofsSpent(proofs []cdk.Proof) ([]bool, error) {
	return w.inner.CheckProofsSpent(proofs)
}

// ProofsTotal sums the value of a proof slice.
func ProofsTotal(proofs []cdk.Proof) (uint64, error) {
	a, err := cdk.ProofsTotalAmount(proofs)
	if err != nil {
		return 0, err
	}
	return a.Value, nil
}

// TokenInfo is a JSON-friendly summary of a Cashu token.
type TokenInfo struct {
	Mint     string   `json:"mint,omitempty"`
	Unit     string   `json:"unit,omitempty"`
	Value    uint64   `json:"value"`
	Memo     string   `json:"memo,omitempty"`
	Proofs   int      `json:"proofs"`
	P2PK     []string `json:"p2pk_pubkeys,omitempty"`
	HTLC     []string `json:"htlc_hashes,omitempty"`
	Refund   []string `json:"p2pk_refund_pubkeys,omitempty"`
	Locktime []uint64 `json:"locktimes,omitempty"`
}

// DecodeToken parses an encoded token without contacting a mint.
func DecodeToken(encoded string) (*TokenInfo, error) {
	tok, err := cdk.TokenDecode(strings.TrimSpace(encoded))
	if err != nil {
		return nil, err
	}
	value, err := tok.Value()
	if err != nil {
		return nil, err
	}
	info := &TokenInfo{Value: value.Value}
	if mu, err := tok.MintUrl(); err == nil {
		info.Mint = mu.Url
	}
	if u := tok.Unit(); u != nil {
		info.Unit = UnitString(*u)
	}
	if m := tok.Memo(); m != nil {
		info.Memo = *m
	}
	info.P2PK = tok.P2pkPubkeys()
	info.HTLC = tok.HtlcHashes()
	info.Refund = tok.P2pkRefundPubkeys()
	info.Locktime = tok.Locktimes()
	if ps, err := tok.ProofsSimple(); err == nil {
		info.Proofs = len(ps)
	}
	return info, nil
}

// PaymentRequestInfo is a JSON-friendly summary of a NUT-18 payment request.
type PaymentRequestInfo struct {
	Amount      *uint64  `json:"amount,omitempty"`
	Unit        string   `json:"unit,omitempty"`
	Description string   `json:"description,omitempty"`
	Mints       []string `json:"mints,omitempty"`
	SingleUse   bool     `json:"single_use,omitempty"`
	PaymentID   string   `json:"payment_id,omitempty"`
}

// DecodePaymentRequest parses a NUT-18 payment request.
func DecodePaymentRequest(encoded string) (*PaymentRequestInfo, error) {
	pr, err := cdk.PaymentRequestFromString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, err
	}
	info := &PaymentRequestInfo{}
	if a := pr.Amount(); a != nil {
		v := a.Value
		info.Amount = &v
	}
	if u := pr.Unit(); u != nil {
		info.Unit = UnitString(*u)
	}
	if d := pr.Description(); d != nil {
		info.Description = *d
	}
	info.Mints = pr.Mints()
	if s := pr.SingleUse(); s != nil {
		info.SingleUse = *s
	}
	if p := pr.PaymentId(); p != nil {
		info.PaymentID = *p
	}
	return info, nil
}

// InvoiceInfo is a JSON-friendly summary of a decoded BOLT11/BOLT12 invoice.
type InvoiceInfo struct {
	Type        string  `json:"type"`
	AmountMsat  *uint64 `json:"amount_msat,omitempty"`
	Expiry      *uint64 `json:"expiry,omitempty"`
	Description string  `json:"description,omitempty"`
}

// DecodeInvoice decodes a BOLT11/BOLT12 payment request without a network call.
func DecodeInvoice(invoice string) (*InvoiceInfo, error) {
	d, err := cdk.DecodeInvoice(strings.TrimSpace(invoice))
	if err != nil {
		return nil, err
	}
	info := &InvoiceInfo{
		Type:       PaymentTypeString(d.PaymentType),
		AmountMsat: d.AmountMsat,
		Expiry:     d.Expiry,
	}
	if d.Description != nil {
		info.Description = *d.Description
	}
	return info, nil
}

// UnitString renders a currency unit.
func UnitString(u cdk.CurrencyUnit) string {
	switch v := u.(type) {
	case cdk.CurrencyUnitSat:
		return "sat"
	case cdk.CurrencyUnitMsat:
		return "msat"
	case cdk.CurrencyUnitUsd:
		return "usd"
	case cdk.CurrencyUnitEur:
		return "eur"
	case cdk.CurrencyUnitAuth:
		return "auth"
	case cdk.CurrencyUnitCustom:
		return v.Unit
	default:
		return ""
	}
}

// QuoteStateString renders a quote state.
func QuoteStateString(s cdk.QuoteState) string {
	switch s {
	case cdk.QuoteStateUnpaid:
		return "unpaid"
	case cdk.QuoteStatePaid:
		return "paid"
	case cdk.QuoteStatePending:
		return "pending"
	case cdk.QuoteStateIssued:
		return "issued"
	default:
		return fmt.Sprintf("state(%d)", uint(s))
	}
}

// TransactionDirectionString renders a transaction direction.
func TransactionDirectionString(d cdk.TransactionDirection) string {
	switch d {
	case cdk.TransactionDirectionIncoming:
		return "in"
	case cdk.TransactionDirectionOutgoing:
		return "out"
	default:
		return fmt.Sprintf("dir(%d)", uint(d))
	}
}

// TransactionStatusString renders a transaction status.
func TransactionStatusString(s cdk.TransactionStatus) string {
	switch s {
	case cdk.TransactionStatusPending:
		return "pending"
	case cdk.TransactionStatusCompleted:
		return "completed"
	case cdk.TransactionStatusFailed:
		return "failed"
	default:
		return fmt.Sprintf("status(%d)", uint(s))
	}
}

// PaymentTypeString renders a decoded invoice payment type.
func PaymentTypeString(t cdk.PaymentType) string {
	switch t {
	case cdk.PaymentTypeBolt11:
		return "bolt11"
	case cdk.PaymentTypeBolt12:
		return "bolt12"
	default:
		return fmt.Sprintf("type(%d)", uint(t))
	}
}
