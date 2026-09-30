package sliver

import (
	"fmt"
	"sort"
	"strings"
)

// TopologyNode is one vertex in the console's network view.
//
// Sliver gives the operator three separate lists (sessions, beacons, hosts) plus
// a pivot tree, and the relationships that actually describe an operation live
// across all of them. This type and TopologyEdge are the flattened form the UI
// draws: every vertex is a session, a beacon or the console itself, and every
// edge names what carries it.
type TopologyNode struct {
	ID       string `json:"ID"`
	Kind     string `json:"Kind"` // c2 | session | beacon
	Label    string `json:"Label"`
	Hostname string `json:"Hostname"`
	Username string `json:"Username"`
	OS       string `json:"OS"`
	Arch     string `json:"Arch"`
	Address  string `json:"Address"`
	// Transport is the implant's own C2 protocol (mtls, http, wireguard, ...).
	Transport string `json:"Transport"`
	// Depth is the pivot hop count from the console: 0 is a direct implant, 1 a
	// session reached through one pivot listener, and so on. The UI uses it to
	// lay the graph out in columns, which makes the pivot chain readable without
	// doing force simulation.
	Depth int  `json:"Depth"`
	Dead  bool `json:"Dead"`
}

// TopologyEdge is a link between two vertices.
type TopologyEdge struct {
	From string `json:"From"`
	To   string `json:"To"`
	// Kind is one of: transport, pivot, socks, portfwd, rportfwd.
	Kind  string `json:"Kind"`
	Label string `json:"Label"`
}

// TopologyGraph is the whole view.
type TopologyGraph struct {
	Nodes []TopologyNode `json:"nodes"`
	Edges []TopologyEdge `json:"edges"`
}

// TopologyNodeIDs are the fixed identifiers for vertices that are not sessions.
const (
	topologyConsoleID  = "c2"
	topologyConsoleLbl = "Console"
)

// TopologyGraph flattens every relationship the console knows about into one
// graph: console-to-implant transport links, session-to-session pivot links, and
// the console-side proxy/forward links.
//
// A source that fails is skipped rather than aborting the whole graph. The pivot
// call in particular fails on a server with no pivots configured, and losing the
// entire view because one optional subsystem is empty would make this useless in
// exactly the common case.
func (c *Client) TopologyGraph() (*TopologyGraph, error) {
	graph := &TopologyGraph{Nodes: []TopologyNode{}, Edges: []TopologyEdge{}}

	// The console is always present: it is the root of every transport edge, and
	// a graph with no sessions should still render as "the console, alone".
	graph.addNode(TopologyNode{
		ID:    topologyConsoleID,
		Kind:  "c2",
		Label: topologyConsoleLbl,
		Depth: 0,
	})

	sessions, err := c.Sessions()
	if err != nil {
		return nil, err
	}
	for _, s := range sessions {
		graph.addNode(TopologyNode{
			ID:        s.ID,
			Kind:      "session",
			Label:     firstNonEmpty(s.Name, s.Hostname, s.ID),
			Hostname:  s.Hostname,
			Username:  s.Username,
			OS:        s.OS,
			Arch:      s.Arch,
			Address:   s.RemoteAddress,
			Transport: s.Transport,
			Dead:      s.IsDead,
		})
		graph.addEdge(TopologyEdge{
			From:  topologyConsoleID,
			To:    s.ID,
			Kind:  "transport",
			Label: firstNonEmpty(s.Transport, "session"),
		})
	}

	// Beacons are terminals in this view: they are not interactive and cannot
	// pivot, but they are still footholds and the operator wants to see them.
	if beacons, err := c.Beacons(); err == nil {
		for _, b := range beacons {
			graph.addNode(TopologyNode{
				ID:        b.ID,
				Kind:      "beacon",
				Label:     firstNonEmpty(b.Name, b.Hostname, b.ID),
				Hostname:  b.Hostname,
				Username:  b.Username,
				OS:        b.OS,
				Arch:      b.Arch,
				Address:   b.RemoteAddress,
				Transport: b.Transport,
			})
			graph.addEdge(TopologyEdge{
				From:  topologyConsoleID,
				To:    b.ID,
				Kind:  "transport",
				Label: firstNonEmpty(b.Transport, "beacon"),
			})
		}
	}

	// The pivot tree is what turns a flat session list into a chain. Depth is
	// propagated from the root so the layout can show the hierarchy.
	if pivot, err := c.PivotGraph(); err == nil {
		for i := range pivot.Children {
			graph.walkPivot(&pivot.Children[i], 1)
		}
	}

	// Console-side links. These are not pivots between implants: they are paths
	// the operator opened through the console into a session, and they are drawn
	// differently so the two are not confused.
	graph.addLocalLinks(c)

	graph.sortEdges()
	return graph, nil
}

// walkPivot records a pivot entry and its subtree. parentID is the session the
// entry hangs off; the console is the root when entries have no parent link.
func (g *TopologyGraph) walkPivot(e *PivotGraphEntryView, depth int) {
	if e == nil {
		return
	}
	// A pivot entry describes a session that already has its own node. Record
	// the hop so the layout can place it, without duplicating the vertex.
	for i := range g.Nodes {
		if g.Nodes[i].ID == e.SessionID {
			if depth > g.Nodes[i].Depth {
				g.Nodes[i].Depth = depth
			}
			break
		}
	}
	for i := range e.Children {
		child := &e.Children[i]
		if child.SessionID != "" && e.SessionID != "" {
			g.addEdge(TopologyEdge{
				From:  e.SessionID,
				To:    child.SessionID,
				Kind:  "pivot",
				Label: firstNonEmpty(child.Name, "pivot"),
			})
		}
		g.walkPivot(child, depth+1)
	}
}

// addLocalLinks adds the proxy and port-forward edges. These are attributes of
// the console session list rather than of the Sliver server, so they are read
// from the local managers.
func (g *TopologyGraph) addLocalLinks(c *Client) {
	if sm := c.ExistingSocks(); sm != nil {
		for _, s := range sm.List() {
			label := fmt.Sprintf("SOCKS %s", s.BindAddr)
			if s.BindPort != 0 {
				label = fmt.Sprintf("SOCKS %s:%d", s.BindAddr, s.BindPort)
			}
			g.addEdge(TopologyEdge{
				From:  topologyConsoleID,
				To:    s.SessionID,
				Kind:  "socks",
				Label: label,
			})
		}
	}

	if pf := c.ExistingPortForwards(); pf != nil {
		for _, f := range pf.List() {
			g.addEdge(TopologyEdge{
				From:  topologyConsoleID,
				To:    f.SessionID,
				Kind:  "portfwd",
				Label: fmt.Sprintf("%s:%d -> %s:%d", f.LocalAddr, f.LocalPort, f.Host, f.Port),
			})
		}
	}
}

func (g *TopologyGraph) addNode(n TopologyNode) {
	for i := range g.Nodes {
		if g.Nodes[i].ID == n.ID {
			return
		}
	}
	g.Nodes = append(g.Nodes, n)
}

// addEdge ignores links whose endpoints are not both present. A dangling edge
// would render as an arrow into empty space, and the usual cause is a pivot
// entry for a session that has since died -- the edge is stale, not missing.
func (g *TopologyGraph) addEdge(e TopologyEdge) {
	if e.From == "" || e.To == "" || e.From == e.To {
		return
	}
	if !g.hasNode(e.From) || !g.hasNode(e.To) {
		return
	}
	for _, existing := range g.Edges {
		if existing.From == e.From && existing.To == e.To && existing.Kind == e.Kind {
			return
		}
	}
	g.Edges = append(g.Edges, e)
}

func (g *TopologyGraph) hasNode(id string) bool {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return true
		}
	}
	return false
}

// sortEdges gives the list a stable order so the UI does not redraw differently
// between two identical polls.
func (g *TopologyGraph) sortEdges() {
	sort.SliceStable(g.Edges, func(i, j int) bool {
		a, b := g.Edges[i], g.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Kind < b.Kind
	})
	sort.SliceStable(g.Nodes, func(i, j int) bool {
		a, b := g.Nodes[i], g.Nodes[j]
		if a.Depth != b.Depth {
			return a.Depth < b.Depth
		}
		return a.ID < b.ID
	})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
