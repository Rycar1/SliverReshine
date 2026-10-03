package api

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// TestClampRPCTimeoutCapsWhatACallerCanAskFor pins the cap that exists because
// the timeout field is caller-controlled: without it a request could ask for a
// year and the handler would hold a context, a goroutine and an open response
// for that whole time, with nothing downstream to cut it short (the HTTP server
// has no write timeout).
func TestClampRPCTimeoutCapsWhatACallerCanAskFor(t *testing.T) {
	capSeconds := int(maxRPCTimeout / time.Second)
	cases := []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{"absent uses the default", 0, defaultRPCTimeout},
		{"negative uses the default", -1, defaultRPCTimeout},
		{"shorter request is honoured", 5, 5 * time.Second},
		{"exactly the cap", capSeconds, maxRPCTimeout},
		{"one second over the cap", capSeconds + 1, maxRPCTimeout},
		{"a year is capped", 365 * 24 * 3600, maxRPCTimeout},
		{"absurd values must not overflow into an expired deadline", math.MaxInt, maxRPCTimeout},
	}
	for _, tc := range cases {
		if got := clampRPCTimeout(tc.seconds); got != tc.want {
			t.Errorf("%s: clampRPCTimeout(%d) = %v, want %v", tc.name, tc.seconds, got, tc.want)
		}
	}
}

// A cap is only meaningful if it is ordered: a default above the maximum, or a
// maximum so large it may as well be absent, would satisfy the table above while
// leaving the exposure in place.
func TestRPCTimeoutConstantsAreOrdered(t *testing.T) {
	if defaultRPCTimeout <= 0 || maxRPCTimeout <= 0 {
		t.Fatalf("timeouts must be positive: default=%v max=%v", defaultRPCTimeout, maxRPCTimeout)
	}
	if defaultRPCTimeout > maxRPCTimeout {
		t.Fatalf("default %v exceeds the maximum %v", defaultRPCTimeout, maxRPCTimeout)
	}
	if maxRPCTimeout > 10*time.Minute {
		t.Fatalf("maximum %v is not a bound worth having", maxRPCTimeout)
	}
	if got := clampRPCTimeout(1); got != time.Second {
		t.Fatalf("a one-second request must not be rounded up, got %v", got)
	}
}

// The handler must go through the clamp. A table test on the helper alone keeps
// passing if someone inlines the old verbatim assignment back into the handler,
// which is exactly how the exposure was introduced.
func TestHandleRPCCallUsesTheTimeoutClamp(t *testing.T) {
	src, err := os.ReadFile("rawrpc.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "clampRPCTimeout(req.Timeout)") {
		t.Fatal("handleRPCCall must clamp the caller-supplied timeout")
	}
	if strings.Contains(body, "time.Duration(req.Timeout) * time.Second") {
		t.Fatal("the verbatim timeout conversion must not come back")
	}
}
