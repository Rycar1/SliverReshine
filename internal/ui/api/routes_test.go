package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoutesAcceptDynamicResourceIDs(t *testing.T) {
	tests := []struct {
		name string
		req  *http.Request
	}{
		{
			name: "session kill",
			req:  httptest.NewRequest(http.MethodPost, "/api/sessions/session-123/kill", nil),
		},
		{
			name: "session filesystem",
			req:  httptest.NewRequest(http.MethodGet, "/api/sessions/session-123/fs", nil),
		},
		{
			name: "beacon tasks",
			req:  httptest.NewRequest(http.MethodGet, "/api/beacons/beacon-123/tasks", nil),
		},
		{
			name: "overview",
			req:  httptest.NewRequest(http.MethodGet, "/api/overview", nil),
		},
		{
			name: "listener stop",
			req:  httptest.NewRequest(http.MethodDelete, "/api/listeners/123", nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The mutating routes need the Content-Type the CSRF guard requires;
			// without it the 415 would be reported as "the handler did not match".
			prepareMutation(tt.req)
			rec := httptest.NewRecorder()
			New().Routes().ServeHTTP(rec, tt.req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("route status = %d, want %d (503 proves the handler matched)", rec.Code, http.StatusServiceUnavailable)
			}
		})
	}
}
