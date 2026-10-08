package sliver

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
)

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
			return nil, explainBuildFailure(cfg, err)
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
				req.Name, explainBuildFailure(cfg, err))
		}
		return implantBuildResponse(resp, cfg, req, true), nil
	}
	return implantBuildResponse(resp, cfg, req, false), nil
}

// runGenerate issues one Generate RPC under the console's build deadline.
func (c *Client) runGenerate(name string, cfg *clientpb.ImplantConfig) (*clientpb.Generate, error) {
	ctx, cancel := c.rpcCtx(rpcLong)
	defer cancel()
	return c.RPC.Generate(ctx, &clientpb.GenerateReq{Config: cfg, Name: name})
}

// explainBuildFailure turns a build error into one the operator can act on.
//
// Sliver compiles shared libraries and linux shellcode through a C compiler,
// and a Darwin target needs osxcross; neither ships with a console-only
// deployment. When the toolchain is missing the RPC returns the compiler's own
// terse message -- cgo's `C compiler "gcc" not found: exec: "gcc": executable
// file not found in $PATH`, or a bare `exit status 1` from the zig/osxcross
// wrapper -- which names neither the missing tool nor the way to fix it.
//
// The request itself was valid, so this is a deployment gap rather than a bad
// target: the message names the tool to install and keeps the original error
// attached, so a genuine compile failure is still visible underneath.
func explainBuildFailure(cfg *clientpb.ImplantConfig, err error) error {
	hint := buildToolchainHint(cfg, err)
	if hint == "" {
		return err
	}
	return NewMissingToolchainError(hint, err)
}

// MissingToolchainError is a build that failed because the console's own host
// lacks the C toolchain the requested format needs.
//
// It is a type rather than a formatted error so the API layer can answer it
// without reading the sentence. The hint names the missing compiler as "not
// installed on this host", and the not-found marker list claims that phrase --
// so this deployment gap was answered with a 404, which says "the thing you
// asked for does not exist" about a host that merely needs gcc. The type keeps
// the classification honest and lets the status be chosen by what happened
// rather than by which words the message happens to contain.
type MissingToolchainError struct {
	hint string
	err  error
}

// NewMissingToolchainError wraps a build error that failed for want of a C
// toolchain on the console host, attaching the operator-facing hint.
//
// It is exported so the API layer -- and its tests -- can classify a build
// failure without a live sliver-server.
func NewMissingToolchainError(hint string, err error) error {
	return &MissingToolchainError{hint: hint, err: err}
}

func (e *MissingToolchainError) Error() string { return e.hint + ": " + e.err.Error() }

// Unwrap keeps the server's original error reachable, so a caller that wants
// the gRPC status still gets it.
func (e *MissingToolchainError) Unwrap() error { return e.err }

// IsMissingToolchain reports whether err is a build that failed for want of a C
// toolchain on the console's host.
func IsMissingToolchain(err error) bool {
	var m *MissingToolchainError
	return errors.As(err, &m)
}

// buildToolchainHint returns a sentence naming the missing C toolchain for a
// build that needs one, or an empty string when the error is not a toolchain
// problem.
func buildToolchainHint(cfg *clientpb.ImplantConfig, err error) string {
	if err == nil || cfg == nil {
		return ""
	}
	// Only shared libraries and shellcode invoke a C compiler. An executable
	// build that failed is a different problem with a different fix, so it is
	// left untouched.
	if cfg.Format != clientpb.OutputFormat_SHARED_LIB && cfg.Format != clientpb.OutputFormat_SHELLCODE {
		return ""
	}
	msg := strings.ToLower(err.Error())
	if !looksLikeMissingToolchain(msg) {
		return ""
	}
	if cfg.GOOS == "darwin" {
		return fmt.Sprintf(
			"cannot build %s/%s %s: a Darwin target is compiled with the osxcross C "+
				"cross-compiler, which is not installed on this host (Sliver expects the "+
				"SDK under /opt/osxcross). Install osxcross, or build for linux or windows",
			cfg.GOOS, cfg.GOARCH, formatName(cfg.Format))
	}
	return fmt.Sprintf(
		"cannot build %s/%s %s: it needs a C compiler (gcc) that is not installed on this "+
			"host. Install gcc (for example `apt-get install -y gcc`), or build an "+
			"executable instead",
		cfg.GOOS, cfg.GOARCH, formatName(cfg.Format))
}

// looksLikeMissingToolchain matches the errors a missing C toolchain produces.
//
// The list is explicit rather than a single "exit status" test, because a
// non-zero status can also come from a genuine compile error in a working
// toolchain, and mislabelling that as "install gcc" would send the operator
// down the wrong path. The one status-based match is the terse
// "exit status 1" the zig/osxcross wrappers emit with no diagnostics of their
// own -- for a format that cannot build without a compiler, that is the
// toolchain, not the source.
func looksLikeMissingToolchain(msg string) bool {
	for _, marker := range []string{
		`c compiler "gcc" not found`,   // cgo, GCC
		`c compiler "clang" not found`, // cgo, clang
		"cgo: c compiler",              // any cgo compiler-resolution failure
		"executable file not found",    // exec: "gcc": executable file not found in $PATH
		"not found in $path",           // cgo's PATH-qualified form
		"no such file or directory",    // osxcross / wrapper script absent
		"unknown architecture",         // zig cc -target with an empty target
		"exit status 1",                // zig/osxcross wrapper, no diagnostics
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// formatName renders an output format for a message.
func formatName(f clientpb.OutputFormat) string {
	switch f {
	case clientpb.OutputFormat_SHARED_LIB:
		return "shared library"
	case clientpb.OutputFormat_SHELLCODE:
		return "shellcode"
	case clientpb.OutputFormat_SERVICE:
		return "service"
	default:
		return "executable"
	}
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
