package api

import (
	"net/http"
	"regexp"
	"strings"

	"google.golang.org/grpc/status"
)

// A gRPC error is not an HTTP status, and the console was treating every one it
// did not recognise as 500. That made a caller's own mistake look like a server
// fault: asking for a loot item or beacon that does not exist produced
//
//	500 {"error":"rpc error: code = Internal desc = invalid loot id"}
//
// which says the console broke rather than that the ID was wrong. An operator
// reading that goes looking for a defect in the console, and a client library
// retries it.
//
// The mappings below are the ones the server actually produces on a bad
// identifier. They are keyed on both the gRPC code and the message text, because
// sliver-server grades some of these as Internal -- "invalid loot id" and
// "Database operation failed" both arrive as codes.Internal -- so the code alone
// cannot distinguish "the record is missing" from "the database is down", and
// mapping all Internal to 404 would be worse than the bug being fixed.
//
// The text match is deliberately narrow. A message that is not recognised stays
// a 500, because an unmapped error is more likely a real fault than a bad
// request, and inventing a friendly status for it would hide the fault.

// transportMarkers identify a failure to reach an upstream the console depends
// on. They are checked FIRST, because several of them contain phrases the
// not-found list would otherwise claim: "no such host" is a DNS failure, not a
// 404, and reporting it as "the thing you asked for does not exist" sends the
// operator looking for the wrong problem.
//
// These map to 502 rather than 503, and the distinction is not cosmetic. 503 is
// what this console already returns when it has lost its sliver-server link, and
// that is a state the operator must be told about loudly. A 502 says "an
// upstream I depend on failed to answer", which is what an unreachable
// process-identification service is -- a dependency problem, not a console
// outage. Collapsing the two would make an optional service being down look like
// the C2 itself going away.
var transportMarkers = []string{
	"connection refused",
	"connection reset",
	"connection error",
	"no such host",
	"dial tcp",
	"transport:",
	"i/o timeout",
	"context deadline exceeded",
	"unavailable",
}

// notFoundMarkers are the server's ways of saying a record does not exist.
//
// They are specific phrases rather than a bare "not found", because the bare
// form also matches transport and filesystem messages and would turn those into
// 404s. A phrase that is not listed here stays a 500, which is the safe
// direction: an unrecognised error is more likely a real fault than a bad
// request.
var notFoundMarkers = []string{
	"invalid loot id",
	"invalid beacon id",
	"invalid session id",
	"invalid implant id",
	"invalid task id",
	"invalid taskid",
	"record not found",
	"host not found",
	"credential not found",
	"session not found",
	"beacon not found",
	"implant build not found",
	"no credentials supplied",
	// One-liner wording for a listener that does not exist.
	"no listener with job id",
	// "no traffic encoder named \"x\"" and "alias \"x\" is not installed" say
	// the same thing in the server's other idiom: the named thing is absent.
	"no traffic encoder named",
	"no shellcode encoder named",
	"is not installed",
	"not installed",
}

// notFoundPatterns catch the server's other shape: a noun, an identifier, then
// "not found". "<noun> <id> not found" cannot be listed as a phrase because the
// identifier varies, and matching a bare "not found" would also claim transport
// messages.
var notFoundPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\b(session|beacon|host|implant|credential|loot|task|alias|profile|website|canary)\b[^.]{0,64}\bnot found\b`),
	regexp.MustCompile(`\bno (session|beacon|host|implant|credential|loot|task|profile)\b[^.]{0,32}\bfound\b`),
}

// invalidMarkers are the server's ways of saying the request itself was wrong.
var invalidMarkers = []string{
	"invalid argument",
	"invalid request",
	"invalid id",
	"invalid name",
	"invalid file",
	"invalid compiler target",
	"invalid output format",
	"unsupported listener type",
	"unsupported os",
	"unsupported arch",
	"unsupported format",
	"must be",
	"is required",
	"cannot be empty",
	"illegal base64",
	"no profile supplied",
	// The console's own write-path validation: a stored credential may not
	// take the reserved "apikey" username, which the vault uses as the marker
	// for an API key. The error is produced here rather than by the server, so
	// no gRPC code carries it, and without this the caller's mistake reached
	// the operator as a 500.
	"is reserved for",
	// A delivery method that does not match the requested platform, and a
	// listener type that cannot serve a stage: both are the caller asking for
	// something that cannot work, not the console failing.
	"is for windows, but the target is",
	"is for linux, but the target is",
	"cannot serve a stage",
	"was not started by this console",
	"no file supplied",
	"no data supplied",
}

// notImplementedMarkers identify a feature the connected server build does not
// have. That is not the caller's fault and not a console fault either, so it
// gets 501 rather than 500 -- the operator needs to know the request was
// understood and simply cannot be served here.
var notImplementedMarkers = []string{
	"is not supported by the connected sliver-server",
	"not supported by the connected",
	"unknown message type",
	"unimplemented",
}

// httpStatusForError picks the HTTP status for an error from the sliver client.
//
// It answers 500 only when nothing better is known, which keeps a genuine fault
// visible instead of disguising it as a 404.
func httpStatusForError(err error) int {
	if err == nil {
		return http.StatusOK
	}

	// A gRPC status carries the code and the server's message.
	if st, ok := status.FromError(err); ok {
		switch st.Code().String() {
		case "NotFound":
			return http.StatusNotFound
		case "InvalidArgument", "OutOfRange", "FailedPrecondition":
			return http.StatusBadRequest
		case "PermissionDenied":
			return http.StatusForbidden
		case "Unauthenticated":
			return http.StatusUnauthorized
		case "AlreadyExists", "Aborted":
			return http.StatusConflict
		case "Unavailable", "DeadlineExceeded":
			return http.StatusServiceUnavailable
		case "Unimplemented":
			return http.StatusNotImplemented
		}
	}

	// The server grades a missing record as Internal in several handlers, so the
	// message is consulted when the code does not settle it.
	msg := strings.ToLower(err.Error())

	// Reachability first: "no such host" must not read as "no such record".
	for _, m := range transportMarkers {
		if strings.Contains(msg, m) {
			return http.StatusBadGateway
		}
	}
	for _, m := range notImplementedMarkers {
		if strings.Contains(msg, m) {
			return http.StatusNotImplemented
		}
	}
	for _, m := range notFoundMarkers {
		if strings.Contains(msg, m) {
			return http.StatusNotFound
		}
	}
	for _, re := range notFoundPatterns {
		if re.MatchString(msg) {
			return http.StatusNotFound
		}
	}
	for _, m := range invalidMarkers {
		if strings.Contains(msg, m) {
			return http.StatusBadRequest
		}
	}
	return http.StatusInternalServerError
}

// writeClientError answers with the status the error deserves rather than a
// blanket 500.
//
// It also unwraps the gRPC prefix. "rpc error: code = Internal desc = invalid
// loot id" tells an API consumer nothing they can act on; "invalid loot id" is
// the whole message.
func writeClientError(w http.ResponseWriter, err error) {
	writeErr(w, httpStatusForError(err), clientErrorMessage(err))
}

// clientErrorMessage strips the transport framing from a gRPC error.
//
// The full string is useful in a log and noise in a response body, and the
// console already logs the request.
func clientErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if st, ok := status.FromError(err); ok {
		if msg := strings.TrimSpace(st.Message()); msg != "" {
			return msg
		}
	}
	return err.Error()
}
