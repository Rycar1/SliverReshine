package sliver

import (
	"fmt"
	"strings"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// GenerateRequest is the JSON body for implant generation.
type GenerateRequest struct {
	Name   string `json:"name"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Format string `json:"format"`
	C2     []struct {
		Address  string `json:"address"`
		Protocol string `json:"protocol"`
	} `json:"c2"`
	Interval  int64 `json:"interval"`
	Jitter    int64 `json:"jitter"`
	MaxErrors int32 `json:"maxConnectionErrors"`
	IsBeacon  bool  `json:"is_beacon"`
	Debug     bool  `json:"debug"`
	Evasion   bool  `json:"evasion"`
	Obfuscate bool  `json:"obfuscate"`
}

// ImplantConfigView mirrors the relevant fields of clientpb.ImplantConfig.
type ImplantConfigView struct {
	Name      string `json:"Name"`
	OS        string `json:"OS"`
	Arch      string `json:"Arch"`
	Format    string `json:"Format"`
	Interval  int64  `json:"Interval"`
	Jitter    int64  `json:"Jitter"`
	Obfuscate bool   `json:"Obfuscate"`
	Debug     bool   `json:"Debug"`
	Evasion   bool   `json:"Evasion"`
	MaxErrors uint32 `json:"MaxConnectionErrors"`
	IsBeacon  bool   `json:"IsBeacon"`
	BeaconInt int64  `json:"BeaconInterval"`
	BeaconJit int64  `json:"BeaconJitter"`
	C2        []struct {
		URL string `json:"URL"`
	} `json:"C2"`
}

// ImplantBuildView is the JSON shape for a stored implant build.
type ImplantBuildView struct {
	Name          string             `json:"Name"`
	ImplantConfig *ImplantConfigView `json:"ImplantConfig"`
	OS            string             `json:"OS"`
	Arch          string             `json:"Arch"`
}

func configToView(c *clientpb.ImplantConfig, name string) *ImplantConfigView {
	if c == nil {
		return nil
	}
	// C2 list — start from nil, append actual URLs only
	var c2 []struct {
		URL string `json:"URL"`
	}
	for _, u := range c.C2 {
		if u == nil {
			continue
		}
		c2 = append(c2, struct {
			URL string `json:"URL"`
		}{URL: u.URL})
	}
	return &ImplantConfigView{
		Name:      name,
		OS:        c.GOOS,
		Arch:      c.GOARCH,
		Format:    c.Format.String(),
		Interval:  c.BeaconInterval,
		Jitter:    c.BeaconJitter,
		Obfuscate: c.ObfuscateSymbols,
		Debug:     c.Debug,
		Evasion:   c.Evasion,
		MaxErrors: c.MaxConnectionErrors,
		IsBeacon:  c.IsBeacon,
		BeaconInt: c.BeaconInterval,
		BeaconJit: c.BeaconJitter,
		C2:        c2,
	}
}

// ImplantBuilds lists stored implant builds.
func (c *Client) ImplantBuilds() ([]ImplantBuildView, error) {
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	resp, err := c.RPC.ImplantBuilds(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]ImplantBuildView, 0, len(resp.Configs))
	for name, cfg := range resp.Configs {
		if cfg == nil {
			continue
		}
		out = append(out, ImplantBuildView{
			Name:          name,
			ImplantConfig: configToView(cfg, name),
			OS:            cfg.GOOS,
			Arch:          cfg.GOARCH,
		})
	}
	return out, nil
}

func buildC2URL(address, protocol string) string {
	url := address
	if !strings.Contains(url, "://") {
		if protocol == "" {
			protocol = "mtls"
		}
		switch protocol {
		case "mtls":
			url = "mtls://" + url
		case "http":
			url = "http://" + url
		case "https":
			url = "https://" + url
		case "dns":
			url = "dns://" + url
		case "wireguard":
			url = "wg://" + url
		case "bind", "forward":
			// Forward (bind) sessions: the implant listens and the C2 dials in.
			// The port in the address is the port the implant binds on the
			// target, so it must be explicit — the bind transport refuses a URL
			// without one rather than guessing.
			url = "bind://" + url
		case "tcppivot", "tcp-pivot", "tcp_pivot":
			// Pivot transport: this implant connects to a pivot listener running
			// on another implant, which relays it to the server. The address is
			// the *parent* implant's listen address, so it must be explicit.
			//
			// The scheme is "tcppivot", with no hyphen — that is the string the
			// implant's transport dispatch matches on. The generate page and the
			// sliver CLI both call it "tcp-pivot", so both spellings are accepted
			// here; emitting the CLI's spelling would produce a URL no implant
			// recognises and the build would silently fall back to mTLS.
			url = "tcppivot://" + url
		default:
			url = "mtls://" + url
		}
	}
	return url
}

// ImplantProfileView is the JSON shape of an implant profile.
type ImplantProfileView struct {
	Name   string             `json:"Name"`
	Config *ImplantConfigView `json:"Config"`
}

// ImplantProfiles lists saved implant profiles.
func (c *Client) ImplantProfiles() ([]ImplantProfileView, error) {
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	resp, err := c.RPC.ImplantProfiles(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]ImplantProfileView, 0, len(resp.Profiles))
	for _, p := range resp.Profiles {
		if p == nil {
			continue
		}
		out = append(out, ImplantProfileView{Name: p.Name, Config: configToView(p.Config, p.Name)})
	}
	return out, nil
}

// SaveImplantProfile saves (or updates) an implant profile.
func (c *Client) SaveImplantProfile(name string, req *GenerateRequest, isBeacon bool) error {
	if name == "" {
		return fmt.Errorf("profile name is required")
	}
	cfg := buildImplantConfig(req, isBeacon)
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	_, err := c.RPC.SaveImplantProfile(ctx, &clientpb.ImplantProfile{Name: name, Config: cfg})
	return err
}

// DeleteImplantProfile removes a saved implant profile.
//
// Guard: sliver-server v1.15.16's DeleteImplantProfile RPC panics (nil
// dereference in db.ProfileByName) when the named profile does not exist,
// taking the whole server down. Verify the profile exists first and never
// forward the delete RPC for an unknown name.
func (c *Client) DeleteImplantProfile(name string) error {
	profiles, err := c.ImplantProfiles()
	if err != nil {
		return err
	}
	found := false
	for _, p := range profiles {
		if p.Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("implant profile %q not found", name)
	}
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	_, err = c.RPC.DeleteImplantProfile(ctx, &clientpb.DeleteReq{Name: name})
	return err
}

// buildImplantConfig converts a GenerateRequest into a clientpb.ImplantConfig.
// outputFormats maps the console's format names onto Sliver's enum.
//
// KillSession sends a kill command to the implant.
// Sliver v1.15.16 没有 KillSession RPC 方法 — 服务端在 implant
// 断开后自动清理 session 记录，无需显式调用。
func (c *Client) KillSession(id string) error {
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	_, err := c.RPC.Kill(ctx, &sliverpb.KillReq{
		Force: true,
		Request: &commonpb.Request{
			SessionID: id,
		},
	})
	return err
}

// CompilerTargetView is the JSON shape of a compiler build target.
type CompilerTargetView struct {
	GOOS   string `json:"GOOS"`
	GOARCH string `json:"GOARCH"`
	Format string `json:"Format"`
}

// CrossCompilerView is the JSON shape of an installed cross-compiler.
type CrossCompilerView struct {
	TargetGOOS   string `json:"TargetGOOS"`
	TargetGOARCH string `json:"TargetGOARCH"`
	CCPath       string `json:"CCPath"`
	CXXPath      string `json:"CXXPath"`
}

// CompilerView is the JSON shape returned by GetCompiler.
type CompilerView struct {
	GOOS           string               `json:"GOOS"`
	GOARCH         string               `json:"GOARCH"`
	Targets        []CompilerTargetView `json:"Targets"`
	CrossCompilers []CrossCompilerView  `json:"CrossCompilers"`
}

// CompilerInfo fetches the Sliver server's compiler targets and cross-compilers.
func (c *Client) CompilerInfo() (*CompilerView, error) {
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	resp, err := c.RPC.GetCompiler(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	v := &CompilerView{GOOS: resp.GOOS, GOARCH: resp.GOARCH}
	for _, t := range resp.Targets {
		if t == nil {
			continue
		}
		v.Targets = append(v.Targets, CompilerTargetView{
			GOOS:   t.GOOS,
			GOARCH: t.GOARCH,
			Format: t.Format.String(),
		})
	}
	for _, cc := range resp.CrossCompilers {
		if cc == nil {
			continue
		}
		v.CrossCompilers = append(v.CrossCompilers, CrossCompilerView{
			TargetGOOS:   cc.TargetGOOS,
			TargetGOARCH: cc.TargetGOARCH,
			CCPath:       cc.CCPath,
			CXXPath:      cc.CXXPath,
		})
	}
	return v, nil
}
