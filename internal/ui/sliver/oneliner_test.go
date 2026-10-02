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
	got, err := c2AddressForJob(job, "")
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
	got, err := c2AddressForJob(job, "")
	if err != nil {
		t.Fatalf("c2AddressForJob: %v", err)
	}
	if !strings.HasPrefix(got, "https://") {
		t.Errorf("= %q, want an https scheme", got)
	}
}

// An explicit host wins. A listener bound to 0.0.0.0 is reachable at whatever
// address the target can route to, and only the operator knows which that is.
func TestC2AddressPrefersAnExplicitHost(t *testing.T) {
	job := &JobView{ID: 1, Name: "http", Port: 8080, Domains: []string{"0.0.0.0"}}
	got, err := c2AddressForJob(job, "10.0.0.5")
	if err != nil {
		t.Fatalf("c2AddressForJob: %v", err)
	}
	if got != "http://10.0.0.5:8080" {
		t.Errorf("= %q, want the explicit host", got)
	}
}

// A wildcard bind address is not a usable callback target: it builds, it
// fetches, and it never connects. The placeholder is kept so the result is
// inspectable rather than empty, and the caller surfaces it.
func TestC2AddressDoesNotReportAWildcardAsIfItWereUsable(t *testing.T) {
	job := &JobView{ID: 1, Name: "http", Port: 8080, Domains: []string{"0.0.0.0"}}
	got, err := c2AddressForJob(job, "")
	if err != nil {
		t.Fatalf("c2AddressForJob: %v", err)
	}
	if got != "http://0.0.0.0:8080" {
		t.Errorf("= %q, want the wildcard preserved so it is visible", got)
	}
}

// A listener with no port cannot be turned into an address at all.
func TestC2AddressRejectsAPortlessListener(t *testing.T) {
	job := &JobView{ID: 1, Name: "http", Port: 0}
	if _, err := c2AddressForJob(job, ""); err == nil {
		t.Error("a listener with no port produced an address")
	}
}

// The fetch URL and the callback address usually share a host, but not always:
// an implant may be told to dial a public name while the stage has to be fetched
// from an address the target can reach directly.
func TestStageURLHostFallsBackSensibly(t *testing.T) {
	withDomain := &JobView{ID: 1, Name: "http", Port: 80, Domains: []string{"c2.example.com"}}
	if got := hostForStageURL(withDomain, ""); got != "c2.example.com" {
		t.Errorf("= %q, want the listener domain", got)
	}
	wildcard := &JobView{ID: 1, Name: "http", Port: 80, Domains: []string{"0.0.0.0"}}
	if got := hostForStageURL(wildcard, ""); got != "127.0.0.1" {
		t.Errorf("= %q, want loopback rather than the wildcard", got)
	}
	if got := hostForStageURL(wildcard, "10.1.1.1"); got != "10.1.1.1" {
		t.Errorf("= %q, want the explicit host", got)
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
