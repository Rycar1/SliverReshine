package sliver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
)

// A session list with one session on the given platform. The console resolves
// the platform from this list, which is what the OS gate reads.
func sessionOn(id, osName string) *rpcStub {
	return &rpcStub{getSessions: func() (*clientpb.Sessions, error) {
		return &clientpb.Sessions{Sessions: []*clientpb.Session{{ID: id, OS: osName}}}, nil
	}}
}

// A beacon is not in the session table, so its platform has to come from the
// beacon list. Without that lookup a Windows-only RPC aimed at a Linux beacon
// skipped the gate and came back as the implant's own "unknown message type".
func beaconOn(id, osName string) *rpcStub {
	return &rpcStub{
		getSessions: func() (*clientpb.Sessions, error) { return &clientpb.Sessions{}, nil },
		getBeacons: func() (*clientpb.Beacons, error) {
			return &clientpb.Beacons{Beacons: []*clientpb.Beacon{{ID: id, OS: osName}}}, nil
		},
	}
}

// The defect this gate exists for: a Windows-only RPC sent to a Linux implant
// came back as the implant's own "unknown message type", which names neither
// the feature nor the platform. The console already knows the platform, so the
// refusal has to say so.
func TestRequireWindowsRefusesLinuxWithThePlatformNamed(t *testing.T) {
	c := &Client{RPC: sessionOn("s-1", "linux")}
	err := c.requireWindows("s-1", "Windows service management")
	if err == nil {
		t.Fatal("a Windows-only feature was allowed on a linux session")
	}
	for _, want := range []string{"Windows service management", "not supported", "linux"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not contain %q", err.Error(), want)
		}
	}
	if strings.Contains(strings.ToLower(err.Error()), "unknown message type") {
		t.Errorf("the refusal still leaks the implant's own error: %q", err.Error())
	}
}

func TestRequireWindowsAllowsWindows(t *testing.T) {
	for _, osName := range []string{"windows", "Windows", "windows/amd64"} {
		c := &Client{RPC: sessionOn("s-1", osName)}
		if err := c.requireWindows("s-1", "Windows service management"); err != nil {
			t.Errorf("%s was refused: %v", osName, err)
		}
	}
}

// macOS has extensions but not services, and the two gates have to differ or
// one of the platforms is wrong.
func TestRequireWindowsOrDarwinCoversBothPlatforms(t *testing.T) {
	for _, osName := range []string{"windows", "darwin", "Darwin/arm64"} {
		c := &Client{RPC: sessionOn("s-1", osName)}
		if err := c.requireWindowsOrDarwin("s-1", "implant extensions"); err != nil {
			t.Errorf("%s was refused extensions: %v", osName, err)
		}
	}
	c := &Client{RPC: sessionOn("s-1", "linux")}
	if err := c.requireWindowsOrDarwin("s-1", "implant extensions"); err == nil {
		t.Error("linux was allowed to use extensions, which its implant does not implement")
	}
}

// An unresolved platform must not become a refusal: that would break Windows
// sessions the console merely failed to enumerate.
func TestRequireWindowsPassesThroughWhenThePlatformIsUnknown(t *testing.T) {
	// A session list that does not contain the ID under test.
	c := &Client{RPC: sessionOn("other", "linux")}
	if err := c.requireWindows("s-1", "Windows service management"); err != nil {
		t.Errorf("an unknown session was refused: %v", err)
	}
	// An empty OS field is equally unknown.
	c = &Client{RPC: sessionOn("s-1", "")}
	if err := c.requireWindows("s-1", "Windows service management"); err != nil {
		t.Errorf("an empty platform was refused: %v", err)
	}
}

// The gate has to run before the RPC. The stub's embedded interface is nil, so
// a call that reached the transport would panic -- which is exactly the
// assertion: on a linux session the console must not send the message at all.
func TestGatedCallRefusesBeforeReachingTheTransport(t *testing.T) {
	c := &Client{RPC: sessionOn("s-1", "linux")}
	if err := c.StartService("s-1", "svc", "", "C:\\x.exe", "", ""); err == nil {
		t.Fatal("StartService was allowed on a linux session")
	} else if !strings.Contains(err.Error(), "linux") {
		t.Errorf("the refusal does not name the platform: %v", err)
	}
	if _, err := c.RegistryRead("s-1", "HKLM", "Software", "x"); err == nil {
		t.Fatal("RegistryRead was allowed on a linux session")
	}
	if err := c.GetSystem("s-1", ""); err == nil {
		t.Fatal("GetSystem was allowed on a linux session")
	}
	// The same gate on the endpoints that reach an unregistered message type.
	if _, err := c.ServiceDetail("s-1", "spooler", ""); err == nil {
		t.Fatal("ServiceDetail was allowed on a linux session")
	}
	if _, _, err := c.RegistryReadHive("s-1", "HKLM", "SAM"); err == nil {
		t.Fatal("RegistryReadHive was allowed on a linux session")
	}
	if err := c.Backdoor("s-1", "C:\\x.exe", "profile"); err == nil {
		t.Fatal("Backdoor was allowed on a linux session")
	}
	if err := c.HijackDLL("s-1", "C:\\ref.dll", "C:\\t", nil, nil, "profile"); err == nil {
		t.Fatal("HijackDLL was allowed on a linux session")
	}
	if _, err := c.PsExec("s-1", "host", "profile", "", "", ""); err == nil {
		t.Fatal("PsExec was allowed on a linux session")
	}
}

// A beacon has no session table entry, so the gate has to consult the beacon
// list; without it a Windows-only RPC aimed at a Linux beacon slipped through.
func TestRequireWindowsRefusesALinuxBeacon(t *testing.T) {
	c := &Client{RPC: beaconOn("b-1", "linux")}
	err := c.requireWindows("b-1", "token inspection")
	if err == nil {
		t.Fatal("a Windows-only feature was allowed on a linux beacon")
	}
	for _, want := range []string{"token inspection", "not supported", "linux"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not contain %q", err.Error(), want)
		}
	}
}

func TestRequireWindowsAllowsAWindowsBeacon(t *testing.T) {
	for _, osName := range []string{"windows", "Windows/amd64"} {
		c := &Client{RPC: beaconOn("b-1", osName)}
		if err := c.requireWindows("b-1", "token inspection"); err != nil {
			t.Errorf("%s beacon was refused: %v", osName, err)
		}
	}
}

// The beacon gate must also run before the RPC: the stub's embedded client is
// nil, so a call that reached the transport would panic.
func TestBeaconGatedCallRefusesBeforeReachingTheTransport(t *testing.T) {
	c := &Client{RPC: beaconOn("b-1", "linux")}
	if _, err := c.BeaconIntegrity("b-1", time.Second); err == nil {
		t.Fatal("BeaconIntegrity was allowed on a linux beacon")
	} else if !strings.Contains(err.Error(), "linux") {
		t.Errorf("the refusal does not name the platform: %v", err)
	}
}

// An alias whose manifest selects ExecuteAssembly or SpawnDll rides the same
// Windows-only message types as those commands, so it has to be refused on the
// same platforms -- the manifest choice must not smuggle the RPC past the gate.
func TestExecuteAliasGatesTheWindowsOnlyManifestKinds(t *testing.T) {
	for name, manifest := range map[string]*AliasManifest{
		"assembly":   {IsAssembly: true},
		"reflective": {IsReflective: true},
	} {
		c := &Client{RPC: sessionOn("s-1", "linux")}
		if _, err := c.executeAlias(context.Background(), "s-1", manifest, nil, "", "", "", "", "", false); err == nil {
			t.Errorf("%s alias was allowed on a linux session", name)
		} else if !strings.Contains(err.Error(), "linux") {
			t.Errorf("%s alias refusal does not name the platform: %v", name, err)
		}
	}
}
