package sliver

import (
	"os"
	"testing"
)

// TestMain pins the machine-address fallback so the resolver tests describe the
// sources they set up rather than whatever interfaces the test runner happens to
// have. Tests that exercise the fallback override localCallbackHost themselves
// and restore it with t.Cleanup.
func TestMain(m *testing.M) {
	localCallbackHost = func() string { return "" }
	os.Exit(m.Run())
}

// stubLocalCallbackHost pins the machine-address fallback for one test.
func stubLocalCallbackHost(t *testing.T, host string) {
	t.Helper()
	prev := localCallbackHost
	localCallbackHost = func() string { return host }
	t.Cleanup(func() { localCallbackHost = prev })
}

// The deployment this console is built for binds the listener to 0.0.0.0 and is
// often opened over loopback, so neither the listener nor the console Host header
// names an address. The machine's own routable address is then the only source
// left, and refusing to use it turned a working command into an error box.
func TestCallbackHostFallsBackToTheMachinesOwnAddress(t *testing.T) {
	stubLocalCallbackHost(t, "10.9.9.9")
	job := &JobView{ID: 7, Name: "http", Port: 8889, CallbackHost: "0.0.0.0", Domains: []string{"0.0.0.0", "::"}}
	got, err := callbackHostForJob(job, "", "")
	if err != nil {
		t.Fatalf("callbackHostForJob: %v", err)
	}
	if got != "10.9.9.9" {
		t.Errorf("= %q, want the machine's own address", got)
	}
}

// The suggestion the console pre-fills has to agree with the resolver, so a
// wildcard listener now yields a usable host rather than an empty field.
func TestSuggestedCallbackHostUsesTheMachinesOwnAddress(t *testing.T) {
	stubLocalCallbackHost(t, "10.9.9.9")
	job := &JobView{ID: 7, Name: "http", Port: 8889, CallbackHost: "0.0.0.0", Domains: []string{"0.0.0.0"}}
	if got := SuggestedCallbackHost(job, ""); got != "10.9.9.9" {
		t.Errorf("= %q, want the machine's own address", got)
	}
}

// The machine's address is the weakest source, so anything that names an address
// still wins: an operator's typed host, the listener's recorded address, and the
// console address, in that order.
func TestMachineAddressIsTheLastResort(t *testing.T) {
	stubLocalCallbackHost(t, "10.9.9.9")

	if got, err := callbackHostForJob(&JobView{ID: 1, Port: 80, CallbackHost: "0.0.0.0", Domains: []string{"0.0.0.0"}}, "203.0.113.5", "198.51.100.9"); err != nil || got != "203.0.113.5" {
		t.Errorf("operator host = %q, %v; want 203.0.113.5", got, err)
	}
	if got, err := callbackHostForJob(&JobView{ID: 2, Port: 80, CallbackHost: "10.1.1.1"}, "", "198.51.100.9"); err != nil || got != "10.1.1.1" {
		t.Errorf("listener address = %q, %v; want 10.1.1.1", got, err)
	}
	if got, err := callbackHostForJob(&JobView{ID: 3, Port: 80, CallbackHost: "0.0.0.0", Domains: []string{"0.0.0.0"}}, "", "198.51.100.9"); err != nil || got != "198.51.100.9" {
		t.Errorf("console address = %q, %v; want 198.51.100.9", got, err)
	}
}

// A fallback that reports a wildcard, a loopback or nothing is not a fallback:
// the resolver must still fail loudly rather than emit a command that dials the
// implant's own loopback.
func TestMachineAddressFallbackRejectsUnusableValues(t *testing.T) {
	for _, host := range []string{"", "0.0.0.0", "::", "127.0.0.1", "localhost"} {
		stubLocalCallbackHost(t, host)
		job := &JobView{ID: 7, Name: "http", Port: 8889, CallbackHost: "0.0.0.0", Domains: []string{"0.0.0.0"}}
		got, err := callbackHostForJob(job, "", "")
		if err == nil {
			t.Errorf("fallback %q produced %q, want an error", host, got)
		}
	}
}
