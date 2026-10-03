package sliver

import (
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
	ctx, cancel := rpcCtx(rpcDefault)
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
	ctx, cancel := rpcCtx(rpcDefault)
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
	ctx, cancel := rpcCtx(rpcDefault)
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
	ctx, cancel := rpcCtx(rpcDefault)
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

// sharedLibArches records the targets that can actually produce a c-shared
// library.
//
// The matrix run found five combinations that the console offered, accepted,
// and then failed with a bare "rpc error: code = Internal desc = exit status 1"
// after a compiler ran. Three separate causes, none of them a defect in this
// code, and all of them avoidable before the build starts:
//
//  1. Sliver's zig target table has no entry for linux/arm or any freebsd
//     target, so the CC it builds is "zig cc -target " with an empty target and
//     the compiler dies with "error: unknown architecture: ”".
//
//  2. Darwin cross-compilation shells out to a hardcoded osxcross path
//     (/opt/osxcross/...) that exists in Sliver's own Linux build container and
//     nowhere else -- certainly not on a Windows host running this console.
//
//  3. The Go toolchain itself does not support -buildmode=c-shared on
//     freebsd/386.
//
// Only windows and linux/amd64, linux/arm64 are reachable here. Notably
// linux/386 shared also depends on zig, which does have a target for it.
var sharedLibArches = map[string][]string{
	"windows": {"amd64", "386", "arm64"},
	"linux":   {"amd64", "386", "arm64"},
	"darwin":  {},
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

	// A shared library needs a C toolchain, and which ones this deployment can
	// reach is narrower than the target list. Checked here so the operator gets
	// a sentence instead of a compiler's exit code.
	if strings.EqualFold(format, "shared") && os != "" && arch != "" {
		arches, ok := sharedLibArches[strings.ToLower(os)]
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
					return fmt.Sprintf("%s shared libraries cannot be built by this console "+
						"(they need an osxcross toolchain that exists only in Sliver's own build "+
						"container); use exe or shellcode", os)
				}
				return fmt.Sprintf("%s shared libraries are not supported on %s; supported: %s. "+
					"linux/arm and freebsd need a zig C target that this build does not define, "+
					"and freebsd/386 is refused by the Go toolchain itself",
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
	// Interval and Jitter arrive from the form in SECONDS, and the wire fields are
	// nanoseconds. ReconnectInterval is a time.Duration, BeaconInterval and
	// BeaconJitter are compared against minBeaconInterval via time.Duration too --
	// the official client sends int64(time.Duration) for each:
	//
	//	ReconnectInterval:   reconnectInterval * int64(time.Second)
	//	config.BeaconInterval = int64(interval)          // interval is a Duration
	//	config.BeaconJitter   = int64(beaconJitter * time.Second)
	//
	// Assigning the raw seconds made every one of them 1e9 times too small, so a
	// 60 s reconnect interval became 60 ns and the implant's reconnect loop became
	// a hot loop. The build reported success either way.
	intervalNanos := req.Interval * int64(time.Second)
	jitterNanos := req.Jitter * int64(time.Second)

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
		BeaconInterval:     intervalNanos,
		BeaconJitter:       jitterNanos,
		HTTPC2ConfigName:   "default",
		ConnectionStrategy: "sequential",
	}
	if isBeacon {
		cfg.BeaconInterval = intervalNanos
		cfg.BeaconJitter = jitterNanos
	} else {
		cfg.ReconnectInterval = intervalNanos
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

	// A build name is unique in the server's database, and the server compiles
	// the implant *before* it writes the build row -- so a Generate under a name
	// that is already taken runs the whole toolchain and then fails at the very
	// last step with "UNIQUE constraint failed: implant_builds.name".
	//
	// The old code deleted the previous build before generating. That is the
	// only order in which a rebuild can be made to work at all, but it destroys
	// the previous build before anything has shown the new one can be built: a
	// garble failure, a missing cross-compiler or a bad setting then left the
	// operator with no build at all, old or new.
	//
	// The delete therefore happens as late as this RPC surface allows. When the
	// name is already taken the new build is attempted FIRST, with the previous
	// build still in place:
	//
	//   - a failure for any other reason leaves the previous build untouched,
	//     and its error is returned as-is;
	//   - a failure *because* the name is taken has already compiled the
	//     payload -- that is what the server's ordering guarantees -- so the
	//     name can now be freed and the build retried.
	//
	// A rebuild therefore compiles twice. That is the price of not destroying
	// the previous build on the strength of a build that has not been proven to
	// work. The one window left is the retry failing after the name was freed,
	// and that retry is the same request that compiled a moment earlier.
	//
	// Building under a temporary name and renaming it afterwards would avoid
	// both the second compile and that window, but the server exposes no RPC
	// that renames a build or re-saves one: SaveImplantBuild is reached only
	// from Generate, and from GenerateExternalSaveBuild, which is restricted to
	// an assigned external builder. A build left under a temporary name would
	// be one the operator could not find under the name they asked for, so the
	// ordering above is the best the available RPCs allow.
	prior, err := c.implantBuildExists(req.Name)
	if err != nil {
		// Not being able to list the builds is not a reason to refuse to build,
		// and it is not a reason to delete anything either. Treat the name as
		// unknown: the build is still attempted, and a collision then surfaces
		// as the server's own error with nothing removed.
		log.Printf("[implants] could not list builds before generating %q: %v", req.Name, err)
		prior = false
	}

	resp, err := c.runGenerate(req.Name, cfg)
	if err != nil {
		if !prior || !isDuplicateBuildName(err) {
			// Nothing has been deleted, so a previous build of this name -- if
			// there is one -- is still on the server.
			return nil, err
		}
		// The name is taken and the payload compiled. Free the name, then build
		// the replacement.
		if delErr := c.DeleteImplantBuild(req.Name); delErr != nil {
			// Not fatal on its own: the build below is the real answer, and it
			// reports the collision again if the name is in fact still taken.
			log.Printf("[implants] could not remove build %q before replacing it: %v", req.Name, delErr)
		}
		resp, err = c.runGenerate(req.Name, cfg)
		if err != nil {
			return nil, fmt.Errorf(
				"the previous build %q was removed to free the name, but the replacement failed to build: %w",
				req.Name, err)
		}
		return implantBuildResponse(resp, cfg, req, true), nil
	}
	return implantBuildResponse(resp, cfg, req, false), nil
}

// runGenerate issues one Generate RPC under the console's build deadline.
func (c *Client) runGenerate(name string, cfg *clientpb.ImplantConfig) (*clientpb.Generate, error) {
	ctx, cancel := rpcCtx(rpcLong)
	defer cancel()
	return c.RPC.Generate(ctx, &clientpb.GenerateReq{Config: cfg, Name: name})
}

// implantBuildExists reports whether the server already holds a build with this
// name. It is a plain read: it decides how a failed build is interpreted, not
// what happens on the server.
func (c *Client) implantBuildExists(name string) (bool, error) {
	builds, err := c.ImplantBuilds()
	if err != nil {
		return false, err
	}
	for _, b := range builds {
		if b.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// isDuplicateBuildName reports whether a failed Generate was refused because a
// build of that name already exists.
//
// This refusal is the signal that makes the replacement above safe: the server
// writes the build row after the compiler has run, so reaching it means the
// payload itself built. The error message is the only channel the RPC offers
// for it, so the match covers the three database backends Sliver can be
// configured with (SQLite, Postgres, MySQL). The match is deliberately narrow
// -- the table name is required as well as the duplicate marker -- because a
// false positive would free a name that was never actually taken, while a
// false negative only falls back to reporting the collision.
func isDuplicateBuildName(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "implant_build") {
		return false
	}
	for _, marker := range []string{
		"unique constraint failed", // sqlite
		"duplicate key",            // postgres
		"duplicate entry",          // mysql
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// implantBuildResponse renders the API body for a build that succeeded.
//
// `replaced` is what lets the console tell the operator that a previous build
// of this name was removed. Without it a replacement looks exactly like a
// fresh build, which is how a rebuild -- and, when one failed, the loss of the
// build it replaced -- went unnoticed.
func implantBuildResponse(resp *clientpb.Generate, cfg *clientpb.ImplantConfig, req *GenerateRequest, replaced bool) map[string]any {
	var data string
	name := req.Name
	if resp.GetFile() != nil {
		data = base64.StdEncoding.EncodeToString(resp.File.Data)
		if built := ensureImplantExt(resp.File.Name, cfg); built != "" {
			name = built
		}
	}
	return map[string]any{
		"success":  true,
		"message":  fmt.Sprintf("built %s (%s/%s)", name, req.OS, req.Arch),
		"name":     name,
		"data":     data,
		"replaced": replaced,
	}
}

// KillSession sends a kill command to the implant.
// Sliver v1.15.16 没有 KillSession RPC 方法 — 服务端在 implant
// 断开后自动清理 session 记录，无需显式调用。
func (c *Client) KillSession(id string) error {
	ctx, cancel := rpcCtx(rpcDefault)
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
	ctx, cancel := rpcCtx(rpcDefault)
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
