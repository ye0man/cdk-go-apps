// Command cashu is a lightweight Cashu wallet and a release smoke runner,
// built on the github.com/cashubtc/cdk-go bindings.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	cdk "github.com/cashubtc/cdk-go/bindings/cdkffi"
	"github.com/ye0man/cdk-go-apps/internal/smoke"
	"github.com/ye0man/cdk-go-apps/internal/wallet"
)

const version = "0.1.0"

const usage = `cashu - a lightweight Cashu wallet built on cdk-go

usage: cashu <command> [flags]

wallet:
  init         create a wallet config (generates a mnemonic)
  mnemonic     generate a mnemonic and print it
  balance      show total, pending and reserved balances
  mint-quote   create a BOLT11 mint quote; --wait to pay via testnut and issue
  mint         issue proofs for a paid mint quote id
  send         create a token; --amount N or --all
  receive      redeem an encoded token
  swap         re-split all unspent proofs in place
  melt         pay a BOLT11 invoice
  pay          pay a NUT-18 payment request
  decode       decode a token, payment request or invoice (offline)
  history      list transaction history
  restore      run NUT-09 signature restore
  pending      list pending (unclaimed) sends

testing:
  smoke        run an end-to-end flow against a mint (default: testnut)
  version      print the version
  help         show this help

Run "cashu <command> -h" for command-specific flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	var err error
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	case "version", "-v", "--version":
		fmt.Println("cashu", version)
		return
	case "init":
		err = cmdInit(args)
	case "mnemonic":
		err = cmdMnemonic()
	case "balance":
		err = cmdBalance()
	case "mint-quote":
		err = cmdMintQuote(args)
	case "mint":
		err = cmdMint(args)
	case "send":
		err = cmdSend(args)
	case "receive":
		err = cmdReceive(args)
	case "swap":
		err = cmdSwap(args)
	case "melt":
		err = cmdMelt(args)
	case "pay":
		err = cmdPay(args)
	case "decode":
		err = cmdDecode(args)
	case "history":
		err = cmdHistory()
	case "restore":
		err = cmdRestore()
	case "pending":
		err = cmdPending()
	case "smoke":
		err = cmdSmoke(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func openWallet() (*wallet.Wallet, error) {
	cfg, err := wallet.LoadConfig()
	if err != nil {
		return nil, err
	}
	return wallet.Open(cfg)
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	mint := fs.String("mint", wallet.DefaultMint, "mint URL")
	force := fs.Bool("force", false, "overwrite an existing config with a new mnemonic")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := wallet.LoadConfig(); err == nil && !*force {
		return errors.New("config already exists; use --force to overwrite it")
	}
	cfg, err := wallet.NewConfig(*mint)
	if err != nil {
		return err
	}
	if err := wallet.SaveConfig(cfg); err != nil {
		return err
	}
	p, _ := wallet.ConfigPath()
	fmt.Printf("wrote %s\n", p)
	fmt.Printf("mint:     %s\n", cfg.MintURL)
	fmt.Printf("mnemonic: %s\n", cfg.Mnemonic)
	return nil
}

func cmdMnemonic() error {
	mn, err := cdk.GenerateMnemonic()
	if err != nil {
		return err
	}
	fmt.Println(mn)
	return nil
}

func cmdBalance() error {
	w, err := openWallet()
	if err != nil {
		return err
	}
	defer w.Close()
	total, pending, reserved, err := w.Balances()
	if err != nil {
		return err
	}
	fmt.Printf("mint:     %s\n", w.MintURL())
	fmt.Printf("total:    %d\n", total)
	fmt.Printf("pending:  %d\n", pending)
	fmt.Printf("reserved: %d\n", reserved)
	return nil
}

func cmdMintQuote(args []string) error {
	fs := flag.NewFlagSet("mint-quote", flag.ContinueOnError)
	amount := fs.Uint64("amount", 0, "amount in sats")
	wait := fs.Bool("wait", false, "wait for payment, then issue proofs")
	timeout := fs.Duration("timeout", 120*time.Second, "wait timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *amount == 0 {
		return errors.New("--amount is required")
	}
	w, err := openWallet()
	if err != nil {
		return err
	}
	defer w.Close()

	q, err := w.CreateMintQuote(*amount)
	if err != nil {
		return err
	}
	fmt.Printf("quote id: %s\n", q.Id)
	fmt.Printf("request:  %s\n", q.Request)
	fmt.Printf("amount:   %d\n", *amount)
	fmt.Printf("state:    %s\n", wallet.QuoteStateString(q.State))
	if !*wait {
		return nil
	}

	fmt.Println("waiting for payment...")
	if _, err := w.WaitForMintQuote(context.Background(), q.Id, *timeout); err != nil {
		return err
	}
	proofs, err := w.MintQuoteIssue(q.Id)
	if err != nil {
		return err
	}
	total, err := wallet.ProofsTotal(proofs)
	if err != nil {
		return err
	}
	fmt.Printf("minted %d proofs, value %d\n", len(proofs), total)
	bal, _ := w.Balance()
	fmt.Printf("balance: %d\n", bal)
	return nil
}

func cmdMint(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: cashu mint <quote-id>")
	}
	w, err := openWallet()
	if err != nil {
		return err
	}
	defer w.Close()
	proofs, err := w.MintQuoteIssue(strings.TrimSpace(args[0]))
	if err != nil {
		return err
	}
	total, err := wallet.ProofsTotal(proofs)
	if err != nil {
		return err
	}
	fmt.Printf("minted %d proofs, value %d\n", len(proofs), total)
	bal, _ := w.Balance()
	fmt.Printf("balance: %d\n", bal)
	return nil
}

func cmdSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	amount := fs.Uint64("amount", 0, "amount in sats")
	all := fs.Bool("all", false, "send the entire balance")
	memo := fs.String("memo", "", "optional token memo")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*all && *amount == 0 {
		return errors.New("--amount or --all is required")
	}
	w, err := openWallet()
	if err != nil {
		return err
	}
	defer w.Close()
	encoded, val, err := w.Send(*amount, *all, *memo)
	if err != nil {
		return err
	}
	fmt.Printf("# token value: %d\n", val)
	fmt.Println(encoded)
	return nil
}

func cmdReceive(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: cashu receive <token>")
	}
	w, err := openWallet()
	if err != nil {
		return err
	}
	defer w.Close()
	amt, err := w.Receive(strings.Join(args, ""))
	if err != nil {
		return err
	}
	fmt.Printf("received: %d\n", amt)
	bal, _ := w.Balance()
	fmt.Printf("balance:  %d\n", bal)
	return nil
}

func cmdSwap(args []string) error {
	w, err := openWallet()
	if err != nil {
		return err
	}
	defer w.Close()
	if err := w.Swap(); err != nil {
		return err
	}
	bal, _ := w.Balance()
	fmt.Printf("re-split proofs; balance: %d\n", bal)
	return nil
}

func cmdMelt(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: cashu melt <bolt11-invoice>")
	}
	w, err := openWallet()
	if err != nil {
		return err
	}
	defer w.Close()
	fin, err := w.Melt(strings.TrimSpace(args[0]))
	if err != nil {
		return err
	}
	fmt.Printf("melt quote: %s\n", fin.QuoteId)
	fmt.Printf("amount:     %d\n", fin.Amount.Value)
	fmt.Printf("fee paid:   %d\n", fin.FeePaid.Value)
	fmt.Printf("state:      %s\n", wallet.QuoteStateString(fin.State))
	if fin.Preimage != nil {
		fmt.Printf("preimage:   %s\n", *fin.Preimage)
	}
	return nil
}

func cmdPay(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: cashu pay <payment-request>")
	}
	w, err := openWallet()
	if err != nil {
		return err
	}
	defer w.Close()
	pay, total, err := w.PayRequest(strings.TrimSpace(args[0]))
	if err != nil {
		return err
	}
	fmt.Printf("paid:  %d\n", pay)
	fmt.Printf("total: %d\n", total)
	bal, _ := w.Balance()
	fmt.Printf("balance: %d\n", bal)
	return nil
}

func cmdDecode(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: cashu decode <token|payment-request|invoice>")
	}
	in := strings.Join(args, "")
	if info, err := wallet.DecodeToken(in); err == nil {
		return printJSON(info)
	}
	if info, err := wallet.DecodePaymentRequest(in); err == nil {
		return printJSON(info)
	}
	if info, err := wallet.DecodeInvoice(in); err == nil {
		return printJSON(info)
	}
	return errors.New("unrecognized token, payment request or invoice")
}

func cmdHistory() error {
	w, err := openWallet()
	if err != nil {
		return err
	}
	defer w.Close()
	txs, err := w.Transactions()
	if err != nil {
		return err
	}
	if len(txs) == 0 {
		fmt.Println("no transactions")
		return nil
	}
	fmt.Printf("%-8s %-4s %-9s %-10s %s\n", "AMOUNT", "DIR", "STATUS", "TYPE", "ID")
	for _, t := range txs {
		typ := ""
		if t.PaymentMethod != nil {
			typ = paymentMethodString(*t.PaymentMethod)
		}
		fmt.Printf("%-8d %-4s %-9s %-10s %s\n",
			t.Amount.Value,
			wallet.TransactionDirectionString(t.Direction),
			wallet.TransactionStatusString(t.Status),
			typ,
			t.Id.Hex,
		)
	}
	return nil
}

func cmdRestore() error {
	w, err := openWallet()
	if err != nil {
		return err
	}
	defer w.Close()
	r, err := w.Restore()
	if err != nil {
		return err
	}
	fmt.Printf("spent:   %d\n", r.Spent.Value)
	fmt.Printf("unspent: %d\n", r.Unspent.Value)
	fmt.Printf("pending: %d\n", r.Pending.Value)
	return nil
}

func cmdPending() error {
	w, err := openWallet()
	if err != nil {
		return err
	}
	defer w.Close()
	ids, err := w.PendingSends()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Println("no pending sends")
		return nil
	}
	for _, id := range ids {
		fmt.Println(id)
	}
	return nil
}

func cmdSmoke(args []string) error {
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	mint := fs.String("mint", wallet.DefaultMint, "mint URL")
	timeout := fs.Duration("timeout", 180*time.Second, "overall timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	return smoke.Run(ctx, *mint, os.Stdout)
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func paymentMethodString(m cdk.PaymentMethod) string {
	switch m.(type) {
	case cdk.PaymentMethodBolt11:
		return "bolt11"
	case cdk.PaymentMethodBolt12:
		return "bolt12"
	case cdk.PaymentMethodOnchain:
		return "onchain"
	}
	return ""
}
