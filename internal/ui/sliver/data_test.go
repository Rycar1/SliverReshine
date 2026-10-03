package sliver

import (
	"context"
	"testing"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"google.golang.org/grpc"
)

func TestUnixTimeString_Zero(t *testing.T) {
	if got := unixTimeString(0); got != "" {
		t.Errorf("unixTimeString(0) = %q, want empty", got)
	}
}

func TestUnixTimeString_Value(t *testing.T) {
	// 2024-01-02T03:04:05Z
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC).Unix()
	got := unixTimeString(ts)
	if got != "2024-01-02T03:04:05Z" {
		t.Errorf("unixTimeString = %q, want 2024-01-02T03:04:05Z", got)
	}
}

func TestSessionToView(t *testing.T) {
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC).Unix()
	s := &clientpb.Session{
		ID:            "abcd1234",
		Name:          "SESS-1",
		Hostname:      "victim-host",
		Username:      "root",
		PID:           1337,
		OS:            "linux",
		Arch:          "amd64",
		Transport:     "mtls",
		RemoteAddress: "10.0.0.5:4242",
		LastCheckin:   ts,
		ActiveC2:      "mtls://1.2.3.4:8888",
		Version:       "1.5.30",
		IsDead:        false,
	}

	v := sessionToView(s)
	if v.ID != "abcd1234" {
		t.Errorf("ID = %q", v.ID)
	}
	if v.Name != "SESS-1" || v.Hostname != "victim-host" || v.Username != "root" {
		t.Errorf("identity fields wrong: %+v", v)
	}
	if v.PID != 1337 || v.OS != "linux" || v.Arch != "amd64" {
		t.Errorf("system fields wrong: %+v", v)
	}
	if v.Transport != "mtls" || v.RemoteAddress != "10.0.0.5:4242" {
		t.Errorf("network fields wrong: %+v", v)
	}
	if v.LastCheckin != "2024-01-02T03:04:05Z" {
		t.Errorf("LastCheckin = %q", v.LastCheckin)
	}
	if v.IsDead {
		t.Error("IsDead should be false")
	}
	if !v.IsInteractive {
		t.Error("session should be interactive")
	}
}

func TestBeaconToView(t *testing.T) {
	ts := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC).Unix()
	b := &clientpb.Beacon{
		ID:            "beacon-1",
		Name:          "B-1",
		Hostname:      "win-host",
		Username:      "admin",
		OS:            "windows",
		Arch:          "amd64",
		Transport:     "https",
		RemoteAddress: "192.168.1.10:5555",
		LastCheckin:   ts,
		NextCheckin:   ts + 30,
		Interval:      60,
		Jitter:        15,
		ActiveC2:      "https://1.2.3.4:443",
	}

	v := beaconToView(b)
	if v.ID != "beacon-1" || v.Name != "B-1" {
		t.Errorf("ID/Name = %q/%q", v.ID, v.Name)
	}
	if v.OS != "windows" || v.Arch != "amd64" {
		t.Errorf("OS/Arch = %q/%q", v.OS, v.Arch)
	}
	if v.Interval != 60 || v.Jitter != 15 {
		t.Errorf("Interval/Jitter = %d/%d", v.Interval, v.Jitter)
	}
	if v.LastCheckin != "2024-02-03T04:05:06Z" {
		t.Errorf("LastCheckin = %q", v.LastCheckin)
	}
	if v.NextCheckin != "2024-02-03T04:05:36Z" {
		t.Errorf("NextCheckin = %q", v.NextCheckin)
	}
}

func TestEventToView_SessionEvent(t *testing.T) {
	e := &clientpb.Event{
		EventType: "session-opened",
		Session: &clientpb.Session{
			ID:       "s1",
			Name:     "SESS-1",
			Hostname: "h1",
		},
	}
	v := eventToView(e)
	if v.Type != "session-opened" {
		t.Errorf("Type = %q", v.Type)
	}
	if v.Session == nil || v.Session.ID != "s1" {
		t.Errorf("Session not mapped: %+v", v.Session)
	}
	if v.Beacon != nil {
		t.Error("Beacon should be nil")
	}
}

func TestEventToView_JobEvent(t *testing.T) {
	e := &clientpb.Event{
		EventType: "job-started",
		Job: &clientpb.Job{
			ID:       7,
			Name:     "mtls listener",
			Protocol: "mtls",
			Port:     8888,
			Domains:  []string{"a.com"},
		},
	}
	v := eventToView(e)
	if v.Job == nil {
		t.Fatal("Job should be mapped")
	}
	if v.Job.ID != 7 || v.Job.Type != "mtls" || v.Job.Port != 8888 {
		t.Errorf("Job fields wrong: %+v", v.Job)
	}
	if len(v.Job.Domains) != 1 || v.Job.Domains[0] != "a.com" {
		t.Errorf("Job domains wrong: %+v", v.Job.Domains)
	}
}

func TestEventToView_JobNoDomains(t *testing.T) {
	e := &clientpb.Event{
		EventType: "job-stopped",
		Job:       &clientpb.Job{ID: 1},
	}
	v := eventToView(e)
	if v.Job == nil {
		t.Fatal("Job should be mapped")
	}
	if v.Job.Domains == nil {
		t.Error("Domains should be empty slice, not nil")
	}
	if len(v.Job.Domains) != 0 {
		t.Errorf("Domains should be empty, got %v", v.Job.Domains)
	}
}

func TestConfigToView(t *testing.T) {
	c := &clientpb.ImplantConfig{
		GOOS:             "windows",
		GOARCH:           "amd64",
		Format:           clientpb.OutputFormat_EXECUTABLE,
		Debug:            true,
		Evasion:          false,
		ObfuscateSymbols: true,
		IsBeacon:         false,
		// Nanoseconds, because that is what the protobuf carries. The view is
		// the seconds an operator reads; the raw values here are what produced
		// "60000000000s / 20000000000%" in the table.
		BeaconInterval:      int64(60 * time.Second),
		BeaconJitter:        int64(20 * time.Second),
		ReconnectInterval:   int64(45 * time.Second),
		MaxConnectionErrors: 500,
		C2: []*clientpb.ImplantC2{
			{URL: "mtls://1.2.3.4:8888", Priority: 1},
			{URL: "https://example.com", Priority: 2},
		},
	}

	v := configToView(c, "implant-a")
	if v == nil {
		t.Fatal("configToView returned nil")
	}
	if v.Name != "implant-a" || v.OS != "windows" || v.Arch != "amd64" {
		t.Errorf("identity wrong: %+v", v)
	}
	if v.Format != "EXECUTABLE" {
		t.Errorf("Format = %q, want EXECUTABLE", v.Format)
	}
	if !v.Obfuscate || !v.Debug {
		t.Errorf("flags wrong: obfuscate=%v debug=%v", v.Obfuscate, v.Debug)
	}
	if len(v.C2) != 2 {
		t.Fatalf("expected 2 C2 entries, got %d", len(v.C2))
	}
	if v.C2[0].URL != "mtls://1.2.3.4:8888" {
		t.Errorf("C2[0] = %+v", v.C2[0])
	}
	// The intervals are seconds in the view, whatever the wire carried.
	if v.Interval != 60 || v.Jitter != 20 {
		t.Errorf("Interval/Jitter = %d/%d, want 60/20 (seconds)", v.Interval, v.Jitter)
	}
	if v.BeaconInt != 60 || v.BeaconJit != 20 {
		t.Errorf("BeaconInterval/BeaconJitter = %d/%d, want 60/20 (seconds)", v.BeaconInt, v.BeaconJit)
	}
}

func TestConfigToView_Nil(t *testing.T) {
	if v := configToView(nil, ""); v != nil {
		t.Errorf("expected nil, got %+v", v)
	}
}

// jobsStub returns a fixed job list so the listener table's CanStage flag can be
// pinned. That flag is what gates the "staging command" button on the listeners
// page, so a wrong value is exactly the greyed-out button an operator reads as
// "the feature is broken".
type jobsStub struct {
	rpcpb.SliverRPCClient

	jobs *clientpb.Jobs
}

func (s *jobsStub) GetJobs(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Jobs, error) {
	return s.jobs, nil
}

func TestJobsSetsCanStageFromTheListenerName(t *testing.T) {
	c := &Client{RPC: &jobsStub{jobs: &clientpb.Jobs{Active: []*clientpb.Job{
		{ID: 1, Name: "http", Protocol: "http", Port: 80},
		{ID: 2, Name: "https", Protocol: "https", Port: 443},
		{ID: 3, Name: "mtls", Protocol: "mtls", Port: 8888},
		{ID: 4, Name: "dns", Protocol: "dns", Port: 53},
		{ID: 5, Name: "wg", Protocol: "wg", Port: 51820},
		{ID: 6, Name: "tcp-pivot", Protocol: "tcp-pivot", Port: 4444},
		// The console's own gRPC listener is a job on the server. It must not
		// reach the listeners page at all, let alone be offered a stage.
		{ID: 7, Name: "grpc/mtls", Protocol: "grpc", Description: "client listener"},
	}}}}

	jobs, err := c.Jobs()
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if len(jobs) != 6 {
		t.Fatalf("Jobs returned %d rows, want 6 (the client listener must be filtered out)", len(jobs))
	}

	want := map[string]bool{
		"http": true, "https": true,
		"mtls": false, "dns": false, "wg": false, "tcp-pivot": false,
	}
	for _, j := range jobs {
		w, ok := want[j.Name]
		if !ok {
			t.Fatalf("unexpected job %q reached the listeners page", j.Name)
		}
		if j.CanStage != w {
			t.Errorf("job %q CanStage = %v, want %v", j.Name, j.CanStage, w)
		}
	}
}
