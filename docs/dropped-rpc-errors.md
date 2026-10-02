# Dropped RPC reply errors

## The pattern

Sliver has two error channels, and only one of them is idiomatic Go.

1. **The gRPC status.** A call fails, the generated stub returns a non-nil
   `error`, and the usual `if err != nil` catches it.

2. **The in-band reply field.** The call *succeeds*, the implant reports a
   failure, and the message comes back inside `commonpb.Response.Err`. Nothing
   in the type signature says so. The reply struct looks like any other, and the
   `error` is nil.

Channel 2 is invisible unless the caller explicitly reads it. Every defect in
this document was a caller that did not.

## Why it depends on the server handler

Whether channel 2 is reachable at all is decided on the server, and it differs
per RPC. This is the part that makes the bug easy to miss by reading the console
code alone.

**`GenericHandler`-routed methods.** Most of the surface. `GenericHandler` ends
with:

```go
// sliver/server/rpc/rpc.go
return rpcError(rpc.getError(resp))
```

and `getError` promotes the field:

```go
func (rpc *Server) getError(resp GenericResponse) error {
	respHeader := resp.GetResponse()
	if respHeader != nil && respHeader.Err != "" {
		return status.Error(codes.FailedPrecondition, respHeader.Err)
	}
	return nil
}
```

So for these, `Response.Err` never survives to the console. It has already
become a gRPC status, and `if err != nil` is sufficient. Discarding the reply is
*correct*.

**Methods that talk to the session directly.** `Shell` and `Portfwd` marshal the
request and call `session.Request` themselves, then return the implant's reply
verbatim:

```go
// sliver/server/rpc/rpc-portfwd.go
data, err := session.Request(sliverpb.MsgNumber(req), s.getTimeout(req), reqData)
...
return portfwd, nil
```

No promotion. `Response.Err` travels intact into the console, where it is only
visible if someone looks.

That asymmetry is why a blanket "always check `Response.Err`" rule is wrong: it
produces dozens of findings for methods where the check cannot ever fire, and
the real ones get lost in the noise.

## The defect that was found

`internal/ui/sliver/portfwd.go`, `PortForward.handleConn`:

```go
_, err = pf.mgr.client.RPC.Portfwd(ctx, &sliverpb.PortfwdReq{...})
if err != nil {
    tunnel.close()
    return
}
```

`Portfwd` is not a `GenericHandler` method. When the implant cannot reach the
target — connection refused, filtered, wrong host — it answers with a
**successful** reply whose `Response.Err` describes the failure. The console
discarded that reply.

Consequences, in order:

- The forward registered the tunnel as established.
- The browser's connection was accepted and left open.
- `io.Copy(conn, tunnel)` blocked forever, because nothing would ever write to
  that tunnel.
- The operator saw a port forward listed as running, a browser tab that never
  loaded, and no error anywhere.

This is the same shape as the other defects this console has had: **a thing does
not take effect while every step reports success.**

### The fix

Read the field, and record the reason on the forward so the failure is
attributable:

```go
resp, err := pf.mgr.client.RPC.Portfwd(ctx, &sliverpb.PortfwdReq{...})
if err != nil {
	tunnel.close()
	pf.recordConnError(err)
	return
}
if errMsg := resp.GetResponse().GetErr(); errMsg != "" {
	tunnel.close()
	pf.recordConnError(fmt.Errorf("target %s:%d: %s", pf.Host, pf.Port, errMsg))
	return
}
```

`recordConnError` stores the message in `PortForward.lastConnErr`, which
`List()` reports as `LastConnErr`. The forward table gained a status column that
distinguishes "listening" from "failing", with the underlying message in the
tooltip. A refused target and a filtered one are now different rows.

## The tool

`tools/rpcerrcheck` classifies every `SliverRPCClient` call site:

| Class | Meaning |
|---|---|
| `OK` | No `Err` field, or it is inspected, or the server already promoted it |
| `DISCARDED` | The reply was thrown away and its `Err` was reachable |
| `UNCHECKED` | The reply was captured but its `Err` is never read |

It reads four things to decide:

1. The generated protobuf, to learn which reply types carry `Err`.
2. The generated gRPC interface, to map a method to its reply type.
3. The server handlers, to learn which ones call `GenericHandler`.
4. The console source, to find the call sites and whether they read the field.

It follows package-local indirection, so a wrapper that checks the error counts
for its callers:

```go
return execResultFrom(resp)   // OK -- execResultFrom reads resp.Response.Err
```

A call that genuinely intends to ignore the reply says so explicitly:

```go
//rpcerrcheck:ok best-effort; a failure here must not fail the caller
```

### Call shapes it covers

A dropped reply is a dropped reply regardless of the syntax, so all three are
classified:

```go
_, err := c.RPC.Portfwd(ctx, req)          // plain assignment
if _, err := c.RPC.Portfwd(ctx, req); ...  // if initialiser
return c.RPC.Generate(ctx, req)            // delegated to the caller
```

The `if` initialiser form is worth calling out: it is the shape most likely to
hide a dropped reply, because the code *looks* like it is checking something.

### Running it

```sh
go run ./tools/rpcerrcheck -root .
go run ./tools/rpcerrcheck -root . -v      # every call site, not just findings
go run ./tools/rpcerrcheck -root . -json
```

Exit status is 1 when any finding is present, so it is usable as a gate. The
package tests enforce it through `go test ./tools/rpcerrcheck/`, which means the
existing `go test ./...` runs it without any extra wiring.

## Tests

`internal/ui/sliver/portfwd_error_test.go` covers the defect from both sides:

| Test | Assertion |
|---|---|
| `TestPortForwardRefusedTargetIsReported` | A refused target records the implant's message and registers no tunnel |
| `TestPortForwardDialFailureClosesConnection` | The local socket is released rather than left hanging |
| `TestPortForwardSuccessfulReplyIsNotReportedAsFailure` | An empty `Response.Err` is not treated as an error |
| `TestPortForwardTransportErrorIsRecorded` | The gRPC-error path stays distinguishable |

The regression tests were verified by reverting the fix. Against the old code
they do not merely fail — they hang for the full timeout, which is precisely
what the operator experienced:

```
--- FAIL: TestPortForwardRefusedTargetIsReported (5.00s)
    handleConn did not return after the target refused the connection
--- FAIL: TestPortForwardDialFailureClosesConnection (5.00s)
    handleConn blocked after the implant reported a dial failure
```

`tools/rpcerrcheck/main_test.go` guards the analyzer itself, including the
server-routing scan. If that scan ever broke, the tool would either report
dozens of false findings or report nothing ever — both of which end with someone
deleting it.

## The audit result

Full sweep of the console's RPC surface, after the fix:

```
167 call sites: 167 OK, 0 DISCARDED, 0 UNCHECKED
```

`Portfwd` was the only reachable dropped error. The other candidates the audit
surfaced along the way were false positives of my own analysis, not product
defects, and each is worth recording so the check is not repeated:

| Candidate | Why it is fine |
|---|---|
| `Reconfigure`, `OpenSession`, `GetPrivs` | `GenericHandler`-routed; the server promotes `Err` |
| `Execute`, `ExecuteWindows` | Reply passed to `execResultFrom`, which reads `Response.Err` |
| `Generate` | Reply returned to the caller; `clientpb.Generate` has no `Err` field |
| `RmBeacon`, `GetBeacons` | Reply type is `commonpb.Empty` or has no `Err` field |

## What this does not cover

- **Streaming RPCs.** `TunnelData` and `SocksProxy` carry errors on the stream,
  not in a reply struct. The analyzer skips them.
- **Errors that are not in `Response.Err`.** A reply can embed a failure in a
  field the analyzer does not model, such as `Download.Exists == false`. Those
  need reading the handler.
- **Consumers other than `internal/`.** The scan is rooted there. If a new
  package starts calling the RPC surface, point `-root` at it too.
