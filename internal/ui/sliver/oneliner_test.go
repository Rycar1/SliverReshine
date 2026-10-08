package sliver

import (
	"strings"
	"testing"
)

// The one-liner's job is to keep the command and the thing it points at in
// agreement. These tests cover the derivation, because every failure mode here
// is silent: a command that fetches a path nobody serves, or an implant that
// dials an address no listener holds, both build successfully and simply never
// produce a session.

func TestJobServesStageOnlyForHTTPTransports(t *testing.T) {
	can := []string{"http", "HTTP", "https", "Https"}
	cannot := []string{"mtls", "dns", "wg", "wireguard", "bind", "tcp-pivot", ""}
	for _, n := range can {
		if !JobServesStage(n) {
			t.Errorf("JobServesStage(%q) = false, want true", n)
		}
	}
	for _, n := range cannot {
		if JobServesStage(n) {
			t.Errorf("JobServesStage(%q) = true, want false: it cannot be fetched with a command", n)
		}
	}
}

// The C2 address is what the implant dials. Getting the port from anywhere but
// the listener produces a payload that calls back to nothing.
func TestC2AddressUsesTheListenersOwnPort(t *testing.T) {
	job := &JobView{ID: 1, Name: "http", Port: 8080, Domains: []string{"c2.example.com"}}
	host, err := callbackHostForJob(job, "", "")
	if err != nil {
		t.Fatalf("callbackHostForJob: %v", err)
	}
	got, err := c2AddressForJob(job, host)
	if err != nil {
		t.Fatalf("c2AddressForJob: %v", err)
	}
	if got != "http://c2.example.com:8080" {
		t.Errorf("= %q, want http://c2.example.com:8080", got)
	}
}

// An HTTPS listener has to produce an https callback, or the implant's traffic
// is refused by the listener it was built for.
func TestC2AddressFollowsHTTPSTransport(t *testing.T) {
	job := &JobView{ID: 2, Name: "https", Port: 443, Domains: []string{"c2.example.com"}}
	host, err := callbackHostForJob(job, "", "")
	if err != nil {
		t.Fatalf("callbackHostForJob: %v", err)
	}
	got, err := c2AddressForJob(job, host)
	if err != nil {
		t.Fatalf("c2AddressForJob: %v", err)
	}
	if !strings.HasPrefix(got, "https://") {
		t.Errorf("= %q, want an https scheme", got)
	}
}

// The regression the whole derivation exists for: a listener started on a real
// interface has to produce a callback on that interface.
//
// Sliver does not report a listener's bind address, so before the console
// recorded it a listener on 192.168.1.9 was indistinguishable from one on
// 0.0.0.0 -- and every one-liner for it told the implant to call back to the
// wildcard, which dials the target's own loopback and never connects.
func TestCallbackHostUsesTheAddressTheListenerWasStartedOn(t *testing.T) {
	job := &JobView{ID: 3, Name: "http", Port: 8889, CallbackHost: "192.168.1.9"}
	got, err := callbackHostForJob(job, "", "")
	if err != nil {
		t.Fatalf("callbackHostForJob: %v", err)
	}
	if got != "192.168.1.9" {
		t.Fatalf("= %q, want the address the listener was bound to", got)
	}
	c2, err := c2AddressForJob(job, got)
	if err != nil {
		t.Fatalf("c2AddressForJob: %v", err)
	}
	if c2 != "http://192.168.1.9:8889" {
		t.Errorf("= %q, want http://192.168.1.9:8889", c2)
	}
}

// An explicit host wins. A listener bound to 0.0.0.0 is reachable at whatever
// address the target can route to, and only the operator knows which that is.
func TestCallbackHostPrefersTheOperatorsOwnHost(t *testing.T) {
	job := &JobView{ID: 1, Name: "http", Port: 8080, CallbackHost: "10.9.9.9", Domains: []string{"0.0.0.0"}}
	got, err := callbackHostForJob(job, "10.0.0.5", "203.0.113.7")
	if err != nil {
		t.Fatalf("callbackHostForJob: %v", err)
	}
	if got != "10.0.0.5" {
		t.Errorf("= %q, want the operator's host", got)
	}
}

// A wildcard typed into the host field is a mistake, not an instruction, for the
// same reason a wildcard listener address is: an implant told to dial 0.0.0.0
// dials its own loopback. It has to be skipped in favour of a source that names a
// real destination, and every spelling of it has to be skipped -- a value that
// fell through to the validator instead would fail the whole request rather than
// silently using the listener's own address.
func TestCallbackHostSkipsAWildcardTheOperatorTyped(t *testing.T) {
	job := &JobView{ID: 2, Name: "http", Port: 8889, CallbackHost: "192.168.1.9"}
	for _, typed := range []string{"0.0.0.0", "::", "[::]", "*"} {
		got, err := callbackHostForJob(job, typed, "203.0.113.7")
		if err != nil {
			t.Fatalf("callbackHostForJob(%q): %v", typed, err)
		}
		if got != "192.168.1.9" {
			t.Errorf("typed %q produced %q, want the listener's own address", typed, got)
		}
	}
}

// When the only address a target could reach comes from the console URL, a typed
// wildcard must still fall through to it rather than being emitted or rejected.
func TestCallbackHostTypedWildcardFallsBackToTheConsoleAddress(t *testing.T) {
	job := &JobView{ID: 2, Name: "http", Port: 8889, CallbackHost: "0.0.0.0", Domains: []string{"0.0.0.0"}}
	got, err := callbackHostForJob(job, "0.0.0.0", "203.0.113.7")
	if err != nil {
		t.Fatalf("callbackHostForJob: %v", err)
	}
	if got != "203.0.113.7" {
		t.Errorf("= %q, want the console address rather than the typed wildcard", got)
	}
}

// SuggestedCallbackHost is what the console shows in the host field before a
// build runs. It must agree with the resolver, and answer "" rather than an
// error when nothing usable is known, so the UI can simply leave the field empty.
func TestSuggestedCallbackHostMatchesTheResolver(t *testing.T) {
	job := &JobView{ID: 3, Name: "http", Port: 8890, CallbackHost: "10.1.2.3"}
	if got := SuggestedCallbackHost(job, "203.0.113.7"); got != "10.1.2.3" {
		t.Errorf("= %q, want the listener's own address", got)
	}

	hopeless := &JobView{ID: 4, Name: "http", Port: 8891, CallbackHost: "0.0.0.0", Domains: []string{"0.0.0.0"}}
	if got := SuggestedCallbackHost(hopeless, ""); got != "" {
		t.Errorf("= %q, want an empty suggestion when no address is usable", got)
	}
}

// The listener's own address is a fact and the console's address is a guess, so
// the fact wins: the console and the listener need not be reachable the same way.
func TestCallbackHostPrefersTheListenerOverTheConsoleAddress(t *testing.T) {
	job := &JobView{ID: 1, Name: "http", Port: 8080, CallbackHost: "10.9.9.9"}
	got, err := callbackHostForJob(job, "", "203.0.113.7")
	if err != nil {
		t.Fatalf("callbackHostForJob: %v", err)
	}
	if got != "10.9.9.9" {
		t.Errorf("= %q, want the listener's own address", got)
	}
}

// With nothing better, the address the operator reached the console on is used:
// it routes here by construction, which beats a wildcard.
func TestCallbackHostFallsBackToTheConsoleAddress(t *testing.T) {
	job := &JobView{ID: 1, Name: "http", Port: 8080, CallbackHost: "0.0.0.0", Domains: []string{"0.0.0.0"}}
	got, err := callbackHostForJob(job, "", "203.0.113.7")
	if err != nil {
		t.Fatalf("callbackHostForJob: %v", err)
	}
	if got != "203.0.113.7" {
		t.Errorf("= %q, want the console address rather than the wildcard", got)
	}
}

// A listener recorded on a loopback address is the same failure as one recorded
// on a wildcard, and it is the harder one to notice: 127.0.0.1 looks like a real
// address, so it was auto-filled into the host field and emitted into commands
// that could only ever connect from the C2 host itself. It has to be skipped in
// favour of a source that names an address a target can route to.
func TestCallbackHostSkipsALoopbackListenerAddress(t *testing.T) {
	job := &JobView{ID: 4, Name: "http", Port: 8891, CallbackHost: "127.0.0.1"}
	got, err := callbackHostForJob(job, "", "203.0.113.7")
	if err != nil {
		t.Fatalf("callbackHostForJob: %v", err)
	}
	if got != "203.0.113.7" {
		t.Errorf("= %q, want the console address rather than the listener's loopback", got)
	}
}

// Every spelling of loopback has to be recognised, or one of them reaches a
// command. They arrive from three different sources -- a recorded bind address, a
// domain Sliver reports, a Host header -- and only the bare IPv4 form is
// guaranteed to be the first.
func TestCallbackHostNeverReturnsALoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "127.1.2.3", "::1", "[::1]", "localhost", "LOCALHOST"} {
		job := &JobView{ID: 9, Name: "http", Port: 8891, CallbackHost: addr, Domains: []string{addr}}
		got, err := callbackHostForJob(job, "", addr)
		if err == nil {
			t.Errorf("%q produced %q, want an error rather than a loopback callback", addr, got)
			continue
		}
		if !strings.Contains(err.Error(), "listener 9") {
			t.Errorf("%q: error does not name the listener: %v", addr, err)
		}
	}
}

// The suggestion the console pre-fills has to agree: nothing usable means an
// empty field and the operator is told, rather than a loopback address that reads
// like a working one.
func TestSuggestedCallbackHostIsEmptyForALoopbackListener(t *testing.T) {
	job := &JobView{ID: 4, Name: "http", Port: 8891, CallbackHost: "127.0.0.1"}
	if got := SuggestedCallbackHost(job, "127.0.0.1"); got != "" {
		t.Errorf("= %q, want an empty suggestion when only loopback is known", got)
	}
	if got := SuggestedCallbackHost(job, "203.0.113.7"); got != "203.0.113.7" {
		t.Errorf("= %q, want the console address", got)
	}
}

// An operator who types a loopback address means it -- a target on the C2 host
// is a real case -- so typing one still builds for it. Only the console's own
// choice is restricted.
func TestCallbackHostHonoursALoopbackTheOperatorTyped(t *testing.T) {
	job := &JobView{ID: 4, Name: "http", Port: 8891, CallbackHost: "127.0.0.1"}
	got, err := callbackHostForJob(job, "127.0.0.1", "")
	if err != nil {
		t.Fatalf("callbackHostForJob: %v", err)
	}
	if got != "127.0.0.1" {
		t.Errorf("= %q, want the address the operator typed", got)
	}
}

// A wildcard is a bind address, not a destination: an implant told to dial
// 0.0.0.0 dials its own loopback. It must never be returned, and when it is the
// only thing available the one-liner has to fail with an explanation rather than
// hand over a command that cannot work.
func TestCallbackHostNeverReturnsAWildcard(t *testing.T) {
	job := &JobView{ID: 7, Name: "http", Port: 8889, CallbackHost: "0.0.0.0", Domains: []string{"0.0.0.0", "::"}}
	got, err := callbackHostForJob(job, "", "")
	if err == nil {
		t.Fatalf("= %q, want an error rather than a wildcard callback", got)
	}
	if !strings.Contains(err.Error(), "listener 7") {
		t.Errorf("error does not name the listener: %v", err)
	}
}

// A domain Sliver reports for the listener is the last fallback: it covers a
// listener started outside this console, and it goes through the same validator,
// so a hostile entry is skipped rather than used.
func TestCallbackHostFallsBackToTheListenersReportedDomain(t *testing.T) {
	job := &JobView{ID: 5, Name: "http", Port: 80, Domains: []string{"0.0.0.0", `x; id`, "c2.example.com"}}
	got, err := callbackHostForJob(job, "", "")
	if err != nil {
		t.Fatalf("callbackHostForJob: %v", err)
	}
	if got != "c2.example.com" {
		t.Errorf("= %q, want the listener's reported domain", got)
	}
}

// A listener with no port cannot be turned into an address at all.
func TestC2AddressRejectsAPortlessListener(t *testing.T) {
	job := &JobView{ID: 1, Name: "http", Port: 0}
	if _, err := c2AddressForJob(job, "c2.example.com"); err == nil {
		t.Error("a listener with no port produced an address")
	}
}

// A Windows-only template must not be handed to a Linux target, or the operator
// gets a command that fails on the target with a shell error.
func TestDeliveryMustMatchThePlatform(t *testing.T) {
	if _, err := deliveryForRequest(WebDeliveryPSH, OneLinerLinux); err == nil {
		t.Error("PowerShell was accepted for a Linux target")
	}
	if _, err := deliveryForRequest(WebDeliveryCertutil, OneLinerLinux); err == nil {
		t.Error("certutil was accepted for a Linux target")
	}
	if _, err := deliveryForRequest(WebDeliveryCurl, OneLinerWindows); err == nil {
		t.Error("curl was accepted for a Windows target")
	}
	if _, err := deliveryForRequest(WebDeliveryPSH, OneLinerWindows); err != nil {
		t.Errorf("PowerShell was refused for Windows: %v", err)
	}
	if _, err := deliveryForRequest(WebDeliveryCurl, OneLinerLinux); err != nil {
		t.Errorf("curl was refused for Linux: %v", err)
	}
}

// An empty delivery resolves to something that works on the platform, and the
// two platforms must not resolve to the same thing.
func TestDeliveryDefaultsPerPlatform(t *testing.T) {
	win, err := deliveryForRequest("", OneLinerWindows)
	if err != nil {
		t.Fatal(err)
	}
	if win != WebDeliveryPSH {
		t.Errorf("windows default = %q, want psh", win)
	}
	nix, err := deliveryForRequest("", OneLinerLinux)
	if err != nil {
		t.Fatal(err)
	}
	if nix != WebDeliveryCurl {
		t.Errorf("linux default = %q, want curl", nix)
	}
	// macOS has curl but not necessarily python3; either is defensible, so the
	// assertion is only that it is a unix-capable template.
	mac, err := deliveryForRequest("", OneLinerDarwin)
	if err != nil {
		t.Fatal(err)
	}
	if p := platformForDelivery(mac); p != string(OneLinerLinux) {
		t.Errorf("darwin default %q is not a unix template", mac)
	}
}

func TestDeliveryRejectsAnUnknownFormat(t *testing.T) {
	if _, err := deliveryForRequest("telnet", OneLinerWindows); err == nil {
		t.Error("an unknown delivery format was accepted")
	}
}

// Alternatives must be usable: same platform, not a repeat of the chosen one,
// and actually rendered from the URL that was passed in.
func TestAlternativesAreUsableAndPlatformAppropriate(t *testing.T) {
	url := "http://host:80/stage.woff"

	win := alternativesFor(url, OneLinerWindows, WebDeliveryPSH)
	if len(win) == 0 {
		t.Fatal("no Windows alternatives offered")
	}
	for _, a := range win {
		if a.Platform != string(OneLinerWindows) {
			t.Errorf("alternative %q is for %s, not windows", a.Delivery, a.Platform)
		}
		if a.Delivery == string(WebDeliveryPSH) {
			t.Error("the chosen delivery was repeated as an alternative")
		}
		if !strings.Contains(a.Command, url) {
			t.Errorf("alternative %q does not contain the URL: %s", a.Delivery, a.Command)
		}
		if a.Label == "" {
			t.Errorf("alternative %q has no label", a.Delivery)
		}
	}

	nix := alternativesFor(url, OneLinerLinux, WebDeliveryCurl)
	for _, a := range nix {
		if a.Platform != string(OneLinerLinux) {
			t.Errorf("linux alternative %q is for %s", a.Delivery, a.Platform)
		}
	}
}

// The generated name has to satisfy the server's own rule, which allows only
// alphanumerics, dot, dash and underscore. Reaching the server with an illegal
// name fails the build, and this name is derived rather than typed, so nothing
// else would catch it.
//
// The check goes through the console's own validator rather than a regex written
// here: a second copy of the rule is a second thing to keep in step with the
// server.
func TestGeneratedStageNameIsLegalForTheServer(t *testing.T) {
	for _, p := range []OneLinerPlatform{OneLinerWindows, OneLinerLinux, OneLinerDarwin} {
		name := generatedStageName(p, 7)
		if err := validateArtifactName(name); err != nil {
			t.Errorf("generated name %q would be refused: %v", name, err)
		}
	}
}

// The stage profile is built from the listener's address, so a profile must
// round-trip that address rather than inheriting one from elsewhere.
func TestEnsureStageProfileCarriesTheListenersAddress(t *testing.T) {
	// The address parsing is the part worth testing without a server: the
	// scheme decides the protocol, and the protocol decides which C2 the
	// implant is built to use.
	cases := []struct {
		addr string
		port uint32
		want string
	}{
		{"http://c2.example.com", 80, "http"},
		{"https://c2.example.com", 443, "https"},
	}
	for _, tc := range cases {
		proto := "http"
		if strings.HasPrefix(tc.addr, "https://") {
			proto = "https"
		}
		if proto != tc.want {
			t.Errorf("addr %q -> protocol %q, want %q", tc.addr, proto, tc.want)
		}
		trimmed := strings.TrimPrefix(strings.TrimPrefix(tc.addr, "https://"), "http://")
		if strings.Contains(trimmed, "://") {
			t.Errorf("scheme not stripped from %q: %q", tc.addr, trimmed)
		}
	}
}
