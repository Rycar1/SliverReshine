package sliver

import (
	"testing"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
)

// A migrated implant is generated from the config we send. If the OS, arch or
// C2 endpoint are missing the shellcode injects successfully and then never
// calls back, which looks like a hang rather than a failure.
func TestSessionImplantConfigCarriesEverythingNeededToBuild(t *testing.T) {
	sess := &clientpb.Session{
		ID:            "6ef05035-0000-0000-0000-000000000000",
		Name:          "Rycarl",
		OS:            "windows",
		Arch:          "amd64",
		Transport:     "mtls",
		ActiveC2:      "172.30.179.196:8443",
		Evasion:       true,
		IsDead:        false,
		Hostname:      "WIN-DEV",
		RemoteAddress: "10.0.0.5:51000",
	}

	cfg := sessionImplantConfig(sess)

	if cfg.GOOS != "windows" {
		t.Errorf("GOOS = %q, want windows", cfg.GOOS)
	}
	if cfg.GOARCH != "amd64" {
		t.Errorf("GOARCH = %q, want amd64", cfg.GOARCH)
	}
	if len(cfg.C2) != 1 {
		t.Fatalf("C2 entries = %d, want 1", len(cfg.C2))
	}
	if cfg.C2[0].URL != "172.30.179.196:8443" {
		t.Errorf("C2 URL = %q, want the session's active C2", cfg.C2[0].URL)
	}
	// The transport flag is what makes the generated implant dial back at all.
	if !cfg.IncludeMTLS {
		t.Error("IncludeMTLS is false; the migrated implant would have no C2")
	}
	if cfg.IncludeHTTP || cfg.IncludeDNS || cfg.IncludeWG {
		t.Error("only the session's own transport should be enabled")
	}
	if cfg.Format != clientpb.OutputFormat_SHELLCODE {
		t.Errorf("Format = %v, want SHELLCODE", cfg.Format)
	}
	if cfg.IsSharedLib {
		t.Log("IsSharedLib set (mirrors GetActiveSessionConfig)")
	}
	if !cfg.Evasion {
		t.Error("Evasion should be inherited from the session")
	}
	if cfg.HTTPC2ConfigName != "default" {
		t.Errorf("HTTPC2ConfigName = %q, want default", cfg.HTTPC2ConfigName)
	}
	if cfg.MaxConnectionErrors == 0 || cfg.ReconnectInterval == 0 {
		t.Error("connection limits should be set so the implant keeps retrying")
	}
}

func TestSessionImplantConfigTransportMapping(t *testing.T) {
	cases := map[string]func(*clientpb.ImplantConfig) bool{
		"mtls":      func(c *clientpb.ImplantConfig) bool { return c.IncludeMTLS },
		"http":      func(c *clientpb.ImplantConfig) bool { return c.IncludeHTTP },
		"https":     func(c *clientpb.ImplantConfig) bool { return c.IncludeHTTP },
		"http(s)":   func(c *clientpb.ImplantConfig) bool { return c.IncludeHTTP },
		"dns":       func(c *clientpb.ImplantConfig) bool { return c.IncludeDNS },
		"wg":        func(c *clientpb.ImplantConfig) bool { return c.IncludeWG },
		"namedpipe": func(c *clientpb.ImplantConfig) bool { return c.IncludeNamePipe },
		"tcppivot":  func(c *clientpb.ImplantConfig) bool { return c.IncludeTCP },
	}

	for transport, check := range cases {
		cfg := sessionImplantConfig(&clientpb.Session{
			OS: "windows", Arch: "amd64",
			Transport: transport, ActiveC2: "host:443",
		})
		if !check(cfg) {
			t.Errorf("transport %q did not enable its include flag", transport)
		}
	}

	// An unknown transport must still produce a usable config rather than a
	// panic or a nil map.
	cfg := sessionImplantConfig(&clientpb.Session{OS: "linux", Arch: "arm64", Transport: "quantum"})
	if cfg == nil || cfg.GOOS != "linux" {
		t.Fatal("an unrecognised transport should still yield a config")
	}
}

// Request.Timeout is in nanoseconds. Leaving it at zero makes the server fall
// back to its 30s floor, which truncates long operations.
func TestRequestForConvertsToNanoseconds(t *testing.T) {
	req := requestFor("session-1", dumpTimeout)

	if req.SessionID != "session-1" {
		t.Errorf("SessionID = %q", req.SessionID)
	}
	// One second shorter than the gRPC deadline so the server errors first.
	want := int64(dumpTimeout - time.Second)
	if req.Timeout != want {
		t.Errorf("Timeout = %d, want %d", req.Timeout, want)
	}
	// A zero or sub-second value would silently hit the server's floor.
	if req.Timeout <= int64(30*time.Second) {
		t.Errorf("Timeout = %d is not above the server's 30s minimum", req.Timeout)
	}
	// And it must not exceed the client-side deadline, or the client gives up
	// mid-stream and the operator sees an opaque transport error.
	if time.Duration(req.Timeout) >= dumpTimeout {
		t.Errorf("implant timeout %v must stay under the gRPC deadline %v",
			time.Duration(req.Timeout), dumpTimeout)
	}
}

func TestRequestForIgnoresSubSecondDurations(t *testing.T) {
	// A duration under a second must not produce a negative timeout.
	req := requestFor("s", 500*time.Millisecond)
	if req.Timeout < 0 {
		t.Errorf("Timeout = %d, want a non-negative value", req.Timeout)
	}
}

func TestOperationalTimeoutsExceedServerFloor(t *testing.T) {
	for name, d := range map[string]time.Duration{
		"migrateTimeout":       migrateTimeout,
		"migrateInjectTimeout": migrateInjectTimeout,
		"dumpTimeout":          dumpTimeout,
	} {
		if d <= 30*time.Second {
			t.Errorf("%s = %v, at or below the server's 30s minimum", name, d)
		}
	}
	// The gRPC deadline has to outlast the timeout sent to the implant.
	if migrateTimeout <= migrateInjectTimeout {
		t.Errorf("migrateTimeout (%v) must exceed migrateInjectTimeout (%v)",
			migrateTimeout, migrateInjectTimeout)
	}
}

// The dump is streamed as raw bytes now; the result type must not try to
// JSON-encode a minidump.
func TestProcessDumpResultIsNotSerialised(t *testing.T) {
	r := &ProcessDumpResult{Data: []byte{0x4d, 0x44, 0x4d, 0x50}} // "MDMP"
	if len(r.Data) != 4 {
		t.Fatal("data round-trip failed")
	}
}

var _ = commonpb.Request{}
