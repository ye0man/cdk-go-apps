package wallet

import (
	"strings"
	"testing"

	cdk "github.com/cashubtc/cdk-go/bindings/cdkffi"
)

// TestMnemonicRoundTrip exercises the pure-crypto FFI path with no network.
func TestMnemonicRoundTrip(t *testing.T) {
	mn, err := cdk.GenerateMnemonic()
	if err != nil {
		t.Fatalf("GenerateMnemonic: %v", err)
	}
	if words := len(strings.Fields(mn)); words != 12 {
		t.Fatalf("expected 12 words, got %d", words)
	}
	entropy, err := cdk.MnemonicToEntropy(mn)
	if err != nil {
		t.Fatalf("MnemonicToEntropy: %v", err)
	}
	if len(entropy) != 16 {
		t.Fatalf("expected 16 bytes of entropy, got %d", len(entropy))
	}
}

// TestOpenMemoryBalanceZero builds a wallet against the in-memory SQLite store
// and reads its balance. This exercises NewWallet plus the record marshaling of
// the bindings without touching the network or disk.
func TestOpenMemoryBalanceZero(t *testing.T) {
	mn, err := cdk.GenerateMnemonic()
	if err != nil {
		t.Fatalf("GenerateMnemonic: %v", err)
	}
	w, err := OpenMemory(DefaultMint, mn)
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer w.Close()

	bal, err := w.Balance()
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if bal != 0 {
		t.Fatalf("expected zero balance, got %d", bal)
	}
}

func TestDecodeTokenRejectsGarbage(t *testing.T) {
	if _, err := DecodeToken("definitely-not-a-cashu-token"); err == nil {
		t.Fatal("expected an error decoding an invalid token")
	}
}

func TestUnitString(t *testing.T) {
	cases := []struct {
		unit cdk.CurrencyUnit
		want string
	}{
		{cdk.CurrencyUnitSat{}, "sat"},
		{cdk.CurrencyUnitMsat{}, "msat"},
		{cdk.CurrencyUnitUsd{}, "usd"},
		{cdk.CurrencyUnitCustom{Unit: "points"}, "points"},
	}
	for _, c := range cases {
		if got := UnitString(c.unit); got != c.want {
			t.Errorf("UnitString(%T) = %q, want %q", c.unit, got, c.want)
		}
	}
}

func TestQuoteStateString(t *testing.T) {
	cases := []struct {
		state cdk.QuoteState
		want  string
	}{
		{cdk.QuoteStateUnpaid, "unpaid"},
		{cdk.QuoteStatePaid, "paid"},
		{cdk.QuoteStatePending, "pending"},
		{cdk.QuoteStateIssued, "issued"},
	}
	for _, c := range cases {
		if got := QuoteStateString(c.state); got != c.want {
			t.Errorf("QuoteStateString(%d) = %q, want %q", c.state, got, c.want)
		}
	}
}
