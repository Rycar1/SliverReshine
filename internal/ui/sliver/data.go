package sliver

import (
	"fmt"
	"strings"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
)

// SessionView is the JSON shape returned by the API.
type SessionView struct {
	ID            string `json:"ID"`
	Name          string `json:"Name"`
	UUID          string `json:"UUID"`
	Hostname      string `json:"Hostname"`
	Username      string `json:"Username"`
	UID           string `json:"UID"`
	GID           string `json:"GID"`
	PID           int32  `json:"PID"`
	OS            string `json:"OS"`
	Arch          string `json:"Arch"`
	Transport     string `json:"Transport"`
	RemoteAddress string `json:"RemoteAddress"`
	LastCheckin   string `json:"LastCheckin"`
	ActiveC2      string `json:"ActiveC2"`
	Locale        string `json:"Locale"`
	AgentVersion  string `json:"AgentVersion"`
	IsDead        bool   `json:"IsDead"`
	IsInteractive bool   `json:"IsInteractive"`
}

// BeaconView is the JSON shape for a beacon.
type BeaconView struct {
	ID            string `json:"ID"`
	Name          string `json:"Name"`
	Hostname      string `json:"Hostname"`
	Username      string `json:"Username"`
	OS            string `json:"OS"`
	Arch          string `json:"Arch"`
	Transport     string `json:"Transport"`
	RemoteAddress string `json:"RemoteAddress"`
	LastCheckin   string `json:"LastCheckin"`
	NextCheckin   string `json:"NextCheckin"`
	Interval      int64  `json:"Interval"`
	Jitter        int64  `json:"Jitter"`
	ActiveC2      string `json:"ActiveC2"`
}

// JobView is the JSON shape for a job.
type JobView struct {
	ID      uint32   `json:"ID"`
	Name    string   `json:"Name"`
	Type    string   `json:"Protocol"`
	Port    uint32   `json:"Port"`
	Domains []string `json:"Domains"`
	// CallbackHost is the address this listener was started on, recorded by the
	// console because Sliver does not report it. It is what stops a listener
	// bound to 192.168.1.9 from producing a callback address of 0.0.0.0.
	CallbackHost string `json:"callback_host,omitempty"`
	// Description is the server's human-readable line for the job. The forward
	// (bind) dialer puts its target in here, because a bind listener has no
	// local port to identify it and the target is the only thing that
	// distinguishes one from another.
	Description string `json:"Description"`
	// CanStage mirrors JobServesStage: only the HTTP family can host a file that
	// a command line can fetch. The server ships the flag so the listeners page
	// does not have to re-derive the rule, and so it cannot drift from it.
	CanStage bool `json:"CanStage"`
}

func unixTimeString(sec int64) string {
	if sec == 0 {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// Sessions lists active interactive sessions.
func (c *Client) Sessions() ([]SessionView, error) {
	ctx, cancel := c.rpcCtx(rpcQuick)
	defer cancel()
	resp, err := c.RPC.GetSessions(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]SessionView, 0, len(resp.Sessions))
	for _, s := range resp.Sessions {
		if s == nil {
			continue
		}
		out = append(out, sessionToView(s))
	}
	return out, nil
}

// Beacons lists active beacons.
func (c *Client) Beacons() ([]BeaconView, error) {
	ctx, cancel := c.rpcCtx(rpcQuick)
	defer cancel()
	resp, err := c.RPC.GetBeacons(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]BeaconView, 0, len(resp.Beacons))
	for _, b := range resp.Beacons {
		if b == nil {
			continue
		}
		out = append(out, beaconToView(b))
	}
	return out, nil
}

// Beacon fetches a single beacon by id.
func (c *Client) Beacon(id string) (*BeaconView, error) {
	ctx, cancel := c.rpcCtx(rpcQuick)
	defer cancel()
	resp, err := c.RPC.GetBeacon(ctx, &clientpb.Beacon{ID: id})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	v := beaconToView(resp)
	return &v, nil
}

// Jobs lists active listener jobs.
func (c *Client) Jobs() ([]JobView, error) {
	ctx, cancel := c.rpcCtx(rpcQuick)
	defer cancel()
	resp, err := c.RPC.GetJobs(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]JobView, 0, len(resp.Active))
	for _, j := range resp.Active {
		if j == nil {
			continue
		}
		// The server reports its own client/gRPC listener as a job
		// ("grpc/mtls", Description "client listener"). That is the
		// port sliver clients (and this UI) connect to — not an implant
		// C2 listener — so it must not show up on the listeners page.
		if j.Description == "client listener" {
			continue
		}
		domains := j.Domains
		if domains == nil {
			domains = []string{}
		}
		// The address the listener was started on, when this console started it.
		// Sliver does not report it, and without it a listener bound to a real
		// interface is indistinguishable from one bound to 0.0.0.0 -- which is
		// how a one-liner for a listener on 192.168.1.9 ended up telling the
		// implant to call back to 0.0.0.0.
		callbackHost, _ := c.listenerHost(j.ID)
		out = append(out, JobView{
			ID:           j.ID,
			Name:         j.Name,
			Type:         j.Protocol,
			Port:         j.Port,
			Domains:      domains,
			CallbackHost: callbackHost,
			Description:  j.Description,
			CanStage:     JobServesStage(j.Name),
		})
	}
	return out, nil
}

// StartListener starts a new listener (mTLS, HTTP(S), DNS, WireGuard).
// StartListener starts a listener.
//
// website and domain are only meaningful for http/https: the first is what lets
// the listener serve files published by WebDelivery, and the second is what the
// implant's callback URIs are built from. Both were missing, which made a
// UI-created HTTP listener incapable of serving a stage -- the file was
// published, the listener reported success, and every fetch returned 404.
func (c *Client) StartListener(jobType, addr string, port uint32, tls bool, website, domain, callbackHost string) (uint32, error) {
	// The address an implant built for this listener should call back to.
	//
	// Sliver's Job does not carry the bind address, so unless it is recorded
	// here the console cannot tell a listener on 192.168.1.9 from one on
	// 0.0.0.0, and every one-liner falls back to the wildcard. An explicit
	// callback address wins; otherwise the bind address is used as-is, wildcard
	// included, so the resolver can say "bound to 0.0.0.0" instead of "unknown".
	//
	// A callback address the operator typed is validated here rather than at
	// build time, because a typo should be refused while they are looking at the
	// form, not two minutes later by a one-liner that quietly ignored it.
	recorded := strings.TrimSpace(callbackHost)
	if recorded != "" {
		if err := validateHost(recorded); err != nil {
			return 0, err
		}
	} else {
		recorded = strings.TrimSpace(addr)
	}

	// Reuse a listener that already owns the port.
	//
	// A second bind on the same port fails at the OS level, and the error names
	// neither the listener that holds it nor the fact that the request was
	// already satisfied -- so a console that posts "start" twice (a double
	// click, or a retry after a slow response) reports a bind failure for a
	// listener that is running. When a listener of the same kind already holds
	// the port, hand back its ID: the operator asked for a listener there, and
	// one is there.
	if id, ok := c.existingListener(listenerJobName(jobType, tls), port); ok {
		if recorded != "" {
			c.rememberListenerHost(id, recorded)
		}
		return id, nil
	}
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	switch jobType {
	case "mtls":
		resp, err := c.RPC.StartMTLSListener(ctx, &clientpb.MTLSListenerReq{
			Host: addr,
			Port: port,
		})
		if err != nil {
			return 0, err
		}
		c.rememberListenerHost(resp.JobID, recorded)
		return resp.JobID, nil
	case "http", "https":
		req := &clientpb.HTTPListenerReq{
			Host: addr,
			Port: port,
			// Website is what makes this listener able to serve hosted content.
			// Without it Sliver accepts the listener and then answers every
			// request for a published file with 404 -- which is how a stage
			// delivery ended up pointing at a URL that never worked while the
			// API reported success at every step.
			Website: website,
			Domain:  domain,
		}
		if jobType == "https" || tls {
			req.Secure = true
		}
		resp, err := c.RPC.StartHTTPListener(ctx, req)
		if err != nil {
			return 0, err
		}
		// Recorded so a later one-liner publishes to the same website this
		// listener serves. Without it the stage goes to whatever default the
		// delivery code picks, the listener cannot see it, and the fetch 404s
		// with every step reporting success.
		c.rememberListenerSite(resp.JobID, website)
		c.rememberListenerHost(resp.JobID, recorded)
		return resp.JobID, nil
	case "dns":
		// The port has to be sent. This branch used to pass only Domains, so the
		// server fell back to its own default and every DNS listener bound port
		// 53 regardless of what the operator asked for -- while the API answered
		// {"success":true}. A listener on the wrong port does not error; it just
		// never receives anything, which is the worst way to fail.
		resp, err := c.RPC.StartDNSListener(ctx, &clientpb.DNSListenerReq{
			Domains: []string{addr},
			Host:    addr,
			Port:    port,
		})
		if err != nil {
			return 0, err
		}
		c.rememberListenerHost(resp.JobID, recorded)
		return resp.JobID, nil
	case "wireguard":
		resp, err := c.RPC.StartWGListener(ctx, &clientpb.WGListenerReq{
			Host: addr,
			Port: port,
		})
		if err != nil {
			return 0, err
		}
		c.rememberListenerHost(resp.JobID, recorded)
		return resp.JobID, nil
	default:
		return 0, fmt.Errorf("unsupported listener type %q", jobType)
	}
}

// StopJob stops a job by ID.
func (c *Client) StopJob(jobID uint32) error {
	ctx, cancel := c.rpcCtx(rpcQuick)
	defer cancel()
	_, err := c.RPC.KillJob(ctx, &clientpb.KillJobReq{ID: jobID})
	return err
}

// listenerJobName maps the console's listener type to the job name Sliver
// registers for it.
//
// A job carries the protocol ("tcp"/"udp") rather than the listener kind, so
// the name is what distinguishes an HTTP listener from an mTLS one, and the two
// are not interchangeable even though both report "tcp". The http/https split
// follows the request: the server names the job from the Secure flag, which the
// switch below sets from the same pair of inputs.
func listenerJobName(jobType string, tls bool) string {
	switch jobType {
	case "mtls":
		return "mtls"
	case "http":
		if tls {
			return "https"
		}
		return "http"
	case "https":
		return "https"
	case "dns":
		return "dns"
	case "wireguard":
		return "wg"
	}
	return jobType
}

// existingListener returns the ID of a listener of the given kind already bound
// to port, if there is one.
//
// The job list does not carry the bind address, so a match is kind and port --
// which is exactly the pair the server refuses to duplicate. An unknown port
// state is not a reuse: the start attempt is left to report the conflict.
func (c *Client) existingListener(name string, port uint32) (uint32, bool) {
	if name == "" || port == 0 {
		return 0, false
	}
	jobs, err := c.Jobs()
	if err != nil {
		return 0, false
	}
	for _, j := range jobs {
		if j.Port == port && strings.EqualFold(j.Name, name) {
			return j.ID, true
		}
	}
	return 0, false
}

// EventView is the JSON shape for a server event.
type EventView struct {
	Type    string         `json:"Type"`
	Err     string         `json:"Err"`
	Session *SessionView   `json:"Session,omitempty"`
	Beacon  *BeaconView    `json:"Beacon,omitempty"`
	Job     *JobView       `json:"Job,omitempty"`
	Data    map[string]any `json:"Data"`
}

func eventToView(e *clientpb.Event) EventView {
	ev := EventView{
		Type: e.EventType,
		Err:  e.Err,
		Data: map[string]any{},
	}
	if e.Session != nil {
		sv := sessionToView(e.Session)
		ev.Session = &sv
	}
	if e.Job != nil {
		domains := e.Job.Domains
		if domains == nil {
			domains = []string{}
		}
		ev.Job = &JobView{ID: e.Job.ID, Name: e.Job.Name, Type: e.Job.Protocol, Port: e.Job.Port, Domains: domains}
	}
	return ev
}

// Events reads from the server event stream until an error or timeout.
// Events collects server events by opening a streaming RPC and reading
// until the context deadline. The Sliver Events stream only sends NEW
// events from the moment the stream is opened — there is no replay of
// historical events. A short timeout (3s) caused most polls to return
// empty because events rarely arrive within that window. We use 10s
// to give events a reasonable chance to arrive.
func (c *Client) Events() ([]EventView, error) {
	ctx, cancel := c.rpcCtx(rpcQuick)
	defer cancel()
	stream, err := c.RPC.Events(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]EventView, 0, 50)
	for {
		e, err := stream.Recv()
		if err != nil {
			break
		}
		if e != nil {
			out = append(out, eventToView(e))
		}
	}
	return out, nil
}

func sessionToView(s *clientpb.Session) SessionView {
	return SessionView{
		ID:            s.ID,
		Name:          s.Name,
		UUID:          s.UUID,
		Hostname:      s.Hostname,
		Username:      s.Username,
		UID:           s.UID,
		GID:           s.GID,
		PID:           s.PID,
		OS:            s.OS,
		Arch:          s.Arch,
		Transport:     s.Transport,
		RemoteAddress: s.RemoteAddress,
		LastCheckin:   unixTimeString(s.LastCheckin),
		ActiveC2:      s.ActiveC2,
		// Sliver v1.15.16 的 clientpb.Session 无 Locale 字段，留空
		Locale:       "",
		AgentVersion: s.Version,
		IsDead:       s.IsDead,
		// Sliver v1.15.16 无 IsInteractive 字段，用 !IsDead 作为近似
		IsInteractive: !s.IsDead,
	}
}

func beaconToView(b *clientpb.Beacon) BeaconView {
	return BeaconView{
		ID:            b.ID,
		Name:          b.Name,
		Hostname:      b.Hostname,
		Username:      b.Username,
		OS:            b.OS,
		Arch:          b.Arch,
		Transport:     b.Transport,
		RemoteAddress: b.RemoteAddress,
		LastCheckin:   unixTimeString(b.LastCheckin),
		NextCheckin:   unixTimeString(b.NextCheckin),
		Interval:      b.Interval,
		Jitter:        b.Jitter,
		ActiveC2:      b.ActiveC2,
	}
}
