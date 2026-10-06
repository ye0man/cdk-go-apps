# cdk-go-apps

A lightweight Cashu wallet CLI and an end-to-end release smoke runner, built on
the [`github.com/cashubtc/cdk-go`](https://github.com/cashubtc/cdk-go) Go
bindings.

The repo exists to **dogfood the Go bindings**: it consumes cdk-go as a normal
downstream dependency (a published tag, pulled from the module proxy) and
exercises the wallet API against a real mint. If a cdk-go release regresses, the
scheduled smoke run here fails.

## What's here

- `cmd/cashu` — a single-binary wallet CLI: mint, send, receive, swap, melt,
  pay NUT-18 requests, decode tokens/requests/invoices, history, restore.
- `internal/wallet` — a thin, idiomatic wrapper over the FFI handles.
- `internal/smoke` — a headless end-to-end flow used for release validation.
- `internal/wallet/*_test.go` — offline tests (no network).

## Requirements

- Go 1.22+
- A C toolchain and `CGO_ENABLED=1` (the bindings are compiled FFI). On Windows,
  a MinGW `gcc` on `PATH`.
- The matching prebuilt native library, which ships inside the cdk-go module.

### Native library discovery

cdk-go's build tags add `-Wl,-rpath,<module-cache>/native/<os>_<arch>` on Linux
and macOS, so the shared library is found automatically. Windows has no rpath:
the DLL must be discoverable at runtime. Either add the native directory to
`PATH`, or copy the DLL next to the binary:

```powershell
$native = "$(go env GOMODCACHE)\github.com\cashubtc\cdk-go@v0.18.1\bindings\cdkffi\native\windows_amd64"
$env:Path = "$native;$env:Path"
```

## Build and run

```bash
go build -o bin/cashu ./cmd/cashu

cashu init                      # create a config + mnemonic
cashu balance
cashu mint-quote --amount 64 --wait   # testnut auto-pays, then mints
cashu send --amount 21          # prints a token
cashu receive <token>
cashu swap                      # re-split all unspent proofs in place
cashu decode <token|request|invoice>
cashu history
cashu restore
```

Config and wallet state live under the OS user config dir (`%AppData%\cashu` on
Windows, `~/.config/cashu` on Linux, `~/Library/Application Support/cashu` on
macOS).

## Smoke runner

```bash
cashu smoke --mint https://testnut.cashudevkit.org
```

The flow opens two in-memory wallets, mints, sends a token, decodes it offline,
receives it in the second wallet, verifies the spent proofs, re-splits the
balance, lists history, and restores. Each step prints `[ok] ...`; the run ends
with `PASS` and a non-zero exit on any failed assertion.

`testnut.cashudevkit.org` auto-pays BOLT11 mint quotes, so no Lightning backend
is needed. Melt/on-chain flows need a mint with a working payment backend and are
intentionally not part of the CI smoke.

## Testing a cdk-go release

- **CI** (`.github/workflows/ci.yml`) builds, vets, runs offline tests and the
  testnut smoke across linux/amd64, linux/arm64, darwin/arm64 and windows/amd64.
- **Scheduled smoke** (`.github/workflows/scheduled-smoke.yml`) runs daily,
  testing the latest **stable** and **nightly** cdk-go tags, uploading a report
  and opening/updating an issue on failure. Dispatch it manually, optionally with
  a specific version:

  ```
  gh workflow run scheduled-smoke.yml -f version=v0.18.1
  ```

No secrets are required: the module proxy and testnut are public.

## Notes

- The repo pins a published cdk-go tag in `go.mod`; there is no `replace`
  directive, so the tested artifact is exactly what a downstream consumer
  resolves.
- `Wallet.Swap` semantics worth knowing: the cdk `swap(amount, ...)` call treats
  `amount` as the portion *returned to the caller* (a withdraw), leaving only the
  change in the wallet. To re-split a balance in place, pass `amount = nil` with
  an explicit split target — which is what `internal/wallet` does.
