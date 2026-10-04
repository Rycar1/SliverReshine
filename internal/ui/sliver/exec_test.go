package sliver

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
)

// rpcStub implements only the RPCs a test drives.
//
// The embedded interface is nil, so a method a test does not override panics
// instead of quietly returning a zero value: a test that starts exercising a
// new call fails loudly rather than passing for the wrong reason.
type rpcStub struct {
	rpcpb.SliverRPCClient

	execWindows func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error)
	exec        func(*sliverpb.ExecuteReq) (*sliverpb.Execute, error)
	getSessions func() (*clientpb.Sessions, error)
	getJobs     func() (*clientpb.Jobs, error)

	windowsCalls int32
	plainCalls   int32
	sessionCalls int32
}

func (s *rpcStub) ExecuteWindows(_ context.Context, in *sliverpb.ExecuteWindowsReq, _ ...grpc.CallOption) (*sliverpb.Execute, error) {
	atomic.AddInt32(&s.windowsCalls, 1)
	if s.execWindows == nil {
		return nil, errors.New("ExecuteWindows is not expected in this test")
	}
	return s.execWindows(in)
}

func (s *rpcStub) Execute(_ context.Context, in *sliverpb.ExecuteReq, _ ...grpc.CallOption) (*sliverpb.Execute, error) {
	atomic.AddInt32(&s.plainCalls, 1)
	if s.exec == nil {
		return nil, errors.New("Execute is not expected in this test")
	}
	return s.exec(in)
}

func (s *rpcStub) GetSessions(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Sessions, error) {
	atomic.AddInt32(&s.sessionCalls, 1)
	if s.getSessions == nil {
		return nil, errors.New("GetSessions is not expected in this test")
	}
	return s.getSessions()
}

func (s *rpcStub) GetJobs(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Jobs, error) {
	if s.getJobs == nil {
		return nil, errors.New("GetJobs is not expected in this test")
	}
	return s.getJobs()
}

// okExecute is a successful reply carrying stdout.
func okExecute(stdout string) *sliverpb.Execute {
	return &sliverpb.Execute{
		Response: &commonpb.Response{},
		Stdout:   []byte(stdout),
	}
}

// --- Which RPC a spawn uses ---

// A Windows target must be driven through ExecuteWindows. The plain Execute RPC
// carries a request type with no window field at all, so anything sent that way
// starts with a visible console window -- the defect this routing exists to fix.
func TestWindowsSpawnGoesThroughExecuteWindowsWithTheWindowHidden(t *testing.T) {
	var got *sliverpb.ExecuteWindowsReq
	stub := &rpcStub{execWindows: func(in *sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
		got = in
		return okExecute("done"), nil
	}}
	c := &Client{RPC: stub}

	res, err := c.execOn("s-1", platformWindows, "cmd.exe", []string{"/c", "whoami"}, execDefaultTimeout)
	if err != nil {
		t.Fatalf("execOn: %v", err)
	}
	if got == nil {
		t.Fatal("ExecuteWindows was not called; a Windows spawn must not use the plain RPC")
	}
	if !got.HideWindow {
		t.Error("HideWindow = false: the target would show a console window")
	}
	if atomic.LoadInt32(&stub.plainCalls) != 0 {
		t.Error("the plain Execute RPC was used for a Windows session")
	}
	if res.Stdout != "done" {
		t.Errorf("Stdout = %q, want %q", res.Stdout, "done")
	}
}

// Non-Windows targets have no MsgExecuteWindowsReq handler, so the Windows RPC
// would come back as an unknown message type rather than running anything.
func TestNonWindowsSpawnUsesThePlainRPC(t *testing.T) {
	var got *sliverpb.ExecuteReq
	stub := &rpcStub{exec: func(in *sliverpb.ExecuteReq) (*sliverpb.Execute, error) {
		got = in
		return okExecute("ok"), nil
	}}
	c := &Client{RPC: stub}

	if _, err := c.execOn("s-1", platformLinux, "id", nil, execDefaultTimeout); err != nil {
		t.Fatalf("execOn: %v", err)
	}
	if got == nil {
		t.Fatal("the plain Execute RPC was not called for a Linux session")
	}
	if atomic.LoadInt32(&stub.windowsCalls) != 0 {
		t.Error("ExecuteWindows was used for a Linux session")
	}
}

// An unresolvable platform must still take the window-hiding path.
//
// This is the case where the target might be Windows, so falling back to the
// plain RPC is what puts a console window on the desktop. ExecuteWindows is
// harmless on a non-Windows implant -- it answers with an unknown message type
// and nothing runs -- so the cost of trying it first is one round trip.
func TestUnresolvablePlatformStillHidesTheWindow(t *testing.T) {
	stub := &rpcStub{
		getSessions: func() (*clientpb.Sessions, error) {
			return nil, errors.New("server unreachable")
		},
		execWindows: func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
			return okExecute("hidden"), nil
		},
		exec: func(*sliverpb.ExecuteReq) (*sliverpb.Execute, error) {
			return okExecute("plain"), nil
		},
	}
	c := &Client{RPC: stub}

	res, err := c.execOn("s-1", "", "cmd.exe", nil, execDefaultTimeout)
	if err != nil {
		t.Fatalf("execOn: %v", err)
	}
	if res.Stdout != "hidden" {
		t.Errorf("Stdout = %q, want the window-hiding path to be preferred", res.Stdout)
	}
	if atomic.LoadInt32(&stub.windowsCalls) != 1 {
		t.Errorf("ExecuteWindows calls = %d, want 1", stub.windowsCalls)
	}
	if atomic.LoadInt32(&stub.plainCalls) != 0 {
		t.Error("the plain Execute RPC was used before trying the hidden one")
	}
}

// When the platform cannot be resolved AND the implant predates
// ExecuteWindows, the unknown-message-type fallback still has to run the
// command: a working command beats a hidden one.
func TestUnresolvablePlatformFallsBackWhenImplantIsOld(t *testing.T) {
	stub := &rpcStub{
		getSessions: func() (*clientpb.Sessions, error) {
			return nil, errors.New("server unreachable")
		},
		execWindows: func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
			return nil, errors.New("rpc error: code = Unknown desc = unknown message type")
		},
		exec: func(*sliverpb.ExecuteReq) (*sliverpb.Execute, error) {
			return okExecute("fallback"), nil
		},
	}
	c := &Client{RPC: stub}

	res, err := c.execOn("s-1", "", "id", nil, execDefaultTimeout)
	if err != nil {
		t.Fatalf("execOn: %v", err)
	}
	if res.Stdout != "fallback" {
		t.Errorf("Stdout = %q, want the plain-RPC reply", res.Stdout)
	}
}

// An OS string that is neither Windows nor a recognised non-Windows platform
// must not be read as "definitely not Windows" -- that is how a popup returns.
func TestUnexpectedOSStringStillHidesTheWindow(t *testing.T) {
	stub := &rpcStub{
		execWindows: func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
			return okExecute("hidden"), nil
		},
		exec: func(*sliverpb.ExecuteReq) (*sliverpb.Execute, error) {
			return okExecute("plain"), nil
		},
	}
	c := &Client{RPC: stub}

	// "windows/amd64" is not equal to "windows" but is still Windows.
	res, err := c.execOn("s-1", "windows/amd64", "cmd.exe", nil, execDefaultTimeout)
	if err != nil {
		t.Fatalf("execOn: %v", err)
	}
	if res.Stdout != "hidden" {
		t.Errorf("Stdout = %q, want the window-hiding path", res.Stdout)
	}
}

// A platform we can positively identify as non-Windows keeps the plain RPC, so
// a Linux fleet does not pay a wasted round trip on every spawn.
func TestKnownNonWindowsUsesThePlainRPC(t *testing.T) {
	stub := &rpcStub{
		exec: func(*sliverpb.ExecuteReq) (*sliverpb.Execute, error) {
			return okExecute("plain"), nil
		},
	}
	c := &Client{RPC: stub}

	for _, osName := range []string{"linux", "darwin", "freebsd"} {
		if _, err := c.execOn("s-1", osName, "id", nil, execDefaultTimeout); err != nil {
			t.Fatalf("execOn(%s): %v", osName, err)
		}
	}
	if atomic.LoadInt32(&stub.windowsCalls) != 0 {
		t.Errorf("ExecuteWindows calls = %d, want 0 for known non-Windows targets", stub.windowsCalls)
	}
	if atomic.LoadInt32(&stub.plainCalls) != 3 {
		t.Errorf("plain Execute calls = %d, want 3", stub.plainCalls)
	}
}

// An implant built before the ExecuteWindows RPC answers with an unknown
// message type. Retrying through the plain path keeps that target usable.
func TestUnknownMessageTypeFallsBackToThePlainRPC(t *testing.T) {
	stub := &rpcStub{
		execWindows: func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
			return nil, errors.New("rpc error: code = Unknown desc = unknown message type")
		},
		exec: func(*sliverpb.ExecuteReq) (*sliverpb.Execute, error) {
			return okExecute("fallback"), nil
		},
	}
	c := &Client{RPC: stub}

	res, err := c.execOn("s-1", platformWindows, "cmd.exe", nil, execDefaultTimeout)
	if err != nil {
		t.Fatalf("execOn: %v", err)
	}
	if res.Stdout != "fallback" {
		t.Errorf("Stdout = %q, want the plain-RPC reply", res.Stdout)
	}
}

// Only the unknown-message-type failure may be retried. Anything else describes
// the command, and running it a second time would be a duplicate execution.
func TestOtherErrorsAreNotRetried(t *testing.T) {
	stub := &rpcStub{
		execWindows: func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
			return nil, errors.New("rpc error: code = Unavailable desc = connection closed")
		},
		exec: func(*sliverpb.ExecuteReq) (*sliverpb.Execute, error) {
			return okExecute("should not run"), nil
		},
	}
	c := &Client{RPC: stub}

	if _, err := c.execOn("s-1", platformWindows, "cmd.exe", nil, execDefaultTimeout); err == nil {
		t.Fatal("a transport failure was swallowed")
	}
	if atomic.LoadInt32(&stub.plainCalls) != 0 {
		t.Error("a non-fallback error caused a second execution")
	}
}

// An error the implant reported describes the command, not the transport, and
// must surface as an error rather than a zero-valued success.
func TestImplantReportedErrorBecomesAnError(t *testing.T) {
	stub := &rpcStub{execWindows: func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
		return &sliverpb.Execute{
			Response: &commonpb.Response{Err: "The system cannot find the file specified."},
		}, nil
	}}
	c := &Client{RPC: stub}

	if _, err := c.execOn("s-1", platformWindows, "nope.exe", nil, execDefaultTimeout); err == nil {
		t.Fatal("an implant-reported error was returned as success")
	}
}

// --- Platform cache ---

// Resolving the platform must cost one Sessions call, not one per spawn: the
// persistence inventory starts a probe per module.
func TestSessionOSIsResolvedOnce(t *testing.T) {
	stub := &rpcStub{
		getSessions: func() (*clientpb.Sessions, error) {
			return &clientpb.Sessions{Sessions: []*clientpb.Session{
				{ID: "s-1", OS: "Windows"},
			}}, nil
		},
		execWindows: func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
			return okExecute(""), nil
		},
	}
	c := &Client{RPC: stub}

	for i := 0; i < 5; i++ {
		if _, err := c.execOn("s-1", "", "cmd.exe", nil, execDefaultTimeout); err != nil {
			t.Fatalf("execOn #%d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&stub.sessionCalls); got != 1 {
		t.Errorf("GetSessions calls = %d, want 1 (the answer is cached)", got)
	}
	// Sliver reports the platform capitalised; the comparison must fold case.
	if atomic.LoadInt32(&stub.windowsCalls) != 5 {
		t.Errorf("ExecuteWindows calls = %d, want 5", stub.windowsCalls)
	}
}

// --- Persistence inventory ---

// The inventory issues one probe per module. Serially that was one C2 round
// trip after another and the tab sat empty for seconds; the probes are now
// fanned out, which is only correct if the results still come back in catalog
// order and every spawn stays hidden.
func TestPersistenceInventoryProbesConcurrentlyAndKeepsCatalogOrder(t *testing.T) {
	var inFlight, peak int32
	var mu sync.Mutex
	var hiddenFalse int32

	stub := &rpcStub{}
	stub.execWindows = func(in *sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
		if !in.HideWindow {
			atomic.AddInt32(&hiddenFalse, 1)
		}
		n := atomic.AddInt32(&inFlight, 1)
		mu.Lock()
		if n > peak {
			peak = n
		}
		mu.Unlock()
		// Hold the probe open so overlapping calls are observable.
		time.Sleep(15 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return okExecute(""), nil
	}

	c := &Client{RPC: stub}
	list, err := c.PersistenceList("s-1", platformWindows, "")
	if err != nil {
		t.Fatalf("PersistenceList: %v", err)
	}

	// Expected set and order, derived from the catalog the same way the
	// inventory derives it.
	want := make([]string, 0, len(moduleCatalog))
	for _, m := range moduleCatalog {
		if supportsPlatform(m, platformWindows) {
			want = append(want, m.ID)
		}
	}
	if len(list.Items) != len(want) {
		t.Fatalf("items = %d, want %d", len(list.Items), len(want))
	}
	for i, id := range want {
		if list.Items[i].Module != id {
			t.Errorf("item %d = %q, want %q (results must follow catalog order)", i, list.Items[i].Module, id)
		}
	}

	if got := atomic.LoadInt32(&stub.windowsCalls); int(got) != len(want) {
		t.Errorf("probes issued = %d, want %d", got, len(want))
	}
	if peak < 2 {
		t.Errorf("peak concurrent probes = %d; the inventory is still serial", peak)
	}
	if hiddenFalse != 0 {
		t.Errorf("%d probes were issued without HideWindow", hiddenFalse)
	}
	if atomic.LoadInt32(&stub.plainCalls) != 0 {
		t.Error("the inventory used the plain Execute RPC on a Windows target")
	}
}

// Every catalogued module must still be answered for, and a name-scoped row
// with no name supplied must stay unknown rather than reading as "absent".
func TestPersistenceInventoryMarksUnanswerableRowsUnknown(t *testing.T) {
	stub := &rpcStub{}
	stub.execWindows = func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
		return okExecute(""), nil
	}
	c := &Client{RPC: stub}

	list, err := c.PersistenceList("s-1", platformWindows, "")
	if err != nil {
		t.Fatalf("PersistenceList: %v", err)
	}
	for _, item := range list.Items {
		if item.Installed {
			t.Errorf("%s: reported installed from empty output", item.Module)
		}
		if !nameModules[item.Module] {
			continue
		}
		if !item.Unknown {
			t.Errorf("%s: name-scoped module with no name must be unknown", item.Module)
		}
		if item.Detail == "" {
			t.Errorf("%s: unknown row carries no explanation", item.Module)
		}
		if item.Removable {
			t.Errorf("%s: an unverifiable row must not be removable", item.Module)
		}
	}
}

// A probe that fails at the transport must not abort the whole inventory: the
// remaining rows are still worth reporting.
func TestPersistenceInventorySurvivesAProbeFailure(t *testing.T) {
	var calls int32
	stub := &rpcStub{}
	stub.execWindows = func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return nil, errors.New("implant timeout")
		}
		return okExecute(""), nil
	}
	c := &Client{RPC: stub}

	list, err := c.PersistenceList("s-1", platformWindows, "")
	if err != nil {
		t.Fatalf("one failed probe aborted the inventory: %v", err)
	}
	if len(list.Items) == 0 {
		t.Fatal("no rows reported")
	}
}

// --- RunAs ---

// RunAs reaches a different user's context, so a console window there is both a
// giveaway and an artefact attributed to the wrong account.
func TestRunAsHidesTheWindow(t *testing.T) {
	var got *sliverpb.RunAsReq
	stub := &rpcStub{}
	stubRunAs := &runAsStub{rpcStub: stub, onCall: func(in *sliverpb.RunAsReq) (*sliverpb.RunAs, error) {
		got = in
		return &sliverpb.RunAs{Response: &commonpb.Response{}}, nil
	}}
	c := &Client{RPC: stubRunAs}

	if _, _, err := c.RunAs("s-1", `CORP\alice`, "cmd.exe", "/c whoami"); err != nil {
		t.Fatalf("RunAs: %v", err)
	}
	if got == nil {
		t.Fatal("RunAs was not called")
	}
	if !got.HideWindow {
		t.Error("HideWindow = false: the process would appear on the target's desktop")
	}
}

// runAsStub extends rpcStub with the one additional RPC RunAs needs.
type runAsStub struct {
	*rpcStub
	onCall func(*sliverpb.RunAsReq) (*sliverpb.RunAs, error)
}

func (s *runAsStub) RunAs(_ context.Context, in *sliverpb.RunAsReq, _ ...grpc.CallOption) (*sliverpb.RunAs, error) {
	return s.onCall(in)
}
