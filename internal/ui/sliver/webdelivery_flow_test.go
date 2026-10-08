package sliver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"google.golang.org/grpc"
)

// webDeliveryStub is the smallest SliverRPCClient that can carry a full
// WebDelivery call: job list, profiles, builds, a build, content publishing and
// the listener start. Every call is recorded so a test can assert on what the
// console asked the server to do, and every step can be made to fail.
type webDeliveryStub struct {
	rpcpb.SliverRPCClient

	jobs     []*clientpb.Job
	profiles []*clientpb.ImplantProfile
	builds   map[string]*clientpb.ImplantConfig

	profilesErr error
	buildsErr   error
	generateErr error
	regenErr    error
	addErr      error
	startErr    error

	mu             sync.Mutex
	addCalls       []*clientpb.WebsiteAddContent
	startCalls     []*clientpb.HTTPListenerReq
	savedProfiles  []*clientpb.ImplantProfile
	regenCalls     int
	genCalls       int
	saveProfileErr error
}

func (s *webDeliveryStub) GetJobs(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Jobs, error) {
	return &clientpb.Jobs{Active: s.jobs}, nil
}

func (s *webDeliveryStub) ImplantProfiles(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.ImplantProfiles, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profilesErr != nil {
		return nil, s.profilesErr
	}
	// A profile saved earlier in the same call (OneLiner stages its profile
	// before it delivers) must be visible here, exactly as the server would
	// report it.
	profiles := make([]*clientpb.ImplantProfile, 0, len(s.profiles)+len(s.savedProfiles))
	profiles = append(profiles, s.profiles...)
	profiles = append(profiles, s.savedProfiles...)
	return &clientpb.ImplantProfiles{Profiles: profiles}, nil
}

func (s *webDeliveryStub) ImplantBuilds(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.ImplantBuilds, error) {
	if s.buildsErr != nil {
		return nil, s.buildsErr
	}
	return &clientpb.ImplantBuilds{Configs: s.builds}, nil
}

func (s *webDeliveryStub) Generate(_ context.Context, _ *clientpb.GenerateReq, _ ...grpc.CallOption) (*clientpb.Generate, error) {
	s.mu.Lock()
	s.genCalls++
	s.mu.Unlock()
	if s.generateErr != nil {
		return nil, s.generateErr
	}
	return &clientpb.Generate{File: &commonpb.File{Name: "stage", Data: []byte("MZstage")}}, nil
}

func (s *webDeliveryStub) Regenerate(_ context.Context, _ *clientpb.RegenerateReq, _ ...grpc.CallOption) (*clientpb.Generate, error) {
	s.mu.Lock()
	s.regenCalls++
	s.mu.Unlock()
	if s.regenErr != nil {
		return nil, s.regenErr
	}
	return &clientpb.Generate{File: &commonpb.File{Name: "stage", Data: []byte("MZregen")}}, nil
}

func (s *webDeliveryStub) SaveImplantProfile(_ context.Context, in *clientpb.ImplantProfile, _ ...grpc.CallOption) (*clientpb.ImplantProfile, error) {
	s.mu.Lock()
	s.savedProfiles = append(s.savedProfiles, in)
	s.mu.Unlock()
	if s.saveProfileErr != nil {
		return nil, s.saveProfileErr
	}
	return in, nil
}

func (s *webDeliveryStub) WebsiteAddContent(_ context.Context, in *clientpb.WebsiteAddContent, _ ...grpc.CallOption) (*clientpb.Website, error) {
	s.mu.Lock()
	s.addCalls = append(s.addCalls, in)
	s.mu.Unlock()
	if s.addErr != nil {
		return nil, s.addErr
	}
	return &clientpb.Website{Name: in.Name, Contents: in.Contents}, nil
}

func (s *webDeliveryStub) StartHTTPListener(_ context.Context, in *clientpb.HTTPListenerReq, _ ...grpc.CallOption) (*clientpb.ListenerJob, error) {
	s.mu.Lock()
	s.startCalls = append(s.startCalls, in)
	s.mu.Unlock()
	if s.startErr != nil {
		return nil, s.startErr
	}
	return &clientpb.ListenerJob{JobID: 42}, nil
}

func deliveryProfile(name string) *clientpb.ImplantProfile {
	return &clientpb.ImplantProfile{Name: name, Config: &clientpb.ImplantConfig{}}
}

// A well-formed request publishes the stage, starts the listener and hands back
// a command that names the published URL.
func TestWebDeliveryHappyPathStartsListener(t *testing.T) {
	isolateListenerSites(t)

	s := &webDeliveryStub{
		jobs:     []*clientpb.Job{siteJob(1, "http", 9999)},
		profiles: []*clientpb.ImplantProfile{deliveryProfile("stage2")},
	}
	c := &Client{RPC: s}

	res, err := c.WebDelivery(WebDeliveryRequest{
		ProfileName: "stage2",
		Host:        "10.0.0.5",
		Port:        8080,
		Format:      WebDeliveryPSH,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.URL != "http://10.0.0.5:8080/stage.woff" {
		t.Errorf("URL = %q", res.URL)
	}
	if !strings.Contains(res.Command, res.URL) {
		t.Errorf("command %q does not fetch the URL", res.Command)
	}
	if res.JobID != 42 {
		t.Errorf("JobID = %d, want 42", res.JobID)
	}
	if res.Warning != "" {
		t.Errorf("unexpected warning: %q", res.Warning)
	}
	if len(s.startCalls) != 1 {
		t.Fatalf("start calls = %d, want 1", len(s.startCalls))
	}
	got := s.startCalls[0]
	if got.Domain != "10.0.0.5" || got.Host != "10.0.0.5" || got.Port != 8080 || got.Website != "webdelivery" {
		t.Errorf("listener request = %+v", got)
	}
	if len(s.addCalls) != 1 {
		t.Fatalf("content calls = %d, want 1", len(s.addCalls))
	}
	if _, ok := s.addCalls[0].Contents["/stage.woff"]; !ok {
		t.Errorf("stage not published at /stage.woff: %v", s.addCalls[0].Contents)
	}
	if s.genCalls != 1 {
		t.Errorf("generate calls = %d, want 1", s.genCalls)
	}
}

// A listener already bound to the port owns it: its website wins, the listener
// is reused rather than started twice, and the operator is told why.
func TestWebDeliveryReusesListenerOnPort(t *testing.T) {
	isolateListenerSites(t)

	s := &webDeliveryStub{
		jobs:     []*clientpb.Job{siteJob(7, "https", 8443)},
		profiles: []*clientpb.ImplantProfile{deliveryProfile("stage2")},
	}
	c := &Client{RPC: s}
	c.rememberListenerSite(7, "staging")

	res, err := c.WebDelivery(WebDeliveryRequest{ProfileName: "stage2", Host: "10.0.0.5", Port: 8443})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.JobID != 7 {
		t.Errorf("JobID = %d, want the reused 7", res.JobID)
	}
	if res.Website != "staging" {
		t.Errorf("website = %q, want the listener's own site", res.Website)
	}
	if !strings.Contains(res.Warning, "reused") {
		t.Errorf("warning %q does not explain the reuse", res.Warning)
	}
	if len(s.startCalls) != 0 {
		t.Errorf("a second listener was started on a served port: %+v", s.startCalls)
	}
}

// A listener that will not bind is a warning, not a failure: the stage is
// published and the command still names the right URL.
func TestWebDeliveryListenerFailureIsAWarning(t *testing.T) {
	isolateListenerSites(t)

	s := &webDeliveryStub{
		jobs:     []*clientpb.Job{siteJob(1, "http", 9999)},
		profiles: []*clientpb.ImplantProfile{deliveryProfile("stage2")},
		startErr: errors.New("address already in use"),
	}
	c := &Client{RPC: s}

	res, err := c.WebDelivery(WebDeliveryRequest{ProfileName: "stage2", Host: "10.0.0.5", Port: 8080})
	if err != nil {
		t.Fatalf("a failed listener must not fail the delivery: %v", err)
	}
	if res.JobID != 0 {
		t.Errorf("JobID = %d, want 0", res.JobID)
	}
	if !strings.Contains(res.Warning, "no listener was started") {
		t.Errorf("warning %q does not report the failure", res.Warning)
	}
	if res.URL != "http://10.0.0.5:8080/stage.woff" {
		t.Errorf("URL = %q", res.URL)
	}
}

// A profile that does not exist is refused before any listener is started.
func TestWebDeliveryUnknownProfileIsRefused(t *testing.T) {
	isolateListenerSites(t)

	s := &webDeliveryStub{
		jobs:     []*clientpb.Job{},
		profiles: []*clientpb.ImplantProfile{deliveryProfile("other")},
	}
	c := &Client{RPC: s}

	_, err := c.WebDelivery(WebDeliveryRequest{ProfileName: "missing", Host: "10.0.0.5", Port: 8080})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want a not-found error", err)
	}
	if len(s.startCalls) != 0 {
		t.Errorf("a listener was started for a missing profile: %+v", s.startCalls)
	}
}

func TestWebDeliveryValidatesRequest(t *testing.T) {
	isolateListenerSites(t)
	c := &Client{RPC: &webDeliveryStub{}}

	cases := []struct {
		name string
		req  WebDeliveryRequest
		want string
	}{
		{"no profile", WebDeliveryRequest{Host: "10.0.0.5", Port: 8080}, "profile name is required"},
		{"wildcard host", WebDeliveryRequest{ProfileName: "p", Host: "0.0.0.0", Port: 8080}, "bind address"},
		{"empty host", WebDeliveryRequest{ProfileName: "p", Port: 8080}, "host"},
		{"no port", WebDeliveryRequest{ProfileName: "p", Host: "10.0.0.5"}, "port is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.WebDelivery(tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// An unsupported format is refused before the build runs.
func TestWebDeliveryRefusesUnsupportedFormat(t *testing.T) {
	isolateListenerSites(t)
	s := &webDeliveryStub{profiles: []*clientpb.ImplantProfile{deliveryProfile("p")}}
	c := &Client{RPC: s}

	_, err := c.WebDelivery(WebDeliveryRequest{ProfileName: "p", Host: "10.0.0.5", Port: 8080, Format: "telnet"})
	if err == nil || !strings.Contains(err.Error(), "unsupported delivery format") {
		t.Fatalf("err = %v", err)
	}
	if s.genCalls != 0 {
		t.Errorf("a build ran for an unsupported format")
	}
}

// The path is normalised, an existing build is regenerated rather than rebuilt,
// and an IPv6 host is bracketed in the URL.
func TestWebDeliveryNormalisesPathAndReusesBuild(t *testing.T) {
	isolateListenerSites(t)

	s := &webDeliveryStub{
		jobs:     []*clientpb.Job{},
		profiles: []*clientpb.ImplantProfile{deliveryProfile("stage2")},
		builds:   map[string]*clientpb.ImplantConfig{"stage2": {}},
	}
	c := &Client{RPC: s}

	res, err := c.WebDelivery(WebDeliveryRequest{
		ProfileName: "stage2",
		Host:        "2001:db8::1",
		Port:        9000,
		Path:        "payload.bin",
		Website:     "custom",
		Format:      WebDeliveryCurl,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.URL != "http://[2001:db8::1]:9000/payload.bin" {
		t.Errorf("URL = %q", res.URL)
	}
	if res.Website != "custom" {
		t.Errorf("website = %q, want custom", res.Website)
	}
	if s.regenCalls != 1 || s.genCalls != 0 {
		t.Errorf("regen=%d gen=%d, want an existing build regenerated", s.regenCalls, s.genCalls)
	}
	if _, ok := s.addCalls[0].Contents["/payload.bin"]; !ok {
		t.Errorf("stage not published at the normalised path: %v", s.addCalls[0].Contents)
	}
}

// Failures in the build, the publish and the profile list are surfaced as
// errors, not swallowed.
func TestWebDeliverySurfacesBackendErrors(t *testing.T) {
	isolateListenerSites(t)

	t.Run("profile list", func(t *testing.T) {
		s := &webDeliveryStub{profilesErr: errors.New("rpc down")}
		if _, err := (&Client{RPC: s}).WebDelivery(WebDeliveryRequest{ProfileName: "p", Host: "10.0.0.5", Port: 8080}); err == nil {
			t.Fatal("a failed profile list was ignored")
		}
	})
	t.Run("build", func(t *testing.T) {
		s := &webDeliveryStub{
			profiles:    []*clientpb.ImplantProfile{deliveryProfile("p")},
			generateErr: errors.New("compiler missing"),
		}
		if _, err := (&Client{RPC: s}).WebDelivery(WebDeliveryRequest{ProfileName: "p", Host: "10.0.0.5", Port: 8080}); err == nil {
			t.Fatal("a failed build was ignored")
		}
	})
	t.Run("publish", func(t *testing.T) {
		s := &webDeliveryStub{
			profiles: []*clientpb.ImplantProfile{deliveryProfile("p")},
			addErr:   errors.New("no such website"),
		}
		if _, err := (&Client{RPC: s}).WebDelivery(WebDeliveryRequest{ProfileName: "p", Host: "10.0.0.5", Port: 8080}); err == nil {
			t.Fatal("a failed publish was ignored")
		}
	})
}
