// Package smoke runs a headless, end-to-end Cashu flow through the cdk-go
// bindings against a real mint. It is the release-review workhorse: a non-zero
// exit means the bindings regressed for a downstream consumer.
package smoke

import (
	"context"
	"fmt"
	"io"
	"time"

	cdk "github.com/cashubtc/cdk-go/bindings/cdkffi"
	"github.com/ye0man/cdk-go-apps/internal/wallet"
)

const (
	mintAmount = 1000
	sendAmount = 300
)

// Run executes the smoke flow and writes progress to out.
func Run(ctx context.Context, mintURL string, out io.Writer) error {
	if mintURL == "" {
		mintURL = wallet.DefaultMint
	}
	start := time.Now()
	logf := func(format string, a ...any) {
		fmt.Fprintf(out, format+"\n", a...)
	}
	logf("cashu smoke: mint=%s", mintURL)

	mnA, err := cdk.GenerateMnemonic()
	if err != nil {
		return stepErr("generate mnemonic A", err)
	}
	mnB, err := cdk.GenerateMnemonic()
	if err != nil {
		return stepErr("generate mnemonic B", err)
	}

	a, err := wallet.OpenMemory(mintURL, mnA)
	if err != nil {
		return stepErr("open wallet A", err)
	}
	defer a.Close()
	b, err := wallet.OpenMemory(mintURL, mnB)
	if err != nil {
		return stepErr("open wallet B", err)
	}
	defer b.Close()
	logf("[ok] opened two in-memory wallets")

	if bal, err := a.Balance(); err != nil {
		return stepErr("balance A", err)
	} else if bal != 0 {
		return fmt.Errorf("expected A balance 0, got %d", bal)
	}
	logf("[ok] A initial balance is 0")

	if err := ctx.Err(); err != nil {
		return err
	}
	quote, err := a.CreateMintQuote(mintAmount)
	if err != nil {
		return stepErr("mint quote", err)
	}
	logf("[ok] mint quote %s (state %s)", quote.Id, wallet.QuoteStateString(quote.State))

	if _, err := a.WaitForMintQuote(ctx, quote.Id, 120*time.Second); err != nil {
		return stepErr("wait for mint quote", err)
	}
	proofs, err := a.MintQuoteIssue(quote.Id)
	if err != nil {
		return stepErr("mint", err)
	}
	total, err := wallet.ProofsTotal(proofs)
	if err != nil {
		return stepErr("proofs total", err)
	}
	if total != mintAmount {
		return fmt.Errorf("expected minted value %d, got %d", mintAmount, total)
	}
	if bal, err := a.Balance(); err != nil {
		return stepErr("balance A after mint", err)
	} else if bal != mintAmount {
		return fmt.Errorf("expected A balance %d after mint, got %d", mintAmount, bal)
	}
	logf("[ok] minted %d proofs worth %d sats", len(proofs), total)

	encoded, sentValue, err := a.Send(sendAmount, false, "smoke")
	if err != nil {
		return stepErr("send", err)
	}
	if sentValue != sendAmount {
		return fmt.Errorf("expected sent value %d, got %d", sendAmount, sentValue)
	}
	if bal, err := a.Balance(); err != nil {
		return stepErr("balance A after send", err)
	} else if bal != mintAmount-sendAmount {
		return fmt.Errorf("expected A balance %d after send, got %d", mintAmount-sendAmount, bal)
	}
	logf("[ok] A prepared and confirmed a %d sat send (token %d chars)", sentValue, len(encoded))

	info, err := wallet.DecodeToken(encoded)
	if err != nil {
		return stepErr("decode token", err)
	}
	if info.Value != sentValue {
		return fmt.Errorf("decoded token value %d != sent value %d", info.Value, sentValue)
	}
	logf("[ok] decoded token offline: value=%d unit=%s proofs=%d", info.Value, info.Unit, info.Proofs)

	received, err := b.Receive(encoded)
	if err != nil {
		return stepErr("receive", err)
	}
	if received == 0 || received > sentValue {
		return fmt.Errorf("received %d out of range (0, %d]", received, sentValue)
	}
	if bal, err := b.Balance(); err != nil {
		return stepErr("balance B", err)
	} else if bal != received {
		return fmt.Errorf("expected B balance %d, got %d", received, bal)
	}
	logf("[ok] B received %d sats (fee %d)", received, sentValue-received)

	tok, err := cdk.TokenDecode(encoded)
	if err != nil {
		return stepErr("decode token for spend check", err)
	}
	simple, err := tok.ProofsSimple()
	if err != nil {
		return stepErr("token proofs", err)
	}
	spent, err := a.CheckProofsSpent(simple)
	if err != nil {
		return stepErr("check proofs spent", err)
	}
	if len(spent) != len(simple) {
		return fmt.Errorf("spent check returned %d results for %d proofs", len(spent), len(simple))
	}
	for i, s := range spent {
		if !s {
			return fmt.Errorf("proof %d still reported unspent after transfer", i)
		}
	}
	logf("[ok] all %d sent proofs reported spent", len(simple))

	before, err := b.Balance()
	if err != nil {
		return stepErr("balance B before swap", err)
	}
	if err := b.Swap(); err != nil {
		return stepErr("swap", err)
	}
	after, err := b.Balance()
	if err != nil {
		return stepErr("balance B after swap", err)
	}
	if after > before {
		return fmt.Errorf("swap increased B balance: %d -> %d", before, after)
	}
	if after < before-10 {
		return fmt.Errorf("swap lost too much value: %d -> %d", before, after)
	}
	if after == 0 {
		return fmt.Errorf("swap left B with zero balance")
	}
	logf("[ok] B re-split its balance %d -> %d (input fee %d)", before, after, before-after)

	txs, err := b.Transactions()
	if err != nil {
		return stepErr("list transactions", err)
	}
	if len(txs) == 0 {
		return fmt.Errorf("expected B to have transaction history")
	}
	logf("[ok] B has %d transaction(s)", len(txs))

	if _, err := a.Restore(); err != nil {
		return stepErr("restore", err)
	}
	logf("[ok] A restore completed")

	logf("PASS in %s", time.Since(start).Round(time.Millisecond))
	return nil
}

func stepErr(step string, err error) error {
	return fmt.Errorf("%s: %w", step, err)
}
