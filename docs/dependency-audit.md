# Dependency audit — govulncheck

This is the record of the first vulnerability scan this repository has ever had.
It exists so the next run can be compared against a baseline instead of starting
from nothing, and so the findings that are **reachable** are written down rather
than rediscovered.

## How to run the scan

`govulncheck` is not vendored and is not a `go.mod` dependency. Install it once
into `GOPATH/bin`:

```bash
go install golang.org/x/vuln/cmd/govulncheck@latest
```

On Windows, `GOPATH/bin` is not on `PATH` by default, so invoke the full path or
prepend it:

```powershell
$env:PATH="$env:PATH;$(go env GOPATH)\bin"
govulncheck ./...
```

Note on the toolchain: `govulncheck@latest` resolves to `golang.org/x/vuln
v1.8.0`, which declares `go >= 1.26.0`. With `GOTOOLCHAIN=auto` (the default)
the install transparently downloads and uses Go 1.26.8 to build the tool. That
is the scanner's own toolchain only — the module under scan is still built with
the toolchain named in `go.mod` (`go 1.25.6`).

### The three invocations

| # | Command | Where | Purpose |
|---|---------|-------|---------|
| 1 | `govulncheck ./...` | repo root | The main `sliverreshine` module. This is the one that matters. |
| 2 | `govulncheck -show verbose ./...` | repo root | Same scan with the full call-graph listing for every finding. |
| 3 | `govulncheck -tags "server go_sqlite" ./server/...` | `sliver/` | The embedded Sliver server. |

### Gotchas worth knowing before you rerun this

**The `replace` directive does not break the scan.** This module has
`replace github.com/bishopfox/sliver => ./sliver`, and `govulncheck ./...`
resolved it without complaint — the local tree is analysed in place, which is
what we want, because the patches that live there are part of what ships. The
`-mode=source` fallback the task anticipated was **not needed**.

**The sliver scan needs build tags.** A bare `cd sliver && govulncheck
./server/...` fails outright with:

```
govulncheck: loading packages:
There are errors with the provided package patterns:

sliver/server/assets/assets-helpers.go:153:20: undefined: assetsFs
sliver/server/assets/assets.go:143:21: undefined: assetsFs
sliver/server/db/sql.go:44:14: undefined: sqliteClient
```

That is not a govulncheck problem and not a vendoring problem. The Sliver server
is behind build tags: `server/assets/assets_*.go` are all guarded by
`//go:build server`, and the sqlite backend is one of
`cgo_sqlite` / `go_sqlite` / `wasm_sqlite`. Without tags the `assetsFs` and
`sqliteClient` declarations simply are not compiled, so the package does not
type-check. Passing `-tags "server go_sqlite"` makes it scan cleanly. The
`sliver/vendor/` tree was never used and was not modified.

**The scan is not hermetic.** The database is remote
(`https://vuln.go.dev`) and updates independently of this repo, so two runs on
different days can legitimately differ even with no code change. The DB
timestamp is recorded below for that reason.

## Last run

| | |
|---|---|
| **Date** | 2026-10-01 |
| **Scanner** | `govulncheck@v1.8.0` (`golang.org/x/vuln v1.8.0`) |
| **Scanner toolchain** | go1.26.8 (auto-switched during `go install`) |
| **Module toolchain** | go1.25.6 windows/amd64 |
| **Vulnerability DB** | `https://vuln.go.dev`, updated 2026-09-28 16:43:40 UTC |

Raw output is checked in beside this document:

- `govulncheck-main.txt` — invocation 1, verbatim
- `govulncheck-main-verbose.txt` — invocation 2, verbatim
- `govulncheck-sliver-server.txt` — invocation 3, verbatim
- `govulncheck-main.json` — machine-readable form of invocation 1

## Results — main module (`govulncheck ./...`)

Summary line, verbatim:

```
Your code is affected by 24 vulnerabilities from 1 module and the Go standard library.
This scan also found 8 vulnerabilities in packages you import and 11
vulnerabilities in modules you require, but your code doesn't appear to call
these vulnerabilities.
```

Exit code **3** (`govulncheck` uses 3 to mean "vulnerabilities found", not
"the tool crashed").

### The shape of the problem

24 of 24 reachable findings reduce to **two** remediation actions:

1. **Bump the Go toolchain to ≥ 1.25.13.** 21 of the 24 are standard-library
   findings fixed by a patch release, and *every single one* is fixed by
   1.25.13. This is a one-line change to `go.mod`.
2. **Bump `google.golang.org/grpc` from v1.77.0 to ≥ 1.83.1.** 3 of the 24 are
   the gRPC findings, and the strictest floor across them is 1.83.1
   (GO-2026-6348 requires it; GO-2026-6061 is satisfied by 1.82.1).

There is also a third, smaller action: **`golang.org/x/net` v0.48.0 → ≥ v0.55.0**
(GO-2026-5026). That one is listed as reachable in the report, but note that
govulncheck attributes the trace to `net/http` in the *standard library* as
well, so the toolchain bump may cover the reachable path on its own. Bump the
module anyway; it is a direct dependency of this module and it is cheap.

### Full findings, verbatim from the report

All 24 are **reachable** — they appear under `=== Symbol Results ===`, which is
the section govulncheck reserves for vulnerabilities it traced to a call from
this code. (Findings that are present but unreachable land in the "This scan
also found…" tail, not here.)

| # | ID | Affected package | Found in | Fixed in | Reachable? |
|---|----|------------------|----------|----------|------------|
| 1 | GO-2026-6348 | `google.golang.org/grpc` | v1.77.0 | v1.83.1 | **Yes** |
| 2 | GO-2026-6218 | `net/url` (stdlib) | go1.25.6 | go1.25.13 | **Yes** |
| 3 | GO-2026-6091 | `html/template` (stdlib) | go1.25.6 | go1.25.13 | **Yes** |
| 4 | GO-2026-6090 | `crypto/tls` (stdlib) | go1.25.6 | go1.25.13 | **Yes** |
| 5 | GO-2026-6089 | `net/http` (stdlib) | go1.25.6 | go1.25.13 | **Yes** |
| 6 | GO-2026-6061 | `google.golang.org/grpc` | v1.77.0 | v1.82.1 | **Yes** |
| 7 | GO-2026-5972 | `encoding/asn1` (stdlib) | go1.25.6 | go1.25.13 | **Yes** |
| 8 | GO-2026-5856 | `crypto/tls` (stdlib) | go1.25.6 | go1.25.12 | **Yes** |
| 9 | GO-2026-5039 | `net/textproto` (stdlib) | go1.25.6 | go1.25.11 | **Yes** |
| 10 | GO-2026-5037 | `crypto/x509` (stdlib) | go1.25.6 | go1.25.11 | **Yes** |
| 11 | GO-2026-5026 | `golang.org/x/net` (also `net/http`) | v0.48.0 / go1.25.6 | v0.55.0 / go1.25.13 | **Yes** |
| 12 | GO-2026-4982 | `html/template` (stdlib) | go1.25.6 | go1.25.10 | **Yes** |
| 13 | GO-2026-4980 | `html/template` (stdlib) | go1.25.6 | go1.25.10 | **Yes** |
| 14 | GO-2026-4971 | `net` (stdlib) | go1.25.6 | go1.25.10 | **Yes** |
| 15 | GO-2026-4947 | `crypto/x509` (stdlib) | go1.25.6 | go1.25.9 | **Yes** |
| 16 | GO-2026-4946 | `crypto/x509` (stdlib) | go1.25.6 | go1.25.9 | **Yes** |
| 17 | GO-2026-4918 | `golang.org/x/net` (also `net/http`) | v0.48.0 / go1.25.6 | v0.53.0 / go1.25.10 | **Yes** |
| 18 | GO-2026-4870 | `crypto/tls` (stdlib) | go1.25.6 | go1.25.9 | **Yes** |
| 19 | GO-2026-4869 | `archive/tar` (stdlib) | go1.25.6 | go1.25.9 | **Yes** |
| 20 | GO-2026-4865 | `html/template` (stdlib) | go1.25.6 | go1.25.9 | **Yes** |
| 21 | GO-2026-4603 | `html/template` (stdlib) | go1.25.6 | go1.25.8 | **Yes** |
| 22 | GO-2026-4602 | `os` (stdlib) | go1.25.6 | go1.25.8 | **Yes** |
| 23 | GO-2026-4601 | `net/url` (stdlib) | go1.25.6 | go1.25.8 | **Yes** |
| 24 | GO-2026-4337 | `crypto/tls` (stdlib) | go1.25.6 | go1.25.7 | **Yes** |

### What the reachable traces actually are

govulncheck reports these as reachable because this code calls into them. Two
call sites account for most of the standard-library findings:

- **`cmd/sliverreshine/main.go:222` — `http.Server.Serve`.** The console's own HTTP
  listener. It pulls in `net/http`, `html/template`, `crypto/tls`, `net/url`,
  `crypto/x509` and `net` in one go, which is why a single line appears as the
  trace for findings #3, #5, #10, #12, #13, #15, #16, #18, #20, #21 and #23.
  This is the console serving requests — i.e. the exposed surface, and the
  reason a toolchain bump is the highest-value fix here.
- **`internal/ui/sliver/avlookup.go:132` — `http.Client.Do` (AV/hash lookup).**
  Outbound HTTP, tracing `net/url`, `net/http`, `net/textproto`, `net`.
- **`internal/ui/sliver/client.go:209` — `tls.X509KeyPair` (loading the
  operator profile's mTLS material).** Traces `encoding/asn1`.
- **`internal/ui/sliver/data.go:262` — the gRPC event stream `Recv`.** This is
  the trace for both gRPC findings and most of the `crypto/tls` ones, because
  the console↔server link is TLS.
- **`internal/ui/sliver/aliases.go:258` — `tar.Reader.Next`.** Reading the
  alias bundle.
- **`internal/ui/sliver/client.go:129` — `os.ReadDir`.** Listing profiles.

### Severity

govulncheck does not emit CVSS scores, and this document will not invent them.
What can be said from the traces, ranked by how much it should worry an
operator:

- **High — the console listener group (`main.go:222`).** #5 (`net/http`
  ReadHeaderTimeout), #18 (`crypto/tls` KeyUpdate DoS), #8 (ECH privacy leak)
  and #4 (handshake message limits) are all remotely reachable by anyone who
  can open a connection to the console port. On the shipped `0.0.0.0:8080`
  default that is anyone on the network. #3/#12/#13/#20/#21 are XSS-class
  `html/template` issues, but note this codebase serves an embedded React SPA
  and generated JSON rather than server-side templates, so the practical
  exposure is narrower than the label suggests.
- **High — the gRPC group (#1, #6).** #1 is unauthenticated memory exhaustion
  over HTTP/2; #6 includes an HTTP/2 server transport flaw. The embedded
  server's gRPC port is loopback-only by default (`MultiplayerHost` =
  `127.0.0.1`), which contains this, but the console's *client* side is what
  govulncheck traced.
- **Medium — #7, #10, #15, #16 (`asn1`, `x509`).** Reached through parsing the
  operator profile's own certificate material, i.e. a file the operator
  supplied, not remote input.
- **Medium — #2, #23 (`net/url`).** Reached through parsing URLs; #23 is
  explicitly IPv6-host-literal parsing.
- **Lower — #9, #14, #19, #22.** `net/textproto`, `net`, `archive/tar` and `os`
  are reached through the AV-lookup client, the alias bundle reader and profile
  listing — inputs that are either operator-controlled or an already-trusted
  local file.

None of these are known-exploited-in-the-wild, and none are a credential leak.
The honest framing is: **the dependency set is stale, not broken.** The fix is
a toolchain and module bump, not an architecture change.

## Results — embedded sliver server (`sliver/`, tags `server go_sqlite`)

Summary line, verbatim:

```
Your code is affected by 36 vulnerabilities from 6 modules and the Go standard library.
This scan also found 11 vulnerabilities in packages you import and 26
vulnerabilities in modules you require, but your code doesn't appear to call
these vulnerabilities.
```

All 36 are reachable. The non-standard-library ones, which are the ones a
toolchain bump alone will not fix:

| ID | Affected module | Found in | Fixed in |
|----|-----------------|----------|----------|
| GO-2026-6443 | `google.golang.org/grpc` | v1.77.0 | v1.82.2 |
| GO-2026-6348 | `google.golang.org/grpc` | v1.77.0 | v1.83.1 |
| GO-2026-6061 | `google.golang.org/grpc` | v1.77.0 | v1.82.1 |
| GO-2026-4762 | `google.golang.org/grpc` | v1.77.0 | v1.79.3 |
| GO-2026-5970 | `golang.org/x/text` | v0.32.0 | v0.39.0 |
| GO-2026-5506 | `go.opentelemetry.io/otel` | v1.39.0 | v1.41.0 |
| GO-2026-5004 | `github.com/jackc/pgx/v5` | v5.7.6 | v5.9.2 |
| GO-2026-4945 | `github.com/go-jose/go-jose/v4` | v4.1.3 | v4.1.4 |
| GO-2026-4394 | `go.opentelemetry.io/otel/sdk` | v1.39.0 | v1.40.0 |

The remaining 27 are standard library, all fixed by go1.25.13 (the same
toolchain bump that fixes the main module).

Two of these deserve a flag rather than a table row:

- **GO-2026-4762 — gRPC authorization bypass via a missing leading slash in
  `:path`.** This is the same gRPC version the console links against, so the
  main module is exposed by the same underlying version; fixing the console's
  `grpc` require fixes this too.
- **GO-2026-4394 — OpenTelemetry SDK arbitrary code execution via `PATH`
  hijacking.** `sliver/server/notifications/builder.go` reaches it through
  `fcm`. It is an `init`-time path, so it needs local influence over `PATH`,
  but it is worth noting that the Sliver server binary is the thing that gets
  unpacked and executed as part of this deployment.

### Caveat on the sliver scan

`sliver/go.mod` is a separate module with its own `require` block, and
`sliver/vendor/` is present. This scan analysed the module's own dependency
graph; it did not attempt to reconcile it against the vendored tree, and no
file under `sliver/vendor/` was read for the purposes of this audit. Treat the
sliver numbers as "what the module resolves to", which is what matters for the
binary this project builds from it.

## Remediation plan, in priority order

1. **Bump the Go toolchain to ≥ 1.25.13** in `go.mod` and in the build image.
   Clears 21 of 24 main-module findings and 27 of 36 sliver findings in one
   change. Verify with `govulncheck ./...` afterwards.
2. **Bump `google.golang.org/grpc` to ≥ 1.83.1** in both `go.mod` files.
   Clears the remaining 3 main-module findings. Note the `replace` directive:
   `google.golang.org/grpc` is required directly by the main module, so the
   bump belongs in the root `go.mod`; the sliver tree has its own requirement.
3. **Bump `golang.org/x/net` to ≥ v0.55.0** and `golang.org/x/text` to
   ≥ v0.39.0. Direct dependencies; cheap.
4. **Re-run all three invocations** and diff against this document. If the
   summary lines change, update this file rather than adding a second one.

Items 1–3 were **not** applied as part of this audit. This task was scoped to
*scanning* and recording; a toolchain bump rebuilds the shipped binary, changes
the embedded server's build, and interacts with the release pipeline, so it
belongs in its own change with its own verification rather than folded into a
documentation commit. The findings above are the input to that change.

## Honest bottom line

The repo had never been scanned and the result is **not** a clean bill of
health: 24 reachable findings in the module that ships, 36 in the embedded
server. But 48 of the 60 total collapse into "the Go toolchain is two patch
releases behind", which is a `go.mod` edit and a rebuild — not a redesign. This
is a stale-pins problem, and it is now measured rather than assumed.
