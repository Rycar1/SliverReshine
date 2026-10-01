package api

import (
	"net/http"
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

// notFoundMarkers are the server's ways of saying a record does not exist.
var notFoundMarkers = []string{
	"invalid loot id",
	"invalid beacon id",
	"invalid session id",
	"invalid implant id",
	"invalid task id",
	"invalid taskid",
	"record not found",
	"no such",
	"not found",
}

// invalidMarkers are the server's ways of saying the request itself was wrong.
var invalidMarkers = []string{
	"invalid argument",
	"invalid request",
	"invalid id",
	"must be",
	"required",
	"cannot be empty",
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
	for _, m := range notFoundMarkers {
		if strings.Contains(msg, m) {
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
