package sliver

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
)

// ---------------------------------------------------------------------------
// HTTP C2 profiles
//
// A profile redefines what the implant's HTTP traffic looks like on the wire
// (URIs, headers, cookies, body transforms). Editing profiles from the console
// is what lets an operator reshape C2 traffic per engagement instead of being
// stuck with the defaults baked into the implant.
// ---------------------------------------------------------------------------

// C2ProfileView is a lightweight summary of a stored HTTP C2 profile.
type C2ProfileView struct {
	ID        string   `json:"ID"`
	Name      string   `json:"Name"`
	Created   int64    `json:"Created"`
	ServerURI []string `json:"ServerURI,omitempty"`
	UserAgent string   `json:"UserAgent,omitempty"`
}

// HTTPC2Profiles lists stored profiles.
func (c *Client) HTTPC2Profiles() ([]C2ProfileView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.GetHTTPC2Profiles(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]C2ProfileView, 0, len(resp.Configs))
	for _, p := range resp.Configs {
		v := C2ProfileView{ID: p.ID, Name: p.Name, Created: p.Created}
		if p.ImplantConfig != nil {
			v.UserAgent = p.ImplantConfig.UserAgent
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// HTTPC2Profile fetches one profile in full.
func (c *Client) HTTPC2Profile(name string) (*clientpb.HTTPC2Config, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.GetHTTPC2ProfileByName(ctx, &clientpb.C2ProfileReq{Name: name})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// SaveHTTPC2Profile stores a profile, optionally overwriting an existing one.
//
// The two nested messages are checked here, not just the outer one. Sliver's
// SaveHTTPC2Profile validates the config with CheckHTTPC2ConfigErrors, whose very
// first statement is `len(config.ServerConfig.Cookies)` -- an unconditional
// dereference. A request that supplies a profile without ServerConfig or
// ImplantConfig therefore panics inside the server, and the server is a child
// process (internal/launch starts it with exec.Command) that recovers only to
// call os.Exit(99). One malformed request from an authenticated operator takes
// down every session, beacon and listener on the engagement.
//
// The console is the only place that can prevent this: the check has to happen
// before the RPC is sent.
func (c *Client) SaveHTTPC2Profile(cfg *clientpb.HTTPC2Config, overwrite bool) error {
	if cfg == nil {
		return errors.New("no profile supplied")
	}
	if cfg.ServerConfig == nil {
		return errors.New("profile has no serverConfig: the server dereferences it before validating")
	}
	if cfg.ImplantConfig == nil {
		return errors.New("profile has no implantConfig: the server dereferences it before validating")
	}
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	_, err := c.RPC.SaveHTTPC2Profile(ctx, &clientpb.HTTPC2ConfigReq{
		Overwrite: overwrite,
		C2Config:  cfg,
	})
	return err
}

// ---------------------------------------------------------------------------
// Traffic encoders
//
// Traffic encoders are WASM modules that transform every C2 message. They are
// the general mechanism behind custom encryption and protocol mimicry.
// ---------------------------------------------------------------------------

// TrafficEncoderView summarises a registered encoder.
type TrafficEncoderView struct {
	ID     uint64 `json:"ID"`
	TestID string `json:"TestID"`
}

// TrafficEncoders lists registered traffic encoders.
func (c *Client) TrafficEncoders() ([]string, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.TrafficEncoderMap(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(resp.Encoders))
	for name := range resp.Encoders {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// TrafficEncodersWithID lists encoders together with their IDs.
func (c *Client) TrafficEncodersWithID() (map[string]uint64, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.TrafficEncoderMap(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]uint64, len(resp.Encoders))
	for name, e := range resp.Encoders {
		out[name] = e.GetID()
	}
	return out, nil
}

// TrafficEncoderAdd uploads a WASM encoder and runs the server's conformance
// tests against it. The returned report is what tells the operator whether the
// encoder is safe to use on a live implant.
func (c *Client) TrafficEncoderAdd(name string, wasm []byte, skipTests bool) (*TrafficEncoderReport, error) {
	if name == "" {
		return nil, errors.New("encoder name is required")
	}
	if len(wasm) == 0 {
		return nil, errors.New("encoder wasm is empty")
	}
	ctx, cancel := c.rpcCtx(5 * opTimeout)
	defer cancel()
	resp, err := c.RPC.TrafficEncoderAdd(ctx, &clientpb.TrafficEncoder{
		Wasm:      &commonpb.File{Name: name, Data: wasm},
		SkipTests: skipTests,
	})
	if err != nil {
		return nil, err
	}
	rep := &TrafficEncoderReport{
		TotalTests:    int32(resp.TotalTests),
		TotalDuration: resp.TotalDuration,
		EncoderID:     resp.Encoder.GetID(),
	}
	for _, t := range resp.Tests {
		rep.Tests = append(rep.Tests, TrafficEncoderTestView{
			Name:      t.Name,
			Completed: t.Completed,
			Success:   t.Success,
			Duration:  t.Duration,
			Err:       t.Err,
		})
	}
	return rep, nil
}

// TrafficEncoderTestView is one conformance test result.
type TrafficEncoderTestView struct {
	Name      string `json:"Name"`
	Completed bool   `json:"Completed"`
	Success   bool   `json:"Success"`
	Duration  int64  `json:"Duration"`
	Err       string `json:"Err,omitempty"`
}

// TrafficEncoderReport is the JSON shape of the encoder test run.
type TrafficEncoderReport struct {
	EncoderID     uint64                   `json:"EncoderID"`
	TotalTests    int32                    `json:"TotalTests"`
	TotalDuration int64                    `json:"TotalDuration"`
	Tests         []TrafficEncoderTestView `json:"Tests"`
}

// TrafficEncoderRm removes a registered encoder.
func (c *Client) TrafficEncoderRm(name string) error {
	ids, err := c.TrafficEncodersWithID()
	if err != nil {
		return err
	}
	id, ok := ids[name]
	if !ok {
		return fmt.Errorf("no traffic encoder named %q", name)
	}
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	_, err = c.RPC.TrafficEncoderRm(ctx, &clientpb.TrafficEncoder{ID: id})
	return err
}

// ---------------------------------------------------------------------------
// Shellcode encoders
//
// Encoders rewrite shellcode so it survives a given loader's constraints —
// escaping bad characters and applying an encoding chain the stager can decode.
// ---------------------------------------------------------------------------

// ShellcodeEncoderView describes an available encoder chain.
type ShellcodeEncoderView struct {
	Arch     string   `json:"Arch"`
	Encoders []string `json:"Encoders"`
}

// ShellcodeEncoders lists encoder chains grouped by architecture.
func (c *Client) ShellcodeEncoders() ([]ShellcodeEncoderView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.ShellcodeEncoderMap(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]ShellcodeEncoderView, 0, len(resp.Encoders))
	for arch, m := range resp.Encoders {
		v := ShellcodeEncoderView{Arch: arch}
		for name := range m.Encoders {
			v.Encoders = append(v.Encoders, name)
		}
		sort.Strings(v.Encoders)
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Arch < out[j].Arch })
	return out, nil
}

// ShellcodeEncode runs an encoder over raw shellcode. Returns base64-ready bytes.
func (c *Client) ShellcodeEncode(encoder, arch string, data []byte, iterations uint32, badChars []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("no shellcode supplied")
	}
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	enc, err := shellcodeEncoderFromString(encoder)
	if err != nil {
		return nil, err
	}

	resp, err := c.RPC.ShellcodeEncoder(ctx, &clientpb.ShellcodeEncodeReq{
		Encoder:      enc,
		Architecture: arch,
		Iterations:   iterations,
		BadChars:     badChars,
		Data:         data,
	})
	if err != nil {
		return nil, err
	}
	if resp.GetResponse().GetErr() != "" {
		return nil, errors.New(resp.GetResponse().GetErr())
	}
	return resp.Data, nil
}

// ShellcodeEncoder is one encoder the server offers.
//
// The advertised Names are lowercase ("xor", "shikata_ga_nai"); the protobuf
// enum values are uppercase ("XOR", "SHIKATA_GA_NAI"). These are two different
// name spaces and they used to be confused -- see shellcodeEncoderFromString.
type ShellcodeEncoder struct {
	Enum  string
	Names []string
}

// knownShellcodeEncoders maps each protobuf enum onto the names the server
// advertises for it.
//
// It is a hardcoded table because the authoritative mapping is the server's own
// shellcodeEncoderEnums, which is unexported and unreachable from here. The
// console cannot import it, so it is mirrored -- with a test that fails if the
// two ever disagree about a name the console accepts.
var knownShellcodeEncoders = []ShellcodeEncoder{
	{Enum: "SHIKATA_GA_NAI", Names: []string{"shikata_ga_nai"}},
	{Enum: "XOR", Names: []string{"xor"}},
	{Enum: "XOR_DYNAMIC", Names: []string{"xor_dynamic"}},
}

// shellcodeEncoderFromString resolves an operator-supplied encoder name to the
// enum the RPC expects.
//
// This used to look the name up in clientpb.ShellcodeEncoder_value, whose keys
// are the ENUM names. Every lowercase name the console itself advertises -- and
// therefore every name the UI offers and its placeholder suggests -- missed that
// map and fell through to the zero value, ShellcodeEncoder_NONE. The server's
// response to NONE is to return the input unchanged:
//
//	if req.Encoder == clientpb.ShellcodeEncoder_NONE {
//	    resp.Data = req.Data
//	    return resp, nil
//	}
//
// So the operator got HTTP 200, a success toast, and their own unencoded bytes
// back, then handed raw shellcode to a loader. Nothing reported a failure.
//
// Both spellings are accepted here, and an unrecognised name is an error rather
// than a silent NONE. Falling back is exactly what made the old behaviour
// invisible.
func shellcodeEncoderFromString(s string) (clientpb.ShellcodeEncoder, error) {
	name := strings.TrimSpace(s)
	if name == "" {
		return 0, errors.New("no encoder named")
	}

	for _, e := range knownShellcodeEncoders {
		if strings.EqualFold(name, e.Enum) {
			return clientpb.ShellcodeEncoder(clientpb.ShellcodeEncoder_value[e.Enum]), nil
		}
		for _, alias := range e.Names {
			if strings.EqualFold(name, alias) {
				return clientpb.ShellcodeEncoder(clientpb.ShellcodeEncoder_value[e.Enum]), nil
			}
		}
	}

	valid := make([]string, 0, len(knownShellcodeEncoders))
	for _, e := range knownShellcodeEncoders {
		valid = append(valid, e.Names[0])
	}
	sort.Strings(valid)
	return 0, fmt.Errorf("unknown encoder %q; the server offers %s",
		s, strings.Join(valid, ", "))
}
