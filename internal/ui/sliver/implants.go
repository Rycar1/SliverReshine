package sliver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err = c.RPC.DeleteImplantProfile(ctx, &clientpb.DeleteReq{Name: name})
	return err
}

// buildImplantConfig converts a GenerateRequest into a clientpb.ImplantConfig.
// outputFormats maps the console's format names onto Sliver's enum.
//
// A map rather than a switch, so that "is this a format I know?" and "which
// enum is it?" are the same question. The switch it replaces had no default,
// which meant an unrecognised format silently became EXECUTABLE -- the request
// was answered with a payload, just not the one that was asked for.
var outputFormats = map[string]clientpb.OutputFormat{
	"exe":        clientpb.OutputFormat_EXECUTABLE,
	"executable": clientpb.OutputFormat_EXECUTABLE,
	"service":    clientpb.OutputFormat_SERVICE,
	"shellcode":  clientpb.OutputFormat_SHELLCODE,
	"shared":     clientpb.OutputFormat_SHARED_LIB,
}

// buildTargets are the operating systems and architectures the console offers.
//
// Held here rather than only in the frontend because the API is reachable
// directly, and a request that names a platform the toolchain cannot build
// should fail with that fact rather than a compiler message from three layers
// down.
var buildTargets = map[string][]string{
	"windows": {"amd64", "386", "arm64"},
	"linux":   {"amd64", "386", "arm64", "arm"},
	"darwin":  {"amd64", "arm64"},
	"freebsd": {"amd64", "386", "arm64", "arm"},
}

// shellcodeArches records which architectures each OS can produce shellcode for.
//
// Sliver enforces this per platform in its own builders, with three different
// rules, and none of them are visible from the console's side:
//
//	windows  amd64, 386        (arms64 and arm are refused)
//	linux    amd64, arm64      (386 and arm are refused)
//	darwin   arm64 only        (amd64 is refused)
//	freebsd  none
//
// Without this the console offered "windows/arm64 shellcode" and the operator
// got "windows shellcode format is only supported for amd64 and 386
// architectures" -- after waiting for a build that was never possible. The
// frontend narrows its dropdown from the same table so the combination cannot
// be chosen at all.
var shellcodeArches = map[string][]string{
	"windows": {"amd64", "386"},
	"linux":   {"amd64", "arm64"},
	"darwin":  {"arm64"},
	"freebsd": {},
}

// validateBuildRequest reports the first thing about a request that cannot be
// built, or an empty string when it is fine.
//
// The message names the accepted values. An operator who typed "plan9" or
// "rom" needs to know what the alternatives are, and "invalid target" alone
// sends them to the source.
func validateBuildRequest(os, arch, format string) string {
	if os != "" {
		arches, ok := buildTargets[strings.ToLower(os)]
		if !ok {
			return fmt.Sprintf("unsupported os %q; supported: %s", os, sortedKeys(buildTargets))
		}
		if arch != "" {
			found := false
			for _, a := range arches {
				if strings.EqualFold(a, arch) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Sprintf("unsupported arch %q for %s; supported: %s", arch, os, strings.Join(arches, ", "))
			}
		}
	}
	if format != "" {
		if _, ok := outputFormats[strings.ToLower(format)]; !ok {
			return fmt.Sprintf("unsupported format %q; supported: %s", format, sortedKeys(outputFormats))
		}
	}

	// The format and the target have to be checked together: shellcode is
	// offered for every OS but only builds on some architectures of each.
	if strings.EqualFold(format, "shellcode") && os != "" && arch != "" {
		arches, ok := shellcodeArches[strings.ToLower(os)]
		if ok {
			allowed := false
			for _, a := range arches {
				if strings.EqualFold(a, arch) {
					allowed = true
					break
				}
			}
			if !allowed {
				if len(arches) == 0 {
					return fmt.Sprintf("%s does not support the shellcode format; use exe or shared", os)
				}
				return fmt.Sprintf("%s shellcode is not supported on %s; supported: %s",
					os, arch, strings.Join(arches, ", "))
			}
		}
	}
	return ""
}

// sortedKeys renders a map's keys for a message, in a stable order so the same
// mistake produces the same sentence every time.
func sortedKeys[V any](m map[string]V) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func buildImplantConfig(req *GenerateRequest, isBeacon bool) *clientpb.ImplantConfig {
	if req.OS == "" {
		req.OS = "windows"
	}
	if req.Arch == "" {
		req.Arch = "amd64"
	}
	if req.Interval == 0 {
		req.Interval = 60
	}

	// A validated request cannot reach the default below with an unknown name,
	// so the EXECUTABLE fallback is reached only when the caller left the field
	// empty -- which is a request for the default, not a typo.
	format, ok := outputFormats[strings.ToLower(req.Format)]
	if !ok {
		format = clientpb.OutputFormat_EXECUTABLE
	}

	cfg := &clientpb.ImplantConfig{
		GOOS:               req.OS,
		GOARCH:             req.Arch,
		Format:             format,
		Debug:              req.Debug,
		Evasion:            req.Evasion,
		ObfuscateSymbols:   req.Obfuscate,
		IsBeacon:           isBeacon,
		BeaconInterval:     req.Interval,
		BeaconJitter:       req.Jitter,
		HTTPC2ConfigName:   "default",
		ConnectionStrategy: "sequential",
	}
	if isBeacon {
		cfg.BeaconInterval = req.Interval
		cfg.BeaconJitter = req.Jitter
	} else {
		cfg.ReconnectInterval = req.Interval
	}
	if req.MaxErrors != 0 {
		cfg.MaxConnectionErrors = uint32(req.MaxErrors)
	} else {
		cfg.MaxConnectionErrors = 1000
	}

	for _, c2 := range req.C2 {
		if c2.Address == "" {
			continue
		}
		url := buildC2URL(c2.Address, c2.Protocol)
		cfg.C2 = append(cfg.C2, &clientpb.ImplantC2{URL: url})
		switch {
		case strings.HasPrefix(url, "mtls://"):
			cfg.IncludeMTLS = true
		case strings.HasPrefix(url, "http://"), strings.HasPrefix(url, "https://"):
			cfg.IncludeHTTP = true
		case strings.HasPrefix(url, "dns://"):
			cfg.IncludeDNS = true
		case strings.HasPrefix(url, "wg://"):
			cfg.IncludeWG = true
		}
	}
	if len(cfg.C2) == 0 {
		cfg.C2 = append(cfg.C2, &clientpb.ImplantC2{URL: "mtls://127.0.0.1:8888"})
		cfg.IncludeMTLS = true
	}
	return cfg
}

// implantFileExtension returns the file extension for a built implant,
// mirroring sliver's server/generate naming rules. The stored config.Extension
// is often empty, so the extension is derived from the output format and OS.
func implantFileExtension(cfg *clientpb.ImplantConfig) string {
	switch cfg.Format {
	case clientpb.OutputFormat_SHARED_LIB:
		switch cfg.GOOS {
		case "windows":
			return ".dll"
		case "darwin":
			return ".dylib"
		default:
			return ".so"
		}
	case clientpb.OutputFormat_SHELLCODE:
		return ".bin"
	default: // EXECUTABLE
		if cfg.GOOS == "windows" {
			return ".exe"
		}
	}
	return ""
}

// ensureImplantExt appends the platform extension when the build name has none,
// so downloaded files keep a meaningful format instead of a bare name.
func ensureImplantExt(name string, cfg *clientpb.ImplantConfig) string {
	if name == "" || filepath.Ext(name) != "" {
		return name
	}
	return name + implantFileExtension(cfg)
}

// GenerateImplant generates and compiles an implant.
func (c *Client) GenerateImplant(req *GenerateRequest) (map[string]any, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("implant name is required")
	}
	// Checked before the build is queued. Sliver rejects an unknown
	// GOOS/GOARCH itself, but only as "invalid compiler target", and it accepts
	// a format it does not recognise by falling back to EXECUTABLE -- so a
	// request for "rom" used to come back as a working exe.
	if problem := validateBuildRequest(req.OS, req.Arch, req.Format); problem != "" {
		return nil, errors.New(problem)
	}
	cfg := buildImplantConfig(req, req.IsBeacon)

	// Implant build names are unique in the server's database, so reusing a
	// name fails with "UNIQUE constraint failed: implant_builds.name".
	// Rebuilding a profile is a normal workflow (tweak a setting, build again),
	// so replace the previous build of this name instead of erroring out. A
	// failure here is not fatal: the name may simply be new.
	if err := c.DeleteImplantBuild(req.Name); err != nil {
		log.Printf("[implants] no prior build named %q to replace: %v", req.Name, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	resp, err := c.RPC.Generate(ctx, &clientpb.GenerateReq{Config: cfg, Name: req.Name})
	if err != nil {
		return nil, err
	}
	var data string
	if resp.File != nil {
		data = base64.StdEncoding.EncodeToString(resp.File.Data)
	}
	name := ensureImplantExt(resp.File.Name, cfg)
	return map[string]any{
		"success": true,
		"message": fmt.Sprintf("built %s (%s/%s)", name, req.OS, req.Arch),
		"name":    name,
		"data":    data,
	}, nil
}

// KillSession sends a kill command to the implant.
// Sliver v1.15.16 没有 KillSession RPC 方法 — 服务端在 implant
// 断开后自动清理 session 记录，无需显式调用。
func (c *Client) KillSession(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
