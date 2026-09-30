package sliver

import (
	"testing"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
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
		BeaconInterval:   60,
		BeaconJitter:     20,
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
}

func TestConfigToView_Nil(t *testing.T) {
	if v := configToView(nil, ""); v != nil {
		t.Errorf("expected nil, got %+v", v)
	}
}
