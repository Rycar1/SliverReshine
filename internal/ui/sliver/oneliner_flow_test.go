package sliver

import (
	"strings"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
)

// stageableClient is a client whose listener list has one HTTP listener on
// port 8080, with the website it serves recorded the way starting it from this
// console would have.
func stageableClient(t *testing.T, jobs ...*clientpb.Job) (*Client, *webDeliveryStub) {
	t.Helper()
	isolateListenerSites(t)
	s := &webDeliveryStub{
		jobs:     jobs,
		profiles: []*clientpb.ImplantProfile{deliveryProfile("unused")},
	}
	return &Client{RPC: s}, s
}

func httpJob(id uint32, port uint32) *clientpb.Job {
	return &clientpb.Job{ID: id, Name: "http", Protocol: "http", Port: port, Domains: []string{"c2.example.com"}}
}

// The happy path: a listener the console started, an operator host, and a
// Windows command that fetches the stage from that host.
func TestOneLinerBuildsAndPublishesAStage(t *testing.T) {
	c, s := stageableClient(t, httpJob(5, 8080))
	c.rememberListenerSite(5, "webdelivery")

	res, err := c.OneLiner(OneLinerRequest{JobID: 5, Platform: OneLinerWindows, Host: "10.0.0.5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.URL != "http://10.0.0.5:8080/stage.woff" {
		t.Errorf("URL = %q", res.URL)
	}
	if res.C2URL != "http://10.0.0.5:8080" {
		t.Errorf("C2URL = %q", res.C2URL)
	}
	if !strings.Contains(res.Command, res.URL) {
		t.Errorf("command %q does not fetch the URL", res.Command)
	}
	if res.Platform != "windows" || res.Delivery != string(WebDeliveryPSH) {
		t.Errorf("platform/delivery = %q/%q", res.Platform, res.Delivery)
	}
	if res.StagedAs != "stage-windows-5" {
		t.Errorf("StagedAs = %q", res.StagedAs)
	}
	if len(res.Alternatives) == 0 {
		t.Error("no alternatives were offered")
	}
	if res.Reused {
		t.Error("a fresh build was reported as reused")
	}
	if len(s.savedProfiles) != 1 {
		t.Fatalf("profiles saved = %d, want 1", len(s.savedProfiles))
	}
	if got := s.savedProfiles[0].Name; got != "oneliner-stage-windows-5" {
		t.Errorf("profile name = %q", got)
	}
	cfg := s.savedProfiles[0].Config
	if cfg == nil || len(cfg.C2) == 0 {
		t.Fatalf("saved profile has no C2: %+v", cfg)
	}
	// The profile dials the listener's own address, scheme included, which is
	// what the console reports as the C2 URL.
	if cfg.C2[0].URL != res.C2URL {
		t.Errorf("profile C2 = %q, want %q (the listener's address)", cfg.C2[0].URL, res.C2URL)
	}
}

// A repeated identical request is answered from the first build rather than
// rebuilding a ~20 MB implant.
func TestOneLinerReusesAnIdenticalBuild(t *testing.T) {
	c, s := stageableClient(t, httpJob(5, 8080))
	c.rememberListenerSite(5, "webdelivery")

	req := OneLinerRequest{JobID: 5, Platform: OneLinerWindows, Host: "10.0.0.5"}
	first, err := c.OneLiner(req)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := c.OneLiner(req)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !second.Reused {
		t.Error("the second identical request rebuilt instead of reusing")
	}
	if second.Command != first.Command || second.URL != first.URL {
		t.Error("the reused result differs from the built one")
	}
	if len(s.savedProfiles) != 1 {
		t.Errorf("profiles saved = %d, want 1 (the second request must not rebuild)", len(s.savedProfiles))
	}

	// Force is the escape hatch: it must rebuild even though the fingerprint
	// matches.
	forced, err := c.OneLiner(OneLinerRequest{JobID: 5, Platform: OneLinerWindows, Host: "10.0.0.5", Force: true})
	if err != nil {
		t.Fatalf("forced: %v", err)
	}
	if forced.Reused {
		t.Error("Force returned a reused result")
	}
	if len(s.savedProfiles) != 2 {
		t.Errorf("profiles saved = %d, want 2 after Force", len(s.savedProfiles))
	}
}

func TestOneLinerRefusesWhatItCannotBuild(t *testing.T) {
	c, _ := stageableClient(t,
		httpJob(5, 8080),
		&clientpb.Job{ID: 6, Name: "mtls", Protocol: "mtls", Port: 8888},
	)
	c.rememberListenerSite(5, "webdelivery")

	t.Run("unknown listener", func(t *testing.T) {
		_, err := c.OneLiner(OneLinerRequest{JobID: 99, Platform: OneLinerWindows, Host: "10.0.0.5"})
		if err == nil || !strings.Contains(err.Error(), "no listener with job id 99") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("non-http listener", func(t *testing.T) {
		_, err := c.OneLiner(OneLinerRequest{JobID: 6, Platform: OneLinerWindows, Host: "10.0.0.5"})
		if err == nil || !strings.Contains(err.Error(), "cannot serve a stage") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown platform", func(t *testing.T) {
		_, err := c.OneLiner(OneLinerRequest{JobID: 5, Platform: "plan9", Host: "10.0.0.5"})
		if err == nil || !strings.Contains(err.Error(), "unsupported platform") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("website unknown", func(t *testing.T) {
		// A listener this console did not start has no recorded website, and
		// publishing to a guess would produce a 404 the operator cannot see.
		c2, _ := stageableClient(t, httpJob(8, 8081))
		_, err := c2.OneLiner(OneLinerRequest{JobID: 8, Platform: OneLinerWindows, Host: "10.0.0.5"})
		if err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("err = %v", err)
		}
	})
}

// OneLinerAll builds one stage per platform, each on its own path so the
// publishes cannot overwrite one another.
func TestOneLinerAllPublishesOnePathPerPlatform(t *testing.T) {
	c, s := stageableClient(t, httpJob(5, 8080))
	c.rememberListenerSite(5, "webdelivery")

	results := c.OneLinerAll(OneLinerRequest{JobID: 5, Host: "10.0.0.5"}, []OneLinerPlatform{OneLinerWindows, OneLinerLinux})
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	paths := map[string]bool{}
	for _, r := range results {
		if r.Error != "" {
			t.Errorf("platform %s failed: %s", r.Platform, r.Error)
		}
		if r.Command == "" {
			t.Errorf("platform %s has no command", r.Platform)
		}
		if paths[r.Path] {
			t.Errorf("path %q was used twice", r.Path)
		}
		paths[r.Path] = true
	}
	if len(s.addCalls) != 2 {
		t.Errorf("publishes = %d, want one per platform", len(s.addCalls))
	}
}
