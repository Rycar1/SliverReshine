package sliver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
)

// aclStub answers the two RPCs the Windows ownership path can reach -- the
// session list that names the platform, and the spawn that runs icacls -- plus
// the Chown RPC the portable path uses. Every other method comes from the nil
// embedded interface and panics, so a call this test did not expect fails
// loudly instead of passing on a zero value.
type aclStub struct {
	rpcpb.SliverRPCClient

	sessionOS string
	execWin   func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error)
	execPlain func(*sliverpb.ExecuteReq) (*sliverpb.Execute, error)
	chown     func(*sliverpb.ChownReq) (*sliverpb.Chown, error)

	execWinCalls   int
	execPlainCalls int
	chownCalls     int
}

func (s *aclStub) GetSessions(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Sessions, error) {
	if s.sessionOS == "" {
		return &clientpb.Sessions{}, nil
	}
	return &clientpb.Sessions{Sessions: []*clientpb.Session{{ID: "s-1", OS: s.sessionOS, Arch: "amd64"}}}, nil
}

func (s *aclStub) GetBeacons(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Beacons, error) {
	return &clientpb.Beacons{}, nil
}

func (s *aclStub) ExecuteWindows(_ context.Context, in *sliverpb.ExecuteWindowsReq, _ ...grpc.CallOption) (*sliverpb.Execute, error) {
	s.execWinCalls++
	if s.execWin == nil {
		return nil, errors.New("ExecuteWindows was not expected in this test")
	}
	return s.execWin(in)
}

func (s *aclStub) Execute(_ context.Context, in *sliverpb.ExecuteReq, _ ...grpc.CallOption) (*sliverpb.Execute, error) {
	s.execPlainCalls++
	if s.execPlain == nil {
		return nil, errors.New("Execute was not expected in this test")
	}
	return s.execPlain(in)
}

func (s *aclStub) Chown(_ context.Context, in *sliverpb.ChownReq, _ ...grpc.CallOption) (*sliverpb.Chown, error) {
	s.chownCalls++
	if s.chown == nil {
		return nil, errors.New("Chown was not expected in this test")
	}
	return s.chown(in)
}

// --- argv shapes ---

func TestWindowsSetOwnerArgsNamesTheAccount(t *testing.T) {
	got := windowsSetOwnerArgs(`C:\tmp\a.txt`, "Administrators", false)
	want := []string{`C:\tmp\a.txt`, "/setowner", "Administrators"}
	if len(got) != len(want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %q, want %q", got, want)
		}
	}
}

func TestWindowsSetOwnerArgsAddsRecurseOnlyWhenAsked(t *testing.T) {
	got := windowsSetOwnerArgs("dir", "Administrators", true)
	if len(got) != 4 || got[3] != "/T" {
		t.Fatalf("recursive argv = %q, want a trailing /T", got)
	}
}

func TestWindowsGrantArgsReplacesTheEntry(t *testing.T) {
	got := windowsGrantArgs(`C:\tmp\a.txt`, "BUILTIN\\Users", "RX", false)
	if len(got) != 3 {
		t.Fatalf("argv = %q, want three elements", got)
	}
	if got[1] != "/grant:r" {
		t.Errorf("flag = %q, want /grant:r so the entry is replaced, not accumulated", got[1])
	}
	if got[2] != "BUILTIN\\Users:RX" {
		t.Errorf("principal:perm = %q", got[2])
	}
}

func TestWindowsGrantArgsAddsRecurseOnlyWhenAsked(t *testing.T) {
	got := windowsGrantArgs("dir", "Users", "M", true)
	if len(got) != 4 || got[3] != "/T" {
		t.Fatalf("recursive argv = %q, want a trailing /T", got)
	}
}

// --- Chown picks the platform's implementation ---

// A Windows file has a security descriptor, not a mode word, and the Windows
// implant has no Chown handler: the request would come back as an unknown
// message type. Ownership there is an icacls call, so the wrapper has to make
// one instead of sending the RPC.
func TestChownOnWindowsRunsIcaclsSetOwner(t *testing.T) {
	var got *sliverpb.ExecuteWindowsReq
	stub := &aclStub{
		sessionOS: platformWindows,
		execWin: func(in *sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
			got = in
			return &sliverpb.Execute{Response: &commonpb.Response{}}, nil
		},
	}
	c := &Client{RPC: stub}

	if err := c.Chown("s-1", `C:\tmp\a.txt`, "Administrators", "0", true); err != nil {
		t.Fatalf("Chown: %v", err)
	}
	if stub.chownCalls != 0 {
		t.Error("the Chown RPC was sent to a Windows target, which has no handler for it")
	}
	if got == nil {
		t.Fatal("icacls was never run")
	}
	if got.GetPath() != icaclsPath {
		t.Errorf("spawned %q, want the absolute icacls path", got.GetPath())
	}
	if args := got.GetArgs(); len(args) != 4 || args[0] != `C:\tmp\a.txt` || args[1] != "/setowner" ||
		args[2] != "Administrators" || args[3] != "/T" {
		t.Errorf("icacls argv = %q", args)
	}
	if !got.GetHideWindow() {
		t.Error("HideWindow = false: icacls would flash a console window on the target")
	}
}

// The gid half of a unix chown has no counterpart in a Windows security
// descriptor, so it is dropped rather than turned into a second account.
func TestChownOnWindowsIgnoresTheGID(t *testing.T) {
	var got *sliverpb.ExecuteWindowsReq
	stub := &aclStub{
		sessionOS: platformWindows,
		execWin: func(in *sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
			got = in
			return &sliverpb.Execute{Response: &commonpb.Response{}}, nil
		},
	}
	c := &Client{RPC: stub}

	if err := c.Chown("s-1", "a.txt", "Users", "1000", false); err != nil {
		t.Fatalf("Chown: %v", err)
	}
	for _, a := range got.GetArgs() {
		if a == "1000" {
			t.Fatalf("argv = %q: the gid leaked into the icacls call", got.GetArgs())
		}
	}
}

func TestChownOnLinuxUsesTheChownRPC(t *testing.T) {
	var got *sliverpb.ChownReq
	stub := &aclStub{
		sessionOS: "linux",
		chown: func(in *sliverpb.ChownReq) (*sliverpb.Chown, error) {
			got = in
			return &sliverpb.Chown{Response: &commonpb.Response{}}, nil
		},
	}
	c := &Client{RPC: stub}

	if err := c.Chown("s-1", "/tmp/a", "1000", "1000", false); err != nil {
		t.Fatalf("Chown: %v", err)
	}
	if stub.execWinCalls != 0 {
		t.Error("icacls was run against a Linux target")
	}
	if got == nil || got.GetUid() != "1000" || got.GetGid() != "1000" {
		t.Fatalf("Chown request = %+v", got)
	}
}

// An unresolved platform keeps the portable implementation: a wrong "windows"
// would run icacls on a target that may not have it, while a wrong "unix" only
// produces the implant's own error text.
func TestChownOnUnresolvedTargetStaysPortable(t *testing.T) {
	stub := &aclStub{
		chown: func(*sliverpb.ChownReq) (*sliverpb.Chown, error) {
			return &sliverpb.Chown{Response: &commonpb.Response{}}, nil
		},
	}
	c := &Client{RPC: stub}

	if err := c.Chown("s-1", "/tmp/a", "0", "0", false); err != nil {
		t.Fatalf("Chown: %v", err)
	}
	if stub.chownCalls != 1 {
		t.Errorf("Chown RPC calls = %d, want 1", stub.chownCalls)
	}
	if stub.execWinCalls != 0 {
		t.Error("icacls was run for a target whose platform could not be resolved")
	}
}

// --- GrantACL ---

func TestGrantACLRunsIcaclsGrantOnWindows(t *testing.T) {
	var got *sliverpb.ExecuteWindowsReq
	stub := &aclStub{
		sessionOS: platformWindows,
		execWin: func(in *sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
			got = in
			return &sliverpb.Execute{Response: &commonpb.Response{}}, nil
		},
	}
	c := &Client{RPC: stub}

	if err := c.GrantACL("s-1", `C:\tmp\a.txt`, "Users", "RX", false); err != nil {
		t.Fatalf("GrantACL: %v", err)
	}
	if got == nil || got.GetPath() != icaclsPath {
		t.Fatalf("spawn = %+v, want the absolute icacls path", got)
	}
	if args := got.GetArgs(); len(args) != 3 || args[1] != "/grant:r" || args[2] != "Users:RX" {
		t.Errorf("icacls argv = %q", args)
	}
}

func TestGrantACLRefusesANonWindowsTarget(t *testing.T) {
	stub := &aclStub{sessionOS: "linux"}
	c := &Client{RPC: stub}

	err := c.GrantACL("s-1", "/tmp/a", "root", "F", false)
	if err == nil {
		t.Fatal("an ACL grant was accepted for a Linux target")
	}
	if !strings.Contains(err.Error(), "chmod") {
		t.Errorf("error = %q, want it to point at chmod", err.Error())
	}
	if stub.execWinCalls != 0 || stub.execPlainCalls != 0 {
		t.Error("a command ran on a target the operation does not apply to")
	}
}

func TestGrantACLRejectsAnUnknownPermissionLevel(t *testing.T) {
	stub := &aclStub{sessionOS: platformWindows}
	c := &Client{RPC: stub}

	err := c.GrantACL("s-1", `C:\tmp\a.txt`, "Users", "RW", false)
	if err == nil {
		t.Fatal("a permission level icacls does not define was accepted")
	}
	if !strings.Contains(err.Error(), "F, M, RX, R, W") {
		t.Errorf("error = %q, want it to name the accepted levels", err.Error())
	}
	if stub.execWinCalls != 0 {
		t.Error("icacls ran with a permission level that was not validated")
	}
}

func TestGrantACLRejectsAnAccountNameThatCouldSpliceTheArgument(t *testing.T) {
	cases := map[string]string{
		"colon":    "Users:F",
		"pathlike": "/grant:r",
	}
	for name, principal := range cases {
		stub := &aclStub{sessionOS: platformWindows}
		c := &Client{RPC: stub}
		err := c.GrantACL("s-1", `C:\tmp\a.txt`, principal, "F", false)
		if err == nil {
			t.Fatalf("%s: account %q was accepted", name, principal)
		}
		if stub.execWinCalls != 0 {
			t.Errorf("%s: icacls ran with an account name that was not validated", name)
		}
	}
}

// --- runIcacls error surface ---

// icacls reports a refusal on stderr with a non-zero exit code while the RPC
// itself succeeds, because a process really did run. Without the exit check the
// console would toast "updated" for a change that never happened.
func TestRunIcaclsSurfacesStderrOnNonZeroExit(t *testing.T) {
	stub := &aclStub{
		sessionOS: platformWindows,
		execWin: func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
			return &sliverpb.Execute{
				Status:   1,
				Stderr:   []byte("Access is denied.\r\n"),
				Response: &commonpb.Response{},
			}, nil
		},
	}
	c := &Client{RPC: stub}

	err := c.runIcacls("s-1", []string{"a.txt", "/setowner", "Administrators"})
	if err == nil {
		t.Fatal("a failed icacls run was reported as success")
	}
	if !strings.Contains(err.Error(), "exit 1") || !strings.Contains(err.Error(), "Access is denied.") {
		t.Errorf("error = %q, want the exit code and icacls' own text", err.Error())
	}
}

func TestRunIcaclsFallsBackToStdoutThenToNoOutput(t *testing.T) {
	cases := []struct {
		name string
		res  *sliverpb.Execute
		want string
	}{
		{"stdout", &sliverpb.Execute{Status: 2, Stdout: []byte("Invalid parameter"), Response: &commonpb.Response{}}, "Invalid parameter"},
		{"silent", &sliverpb.Execute{Status: 2, Response: &commonpb.Response{}}, "no output"},
	}
	for _, tc := range cases {
		stub := &aclStub{
			sessionOS: platformWindows,
			execWin: func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
				return tc.res, nil
			},
		}
		c := &Client{RPC: stub}
		err := c.runIcacls("s-1", []string{"a.txt"})
		if err == nil {
			t.Fatalf("%s: a non-zero exit was reported as success", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %q, want it to contain %q", tc.name, err.Error(), tc.want)
		}
	}
}

func TestRunIcaclsPassesThroughATransportError(t *testing.T) {
	stub := &aclStub{
		sessionOS: platformWindows,
		execWin: func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
			return nil, errors.New("connection refused")
		},
	}
	c := &Client{RPC: stub}

	err := c.runIcacls("s-1", []string{"a.txt"})
	if err == nil || err.Error() != "connection refused" {
		t.Fatalf("error = %v, want the transport error unchanged", err)
	}
}
