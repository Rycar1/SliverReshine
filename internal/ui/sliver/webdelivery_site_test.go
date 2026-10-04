package sliver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"google.golang.org/grpc"
)

// siteJobs is a fixed job list for the delivery-site tests, built from the
// shared jobsStub (see data_test.go).
func siteJobs(jobs ...*clientpb.Job) *jobsStub {
	return &jobsStub{jobs: &clientpb.Jobs{Active: jobs}}
}

func siteJob(id uint32, name string, port uint32) *clientpb.Job {
	return &clientpb.Job{ID: id, Name: name, Protocol: name, Port: port}
}

// A free port means nothing to reuse: the requested website is used and a
// listener is started for it.
func TestDeliverySiteForPortUsesRequestedWhenFree(t *testing.T) {
	c := &Client{RPC: siteJobs(siteJob(1, "http", 9999))}
	website, jobID, reuse, err := c.deliverySiteForPort(8080, "webdelivery", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reuse || jobID != 0 {
		t.Errorf("reuse=%v jobID=%d, want false/0", reuse, jobID)
	}
	if website != "webdelivery" {
		t.Errorf("website = %q, want webdelivery", website)
	}
}

// The defect: the port is already served by a listener this console started,
// whose website is known. Publishing to the requested website instead is
// invisible to it and the URL 404s, so the listener's own website has to win.
func TestDeliverySiteForPortReusesKnownListenerWebsite(t *testing.T) {
	c := &Client{RPC: siteJobs(siteJob(7, "https", 8443))}
	c.rememberListenerSite(7, "staging")

	website, jobID, reuse, err := c.deliverySiteForPort(8443, "webdelivery", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reuse || jobID != 7 {
		t.Errorf("reuse=%v jobID=%d, want true/7", reuse, jobID)
	}
	if website != "staging" {
		t.Errorf("website = %q, want the listener's own site staging", website)
	}
}

// A listener on the port whose website is unknown cannot be published to
// safely, so the request is refused rather than handed a 404 URL.
func TestDeliverySiteForPortRefusesUnknownWebsite(t *testing.T) {
	c := &Client{RPC: siteJobs(siteJob(3, "http", 8080))}
	_, _, _, err := c.deliverySiteForPort(8080, "webdelivery", false)
	if err == nil {
		t.Fatal("an unknown listener website was accepted")
	}
	for _, want := range []string{"8080", "404", "Name the website"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
}

// An operator who names a website for a listener the console did not start has
// nothing better to go on, so the name is honoured and the listener reused.
func TestDeliverySiteForPortHonoursAnExplicitName(t *testing.T) {
	c := &Client{RPC: siteJobs(siteJob(3, "http", 8080))}
	website, jobID, reuse, err := c.deliverySiteForPort(8080, "operator-site", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reuse || jobID != 3 {
		t.Errorf("reuse=%v jobID=%d, want true/3", reuse, jobID)
	}
	if website != "operator-site" {
		t.Errorf("website = %q, want the explicit name", website)
	}
}

// Only a listener that can actually serve a stage takes the port over: a
// non-staging listener on the same port must not be treated as the target.
func TestDeliverySiteForPortIgnoresNonStagingListeners(t *testing.T) {
	c := &Client{RPC: siteJobs(siteJob(4, "dns", 8080))}
	website, jobID, reuse, err := c.deliverySiteForPort(8080, "webdelivery", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reuse || jobID != 0 || website != "webdelivery" {
		t.Errorf("a non-staging listener was reused: website=%q jobID=%d reuse=%v", website, jobID, reuse)
	}
}

// A failing GetJobs must not fail the delivery: the port's state is unknown, so
// the caller's website is used and the listener start surfaces any conflict.
func TestDeliverySiteForPortFallsBackWhenJobsFail(t *testing.T) {
	c := &Client{RPC: &failingJobs{}}
	website, jobID, reuse, err := c.deliverySiteForPort(8080, "webdelivery", false)
	if err != nil {
		t.Fatalf("a failed job list must not fail the delivery: %v", err)
	}
	if reuse || jobID != 0 || website != "webdelivery" {
		t.Errorf("fallback = %q/%d/%v, want webdelivery/0/false", website, jobID, reuse)
	}
}

// failingJobs answers GetJobs with an error, so the delivery-site fallback can
// be exercised without a live server.
type failingJobs struct {
	rpcpb.SliverRPCClient
}

func (f *failingJobs) GetJobs(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Jobs, error) {
	return nil, errors.New("server unreachable")
}
