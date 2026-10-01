package api

import (
	"errors"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
