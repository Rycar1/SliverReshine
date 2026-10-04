package sliver

import (
	"strings"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
)

// A session list with one session on the given platform. The console resolves
// the platform from this list, which is what the OS gate reads.
func sessionOn(id, osName string) *rpcStub {
	return &rpcStub{getSessions: func() (*clientpb.Sessions, error) {
		return &clientpb.Sessions{Sessions: []*clientpb.Session{{ID: id, OS: osName}}}, nil
	}}
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
}
