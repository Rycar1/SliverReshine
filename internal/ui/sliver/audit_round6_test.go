package sliver

import (
	"strings"
	"testing"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
)

// F1: SaveHTTPC2Profile must reject a profile whose nested messages are nil,
// before the RPC reaches a server that dereferences them unconditionally.
//
// Sliver's CheckHTTPC2ConfigErrors starts with `len(config.ServerConfig.Cookies)`,
// so a profile without ServerConfig panics inside the server. The server is a
// child process that recovers only to os.Exit(99), so one malformed request takes
// down every session, beacon and listener.
func TestSaveHTTPC2ProfileRejectsNilNestedConfigs(t *testing.T) {
	cases := []struct {
		name string
		cfg  *clientpb.HTTPC2Config
	}{
		{"nil profile", nil},
		{"no ServerConfig", &clientpb.HTTPC2Config{
			Name:          "probe",
			ImplantConfig: &clientpb.HTTPC2ImplantConfig{},
		}},
		{"no ImplantConfig", &clientpb.HTTPC2Config{
			Name:         "probe",
			ServerConfig: &clientpb.HTTPC2ServerConfig{},
		}},
		{"neither nested config", &clientpb.HTTPC2Config{Name: "probe"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{}
			err := c.SaveHTTPC2Profile(tc.cfg, false)
			if err == nil {
				t.Fatal("SaveHTTPC2Profile accepted a profile that would panic the server")
			}
			if !strings.Contains(err.Error(), "serverConfig") &&
				!strings.Contains(err.Error(), "implantConfig") &&
				!strings.Contains(err.Error(), "no profile") {
				t.Errorf("rejected for an unexpected reason: %v", err)
			}
		})
	}
}

// F2: every encoder name the console advertises must resolve to a real enum, not
// to NONE.
//
// The server answers NONE by returning the input unchanged, so a name that fell
// through produced HTTP 200 and the operator's own unencoded shellcode.
func TestShellcodeEncoderNamesResolve(t *testing.T) {
	// The names the server advertises via ShellcodeEncoderMap, which is what the
	// console's GET returns and what the UI offers.
	advertised := []string{"shikata_ga_nai", "xor", "xor_dynamic"}

	for _, name := range advertised {
		enc, err := shellcodeEncoderFromString(name)
		if err != nil {
			t.Errorf("the advertised encoder %q was rejected: %v", name, err)
			continue
		}
		if enc == clientpb.ShellcodeEncoder_NONE {
			t.Errorf("the advertised encoder %q resolved to NONE, which the server "+
				"answers by returning the input unencoded", name)
		}
	}

	// The uppercase enum spellings must keep working too.
	for _, name := range []string{"XOR", "XOR_DYNAMIC", "SHIKATA_GA_NAI"} {
		enc, err := shellcodeEncoderFromString(name)
		if err != nil {
			t.Errorf("the enum spelling %q was rejected: %v", name, err)
			continue
		}
		if enc == clientpb.ShellcodeEncoder_NONE {
			t.Errorf("the enum spelling %q resolved to NONE", name)
		}
	}

	// And case must not matter.
	for _, name := range []string{"Xor", "XOR_dynamic", "Shikata_Ga_Nai"} {
		if enc, err := shellcodeEncoderFromString(name); err != nil || enc == clientpb.ShellcodeEncoder_NONE {
			t.Errorf("mixed case %q did not resolve (enc=%v err=%v)", name, enc, err)
		}
	}
}

// An unknown encoder must be an error. Falling back to NONE is what made the
// failure silent.
func TestShellcodeEncoderRejectsUnknownNames(t *testing.T) {
	for _, name := range []string{"", "  ", "nope", "aes", "xor2", "base64"} {
		if _, err := shellcodeEncoderFromString(name); err == nil {
			t.Errorf("shellcodeEncoderFromString(%q) returned no error; an unknown "+
				"name must not silently become NONE", name)
		}
	}
}

// The two name spaces must agree: every enum in the table must be a real protobuf
// value, or the lookup would silently produce NONE.
func TestKnownEncoderTableMatchesTheProto(t *testing.T) {
	for _, e := range knownShellcodeEncoders {
		v, ok := clientpb.ShellcodeEncoder_value[e.Enum]
		if !ok {
			t.Errorf("knownShellcodeEncoders names %q, which is not a protobuf enum value", e.Enum)
			continue
		}
		if clientpb.ShellcodeEncoder(v) == clientpb.ShellcodeEncoder_NONE {
			t.Errorf("knownShellcodeEncoders maps %q to NONE", e.Enum)
		}
		if len(e.Names) == 0 || e.Names[0] == "" {
			t.Errorf("knownShellcodeEncoders entry %q has no advertised name", e.Enum)
		}
	}
}

// F4: the interval fields are nanoseconds on the wire.
//
// The form collects seconds; the proto holds a time.Duration and the implant does
// time.Duration(interval) then time.Sleep. Passing raw seconds made a 60 s
// interval 60 ns -- a hot reconnect loop -- while every layer reported success.
func TestImplantConfigIntervalsAreNanoseconds(t *testing.T) {
	req := &GenerateRequest{
		OS:       "windows",
		Arch:     "amd64",
		Format:   "exe",
		Interval: 60,
		Jitter:   30,
	}

	// Beacon: BeaconInterval and BeaconJitter.
	beacon := buildImplantConfig(req, true)
	if beacon.BeaconInterval != int64(60*time.Second) {
		t.Errorf("BeaconInterval = %d ns, want %d (60s)", beacon.BeaconInterval, int64(60*time.Second))
	}
	if beacon.BeaconJitter != int64(30*time.Second) {
		t.Errorf("BeaconJitter = %d ns, want %d (30s)", beacon.BeaconJitter, int64(30*time.Second))
	}

	// Session: ReconnectInterval.
	session := buildImplantConfig(req, false)
	if session.ReconnectInterval != int64(60*time.Second) {
		t.Errorf("ReconnectInterval = %d ns, want %d (60s)",
			session.ReconnectInterval, int64(60*time.Second))
	}

	// A sub-second interval must survive the conversion rather than rounding to
	// zero, which would make the loop as tight as the bug did.
	fast := &GenerateRequest{OS: "windows", Arch: "amd64", Format: "exe", Interval: 1}
	if got := buildImplantConfig(fast, false).ReconnectInterval; got != int64(time.Second) {
		t.Errorf("a 1 s interval became %d ns", got)
	}
}

// The default must also be converted: leaving it raw was how a build with no
// explicit interval still got 60 ns.
func TestImplantConfigDefaultIntervalIsConverted(t *testing.T) {
	req := &GenerateRequest{OS: "windows", Arch: "amd64", Format: "exe"}
	cfg := buildImplantConfig(req, false)
	if cfg.ReconnectInterval != int64(60*time.Second) {
		t.Errorf("default ReconnectInterval = %d ns, want %d (60s)",
			cfg.ReconnectInterval, int64(60*time.Second))
	}
}
