package api

import (
	"errors"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http"
	"sliverreshine/internal/ui/sliver"
	"testing"
)

// Every gRPC error the console did not recognise used to become a 500, which
// made a caller's own mistake look like a console fault. Asking for a loot item
// or a beacon that does not exist produced
//
//	500 {"error":"rpc error: code = Internal desc = invalid loot id"}
//
// An operator reading that goes looking for a defect in the console, and a
// client library retries it. These tests pin the mapping.
//
// The tricky part is that sliver-server grades a missing record as
// codes.Internal, so the code alone cannot decide. Mapping all Internal to 404
// would hide a real database failure, which is why the text is consulted and an
// unrecognised Internal stays a 500.

func TestHTTPStatusForErrorUsesTheGRPCCodeWhenItIsClear(t *testing.T) {
	cases := []struct {
		code codes.Code
		want int
	}{
		{codes.NotFound, http.StatusNotFound},
		{codes.InvalidArgument, http.StatusBadRequest},
		{codes.PermissionDenied, http.StatusForbidden},
		{codes.Unauthenticated, http.StatusUnauthorized},
		{codes.Unavailable, http.StatusServiceUnavailable},
		{codes.DeadlineExceeded, http.StatusServiceUnavailable},
		{codes.Unimplemented, http.StatusNotImplemented},
		{codes.AlreadyExists, http.StatusConflict},
	}
	for _, tc := range cases {
		err := status.Error(tc.code, "something")
		if got := httpStatusForError(err); got != tc.want {
			t.Errorf("%v -> %d, want %d", tc.code, got, tc.want)
		}
	}
}

// The server's own messages for a missing record, which arrive as Internal.
func TestHTTPStatusForErrorReadsTheMessageWhenTheCodeIsInternal(t *testing.T) {
	cases := []struct {
		msg  string
		want int
	}{
		{"invalid loot id", http.StatusNotFound},
		{"Invalid beacon ID", http.StatusNotFound},
		{"invalid session ID", http.StatusNotFound},
		{"record not found", http.StatusNotFound},
		{"invalid request", http.StatusBadRequest},
		{"path must be set", http.StatusBadRequest},
	}
	for _, tc := range cases {
		err := status.Error(codes.Internal, tc.msg)
		if got := httpStatusForError(err); got != tc.want {
			t.Errorf("Internal %q -> %d, want %d", tc.msg, got, tc.want)
		}
	}
}

// The whole point of consulting the text is that an unrecognised Internal stays
// a 500. Mapping it to 404 would turn "the database is down" into "your ID was
// wrong", which is the same class of misdirection as the bug being fixed.
func TestHTTPStatusForErrorKeepsAnUnrecognisedInternalAs500(t *testing.T) {
	err := status.Error(codes.Internal, "Database operation failed")
	if got := httpStatusForError(err); got != http.StatusInternalServerError {
		t.Errorf("an unmapped Internal became %d; a real fault must stay visible", got)
	}
}

func TestHTTPStatusForErrorHandlesPlainErrors(t *testing.T) {
	if got := httpStatusForError(nil); got != http.StatusOK {
		t.Errorf("nil -> %d, want 200", got)
	}
	if got := httpStatusForError(errors.New("the vault is unwritable")); got != http.StatusInternalServerError {
		t.Errorf("an ordinary error -> %d, want 500", got)
	}
	// A non-gRPC error whose text looks like a bad request is still mapped, so
	// the client's own validation errors are not reported as faults either.
	if got := httpStatusForError(errors.New("name is required")); got != http.StatusBadRequest {
		t.Errorf("a validation error -> %d, want 400", got)
	}
}

// The response body should carry the reason, not the transport framing. The
// gRPC prefix tells an API consumer nothing they can act on.
func TestClientErrorMessageStripsTheGRPCFraming(t *testing.T) {
	err := status.Error(codes.Internal, "invalid loot id")
	got := clientErrorMessage(err)
	if got != "invalid loot id" {
		t.Errorf("= %q, want the bare message", got)
	}
	if got == err.Error() {
		t.Error("the transport prefix was left in the message")
	}

	if got := clientErrorMessage(errors.New("plain")); got != "plain" {
		t.Errorf("a plain error lost its text: %q", got)
	}
	if got := clientErrorMessage(nil); got != "" {
		t.Errorf("nil -> %q, want empty", got)
	}
}

// The categories the route sweep actually produced, so the classifier is tested
// against the real messages rather than invented ones.
func TestHTTPStatusForErrorAgainstRealServerMessages(t *testing.T) {
	cases := []struct {
		msg  string
		want int
	}{
		// A missing record, which sliver grades as Internal.
		{"Invalid session ID", http.StatusNotFound},
		{"Invalid beacon ID", http.StatusNotFound},
		{"record not found", http.StatusNotFound},
		{"Credential not found", http.StatusNotFound},
		{"session no-such-thing not found", http.StatusNotFound},
		{"no credentials supplied", http.StatusNotFound},
		{"no traffic encoder named \"no-such-thing\"", http.StatusNotFound},
		{"alias \"no-such-thing\" is not installed", http.StatusNotFound},
		// The request was malformed.
		{"session id is required", http.StatusBadRequest},
		{"profile name is required", http.StatusBadRequest},
		{"hostname is required", http.StatusBadRequest},
		{"no profile supplied", http.StatusBadRequest},
		{"invalid file data: illegal base64 data at input byte 0", http.StatusBadRequest},
		{"unsupported listener type \"x\"", http.StatusBadRequest},
		{"unsupported os \"plan9\"", http.StatusBadRequest},
		{"invalid compiler target: windows/mips", http.StatusBadRequest},
		// The server build lacks the feature. Not the caller's fault, not a
		// console fault: 501 says "understood, cannot serve".
		{"ExecuteToken is not supported by the connected sliver-server version", http.StatusNotImplemented},
		{"MSF stager generation is not supported by the connected sliver-server version", http.StatusNotImplemented},
		{"rpc error: code = Unimplemented desc = unknown message type", http.StatusNotImplemented},
	}
	for _, tc := range cases {
		err := status.Error(codes.Internal, tc.msg)
		if got := httpStatusForError(err); got != tc.want {
			t.Errorf("Internal %q -> %d, want %d", tc.msg, got, tc.want)
		}
	}
}

// Reachability failures must not be reported as "the thing you asked for does
// not exist". "no such host" is a DNS failure, and a bare "not found" match
// would have turned it into a 404 and sent the operator looking for a typo.
func TestHTTPStatusForErrorDoesNotMistakeTransportFailuresForMissingRecords(t *testing.T) {
	cases := []string{
		`rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:31337: connectex: No connection could be made because the target machine actively refused it."`,
		"dial tcp: lookup sliver.example on 1.1.1.1:53: no such host",
		"context deadline exceeded",
		"connection reset by peer",
	}
	for _, msg := range cases {
		got := httpStatusForError(errors.New(msg))
		if got == http.StatusNotFound {
			t.Errorf("%q became a 404; a reachability failure must not read as a missing record", msg)
		}
		if got != http.StatusBadGateway {
			t.Errorf("%q -> %d, want 502 (an upstream failed, which is not the console's own outage)", msg, got)
		}
	}
}

// A build that failed for want of a C toolchain on the console's host is a
// server fault, not a missing record. The hint names the compiler as "not
// installed", which the not-found marker list claims, so this used to be
// answered with a 404 -- telling the operator the target does not exist when
// the fix was to install gcc. The error's type settles it before the text is
// consulted.
func TestHTTPStatusForErrorTreatsAMissingToolchainAsAServerFault(t *testing.T) {
	err := sliver.NewMissingToolchainError(
		"this host cannot build linux shared libraries: gcc is not installed",
		errors.New(`exec: "gcc": executable file not found in $PATH`),
	)
	if got := httpStatusForError(err); got != http.StatusInternalServerError {
		t.Errorf("a missing toolchain produced %d, want %d", got, http.StatusInternalServerError)
	}
	wrapped := fmt.Errorf("generate stage: %w", err)
	if got := httpStatusForError(wrapped); got != http.StatusInternalServerError {
		t.Errorf("a wrapped missing toolchain produced %d, want %d", got, http.StatusInternalServerError)
	}
}
