package sliver

import (
	"errors"
	"strconv"
	"strings"

	"github.com/bishopfox/sliver/protobuf/clientpb"
)

// BindListenerView is the JSON shape of a forward (bind) listener.
type BindListenerView struct {
	// JobID identifies the dialer in the server's job list, so it can be
	// stopped the same way a reverse listener is.
	JobID uint32 `json:"jobId"`
	// Address is what the dialer targets, "host:port".
	Address string `json:"address"`
	Host    string `json:"host"`
	Port    uint32 `json:"port"`
	// Direction is always "forward" here. It is carried explicitly so the UI
	// does not have to infer the direction from the listener type, which is the
	// mistake that makes a bind listener look like a broken reverse one.
	Direction string `json:"direction"`
}

// DialBind starts a forward listener.
//
// A forward listener inverts the direction of every other transport: instead of
// waiting for the implant to call home, the C2 dials a port the implant is
// listening on. The session it produces is the same mutual-TLS session a
// reverse listener produces — the implant is simply the TLS server and the C2
// the client — so it is encrypted and mutually authenticated identically.
//
// The call returns as soon as the dialer is running. The implant may not have
// reached its listen path yet, and the server retries until it does; the session
// appears in the session list when it lands, which is the thing to watch, not
// this call's return value.
func (c *Client) DialBind(host string, port uint32) (BindListenerView, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return BindListenerView{}, errors.New("host is required")
	}
	if port == 0 || port > 65535 {
		return BindListenerView{}, errors.New("port must be between 1 and 65535")
	}

	ctx, cancel := rpcCtx(opTimeout)
	defer cancel()

	resp, err := c.RPC.DialBind(ctx, &clientpb.DialBindReq{Host: host, Port: port})
	if err != nil {
		return BindListenerView{}, err
	}
	if resp.GetResponse().GetErr() != "" {
		return BindListenerView{}, errors.New(resp.GetResponse().GetErr())
	}

	address := resp.GetAddress()
	if address == "" {
		address = host + ":" + itoa32(port)
	}
	return BindListenerView{
		JobID:     resp.GetJobID(),
		Address:   address,
		Host:      host,
		Port:      port,
		Direction: "forward",
	}, nil
}

// BindListeners lists the running forward dialers.
//
// There is no dedicated RPC for this: the server tracks them as jobs, and the
// job list is the single source of truth. Filtering here rather than on the
// server keeps the server-side change to just the one new RPC.
func (c *Client) BindListeners() ([]BindListenerView, error) {
	jobs, err := c.Jobs()
	if err != nil {
		return nil, err
	}

	out := make([]BindListenerView, 0, len(jobs))
	for _, job := range jobs {
		if job.Name != "bind" {
			continue
		}
		host, port := splitHostPort(job.Description, job.Port)
		out = append(out, BindListenerView{
			JobID:     job.ID,
			Address:   job.Description,
			Host:      host,
			Port:      port,
			Direction: "forward",
		})
	}
	return out, nil
}

// StopBindListener stops a forward dialer.
//
// It gives up on reaching the target; it does not disconnect a session that is
// already established. Once an implant has connected, the session is the
// operator's and is closed with the usual session controls.
func (c *Client) StopBindListener(jobID uint32) error {
	return c.StopJob(jobID)
}

// splitHostPort recovers the host and port from a bind job's description.
//
// The server records the target as the job description, because a forward
// dialer has no local port to identify it and the target is the only thing that
// distinguishes one from another. The console splits it back apart so the API
// reports a populated host rather than an empty string next to a correct port —
// the port also arrives as its own job field, so a parse failure falls back to
// that instead of dropping it.
//
// The split is on the LAST colon: an IPv6 target is written [::1]:4444 by the
// server, and splitting on the first colon would return "[".
func splitHostPort(addr string, fallbackPort uint32) (string, uint32) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, fallbackPort
	}
	host := strings.Trim(addr[:i], "[]")
	port, err := strconv.ParseUint(addr[i+1:], 10, 32)
	if err != nil || port == 0 || port > 65535 {
		return host, fallbackPort
	}
	return host, uint32(port)
}

// itoa32 renders a port without pulling strconv into this file's imports for a
// single call.
func itoa32(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
