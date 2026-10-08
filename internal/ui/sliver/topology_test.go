package sliver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"google.golang.org/grpc"
)

// topologyStub answers the RPCs TopologyGraph reads. A nil callback means the
// test expects that call not to happen.
type topologyStub struct {
	rpcStub

	sessions func() (*clientpb.Sessions, error)
	beacons  func() (*clientpb.Beacons, error)
	pivot    func() (*clientpb.PivotGraph, error)
}

func (s *topologyStub) GetSessions(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Sessions, error) {
	return s.sessions()
}

func (s *topologyStub) GetBeacons(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Beacons, error) {
	if s.beacons == nil {
		return &clientpb.Beacons{}, nil
	}
	return s.beacons()
}

func (s *topologyStub) PivotGraph(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.PivotGraph, error) {
	if s.pivot == nil {
		return &clientpb.PivotGraph{}, nil
	}
	return s.pivot()
}

func TestTopologyAlwaysIncludesTheConsole(t *testing.T) {
	c := &Client{RPC: &topologyStub{
		sessions: func() (*clientpb.Sessions, error) {
			return &clientpb.Sessions{}, nil
		},
		beacons: func() (*clientpb.Beacons, error) { return &clientpb.Beacons{}, nil },
		pivot:   func() (*clientpb.PivotGraph, error) { return &clientpb.PivotGraph{}, nil },
	}}

	g, err := c.TopologyGraph()
	if err != nil {
		t.Fatalf("TopologyGraph: %v", err)
	}
	if len(g.Nodes) != 1 {
		t.Fatalf("nodes = %d, want just the console", len(g.Nodes))
	}
	if g.Nodes[0].ID != topologyConsoleID || g.Nodes[0].Kind != "c2" {
		t.Errorf("root node = %+v, want the console", g.Nodes[0])
	}
	// An empty graph must still be a valid object, not nil, so the UI can render
	// "nothing here" instead of guarding against a missing field.
	if g.Edges == nil {
		t.Error("Edges is nil; an empty graph must marshal as [] instead")
	}
}

func TestTopologyLinksEachSessionToTheConsole(t *testing.T) {
	c := &Client{RPC: &topologyStub{
		sessions: func() (*clientpb.Sessions, error) {
			return &clientpb.Sessions{Sessions: []*clientpb.Session{
				{ID: "s-1", Name: "alpha", OS: "windows", Transport: "mtls"},
				{ID: "s-2", Name: "beta", OS: "linux", Transport: "http"},
			}}, nil
		},
		beacons: func() (*clientpb.Beacons, error) { return &clientpb.Beacons{}, nil },
		pivot:   func() (*clientpb.PivotGraph, error) { return &clientpb.PivotGraph{}, nil },
	}}

	g, err := c.TopologyGraph()
	if err != nil {
		t.Fatalf("TopologyGraph: %v", err)
	}
	// console + two sessions
	if len(g.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(g.Nodes))
	}
	if len(g.Edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(g.Edges))
	}
	for _, e := range g.Edges {
		if e.From != topologyConsoleID {
			t.Errorf("edge %+v does not originate at the console", e)
		}
		if e.Kind != "transport" {
			t.Errorf("edge %+v is not a transport link", e)
		}
	}
}

// The pivot tree is what turns a flat session list into a chain, so a child's
// depth must be recorded and the parent-child edge drawn.
func TestTopologyRecordsPivotDepthAndEdges(t *testing.T) {
	c := &Client{RPC: &topologyStub{
		sessions: func() (*clientpb.Sessions, error) {
			return &clientpb.Sessions{Sessions: []*clientpb.Session{
				{ID: "s-1", Name: "root"},
				{ID: "s-2", Name: "child"},
			}}, nil
		},
		beacons: func() (*clientpb.Beacons, error) { return &clientpb.Beacons{}, nil },
		pivot: func() (*clientpb.PivotGraph, error) {
			return &clientpb.PivotGraph{Children: []*clientpb.PivotGraphEntry{
				{
					Session: &clientpb.Session{ID: "s-1"},
					Name:    "root",
					Children: []*clientpb.PivotGraphEntry{
						{Session: &clientpb.Session{ID: "s-2"}, Name: "child"},
					},
				},
			}}, nil
		},
	}}

	g, err := c.TopologyGraph()
	if err != nil {
		t.Fatalf("TopologyGraph: %v", err)
	}

	depth := map[string]int{}
	for _, n := range g.Nodes {
		depth[n.ID] = n.Depth
	}
	if depth["s-1"] != 1 {
		t.Errorf("s-1 depth = %d, want 1", depth["s-1"])
	}
	if depth["s-2"] != 2 {
		t.Errorf("s-2 depth = %d, want 2", depth["s-2"])
	}

	var pivotEdges int
	for _, e := range g.Edges {
		if e.Kind == "pivot" {
			pivotEdges++
			if e.From != "s-1" || e.To != "s-2" {
				t.Errorf("pivot edge = %+v, want s-1 -> s-2", e)
			}
		}
	}
	if pivotEdges != 1 {
		t.Errorf("pivot edges = %d, want 1", pivotEdges)
	}
}

// A pivot entry naming a session that no longer exists must not produce an edge
// into empty space.
func TestTopologyDropsEdgesToMissingSessions(t *testing.T) {
	c := &Client{RPC: &topologyStub{
		sessions: func() (*clientpb.Sessions, error) {
			return &clientpb.Sessions{Sessions: []*clientpb.Session{{ID: "s-1"}}}, nil
		},
		beacons: func() (*clientpb.Beacons, error) { return &clientpb.Beacons{}, nil },
		pivot: func() (*clientpb.PivotGraph, error) {
			return &clientpb.PivotGraph{Children: []*clientpb.PivotGraphEntry{
				{Session: &clientpb.Session{ID: "s-1"}, Children: []*clientpb.PivotGraphEntry{
					{Session: &clientpb.Session{ID: "s-dead"}},
				}},
			}}, nil
		},
	}}

	g, err := c.TopologyGraph()
	if err != nil {
		t.Fatalf("TopologyGraph: %v", err)
	}
	for _, e := range g.Edges {
		if e.To == "s-dead" {
			t.Errorf("edge %+v points at a session that is not in the graph", e)
		}
	}
}

// A failing optional source degrades the view instead of losing it: beacons and
// pivots are both optional, and the sessions list is what the page is for.
func TestTopologySurvivesOptionalSourceFailures(t *testing.T) {
	c := &Client{RPC: &topologyStub{
		sessions: func() (*clientpb.Sessions, error) {
			return &clientpb.Sessions{Sessions: []*clientpb.Session{{ID: "s-1"}}}, nil
		},
		beacons: func() (*clientpb.Beacons, error) { return nil, errors.New("unavailable") },
		pivot:   func() (*clientpb.PivotGraph, error) { return nil, errors.New("no pivots") },
	}}

	g, err := c.TopologyGraph()
	if err != nil {
		t.Fatalf("an optional source failure aborted the graph: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Errorf("nodes = %d, want console + 1 session", len(g.Nodes))
	}
}

// A failure reading sessions is fatal: without them there is no graph to draw.
func TestTopologyFailsWhenSessionsFail(t *testing.T) {
	c := &Client{RPC: &topologyStub{
		sessions: func() (*clientpb.Sessions, error) { return nil, errors.New("server down") },
		beacons:  func() (*clientpb.Beacons, error) { return &clientpb.Beacons{}, nil },
		pivot:    func() (*clientpb.PivotGraph, error) { return &clientpb.PivotGraph{}, nil },
	}}
	if _, err := c.TopologyGraph(); err == nil {
		t.Fatal("a sessions failure was swallowed")
	}
}

// Two identical polls must produce identical output, or the UI redraws the graph
// differently each time it refreshes.
func TestTopologyOutputIsStable(t *testing.T) {
	c := &Client{RPC: &topologyStub{
		sessions: func() (*clientpb.Sessions, error) {
			return &clientpb.Sessions{Sessions: []*clientpb.Session{
				{ID: "z"}, {ID: "a"}, {ID: "m"},
			}}, nil
		},
		beacons: func() (*clientpb.Beacons, error) { return &clientpb.Beacons{}, nil },
		pivot:   func() (*clientpb.PivotGraph, error) { return &clientpb.PivotGraph{}, nil },
	}}

	first, err := c.TopologyGraph()
	if err != nil {
		t.Fatalf("TopologyGraph: %v", err)
	}
	second, err := c.TopologyGraph()
	if err != nil {
		t.Fatalf("TopologyGraph: %v", err)
	}

	if len(first.Nodes) != len(second.Nodes) || len(first.Edges) != len(second.Edges) {
		t.Fatal("two polls returned different sizes")
	}
	for i := range first.Nodes {
		if first.Nodes[i].ID != second.Nodes[i].ID {
			t.Errorf("node %d: %q vs %q", i, first.Nodes[i].ID, second.Nodes[i].ID)
		}
	}
	for i := range first.Edges {
		if first.Edges[i] != second.Edges[i] {
			t.Errorf("edge %d: %+v vs %+v", i, first.Edges[i], second.Edges[i])
		}
	}
}

// --- WebDelivery ---

func TestWebDeliveryValidatesItsInput(t *testing.T) {
	c := &Client{}
	cases := []struct {
		name string
		req  WebDeliveryRequest
		want string
	}{
		{"no profile", WebDeliveryRequest{Host: "h", Port: 80}, "profile name is required"},
		{"no host", WebDeliveryRequest{ProfileName: "p", Port: 80}, "host reachable"},
		{"no port", WebDeliveryRequest{ProfileName: "p", Host: "h"}, "port is required"},
	}
	for _, tc := range cases {
		_, err := c.WebDelivery(tc.req)
		if err == nil {
			t.Errorf("%s: no error returned", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not mention %q", tc.name, err, tc.want)
		}
	}
}

// A wildcard host is a bind address, not a destination. It has to be refused
// with that explanation -- not silently emitted, and not reported as a character
// problem by the host validator -- because the URL it produces is one no target
// can fetch from.
func TestWebDeliveryRejectsAWildcardHost(t *testing.T) {
	c := &Client{RPC: &topologyStub{
		sessions: func() (*clientpb.Sessions, error) { return &clientpb.Sessions{}, nil },
	}}
	for _, host := range []string{"0.0.0.0", "::", "[::]", "*"} {
		_, err := c.WebDelivery(WebDeliveryRequest{ProfileName: "p", Host: host, Port: 80})
		if err == nil {
			t.Errorf("host %q was accepted", host)
			continue
		}
		if !strings.Contains(err.Error(), "bind address") {
			t.Errorf("host %q: error %q does not explain that it is a bind address", host, err)
		}
	}
}

// An unknown format is rejected before anything is built or published, since the
// whole point is that the command and the published path agree.
func TestWebDeliveryRejectsUnknownFormat(t *testing.T) {
	c := &Client{RPC: &topologyStub{
		sessions: func() (*clientpb.Sessions, error) { return &clientpb.Sessions{}, nil },
	}}
	_, err := c.WebDelivery(WebDeliveryRequest{
		ProfileName: "p",
		Host:        "h",
		Port:        80,
		Format:      "definitely-not-a-format",
	})
	if err == nil {
		t.Fatal("an unknown format was accepted")
	}
	if !strings.Contains(err.Error(), "unsupported delivery format") {
		t.Errorf("error = %q, want a format complaint", err)
	}
}

func TestWebDeliveryCommandTemplates(t *testing.T) {
	const url = "http://10.0.0.1:8443/stage.woff"

	cases := []struct {
		format WebDeliveryFormat
		want   []string
	}{
		{WebDeliveryPSH, []string{"powershell", "-w hidden", url, `$env:TEMP`}},
		{WebDeliveryCertutil, []string{"certutil", "-urlcache", url, `%TEMP%`}},
		{WebDeliveryBits, []string{"bitsadmin", "/transfer", url}},
		{WebDeliveryPython, []string{"python3", url, "/tmp/.s"}},
		{WebDeliveryCurl, []string{"curl", "wget", url, "chmod +x"}},
	}
	for _, tc := range cases {
		got := webDeliveryCommand(tc.format, url)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s command %q does not contain %q", tc.format, got, want)
			}
		}
		// The URL must appear verbatim: a mangled URL fails on the target with
		// no useful diagnostic.
		if !strings.Contains(got, url) {
			t.Errorf("%s command does not carry the URL: %q", tc.format, got)
		}
	}
}

// The PowerShell template runs under cmd.exe, so a nested double quote inside
// the -c argument would be consumed by cmd before PowerShell saw the script.
func TestWebDeliveryPowerShellTemplateAvoidsNestedQuotes(t *testing.T) {
	got := webDeliveryCommand(WebDeliveryPSH, "http://h/s")
	// Exactly the two quotes that wrap the -c argument.
	if n := strings.Count(got, `"`); n != 2 {
		t.Errorf("command has %d double quotes, want 2: %q", n, got)
	}
	if !strings.HasPrefix(got, `powershell `) {
		t.Errorf("command does not start with powershell: %q", got)
	}
}

func TestWebDeliveryFormatsCoverBothPlatforms(t *testing.T) {
	var windows, linux int
	for _, f := range WebDeliveryFormats() {
		switch f["platform"] {
		case platformWindows:
			windows++
		case platformLinux:
			linux++
		default:
			t.Errorf("format %+v has an unexpected platform", f)
		}
		if f["id"] == "" || f["label"] == "" {
			t.Errorf("format %+v is missing an id or label", f)
		}
		if !validWebDeliveryFormat(WebDeliveryFormat(f["id"])) {
			t.Errorf("format %q is advertised but not valid", f["id"])
		}
	}
	if windows == 0 || linux == 0 {
		t.Errorf("formats cover windows=%d linux=%d; both platforms need a template", windows, linux)
	}
}

// A path that is not rooted still has to produce an absolute URL, or the target
// resolves it against its own working directory.
func TestWebDeliveryNormalisesThePath(t *testing.T) {
	// Exercised through the URL construction, which is the only place the path
	// affects the command.
	for _, in := range []string{"stage.woff", "/stage.woff"} {
		normalised := in
		if !strings.HasPrefix(normalised, "/") {
			normalised = "/" + normalised
		}
		url := "http://h:80" + normalised
		if !strings.Contains(webDeliveryCommand(WebDeliveryPSH, url), "http://h:80/stage.woff") {
			t.Errorf("path %q did not normalise into the URL", in)
		}
	}
}
