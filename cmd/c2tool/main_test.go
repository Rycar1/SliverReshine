package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"c2tool/internal/config"
)

// withFlags installs a throwaway flag set carrying the same names as
// parseFlags, parses args into it, and restores the process-wide set when the
// test ends.
//
// applyOverrides decides precedence by asking flag.Visit which flags were
// actually given, so a test that wants to simulate "-addr was on the command
// line" has to make the flag genuinely present in the set; passing a value in
// the overrides struct alone is exactly the case that must NOT override the
// settings file.
func withFlags(t *testing.T, args ...string) {
	t.Helper()
	prev := flag.CommandLine
	fs := flag.NewFlagSet("c2tool-test", flag.ContinueOnError)
	fs.String("addr", "", "")
	fs.String("operator", "", "")
	fs.String("mp-host", "", "")
	fs.Int("mp-port", 0, "")
	fs.Bool("server-only", false, "")
	fs.Bool("no-autoconnect", false, "")
	fs.String("auth-user", "", "")
	fs.String("auth-pass", "", "")
	fs.String("auth-realm", "", "")
	flag.CommandLine = fs
	t.Cleanup(func() { flag.CommandLine = prev })
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
}

// clearEnv neutralises every C2TOOL_* variable applyOverrides reads, so the
// machine the test runs on cannot decide its outcome. An empty value counts as
// unset to lookupEnv.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"C2TOOL_ADDR",
		"C2TOOL_AUTH_USER",
		"C2TOOL_AUTH_REALM",
		"C2TOOL_AUTH_PASS",
		"C2TOOL_AUTH",
	} {
		t.Setenv(k, "")
	}
}

// quietProvision calls provision and then closes the log file it opened and
// restores the logger.
//
// installLogFile points the process-wide logger at <home>/c2tool.log and keeps
// the handle open, which on Windows means the temp directory cannot be removed
// while it lives. Closing it here also keeps one test's log file from being
// repointed at by the next.
func quietProvision(t *testing.T, base string, o options) (config.Config, string) {
	t.Helper()
	before := log.Writer()
	settings, pass := provision(base, o)
	if f, ok := log.Writer().(*fanout); ok {
		for _, w := range f.w {
			if w != os.Stderr {
				_ = w.Close()
			}
		}
	}
	log.SetOutput(before)
	return settings, pass
}

// The precedence rule applyOverrides exists for: flag, then environment, then
// settings file, then the built-in default. An untouched flag must leave the
// file alone -- the regression it guards is a flag default silently winning
// over a value the operator put in the settings file.
func TestApplyOverridesPrecedence(t *testing.T) {
	t.Run("an untouched flag leaves the settings file alone", func(t *testing.T) {
		clearEnv(t)
		withFlags(t)
		cfg := config.Default()
		cfg.Addr = "10.0.0.1:9999"
		cfg.Operator = "from-file"

		got, pass := applyOverrides(cfg, overrides{addr: "ignored:1", operator: "ignored"})

		if got.Addr != "10.0.0.1:9999" {
			t.Errorf("Addr = %q, want the file value; a flag that was not given must not override it", got.Addr)
		}
		if got.Operator != "from-file" {
			t.Errorf("Operator = %q, want the file value", got.Operator)
		}
		if pass != "" {
			t.Errorf("pass = %q, want empty: neither the flag nor the environment supplied one", pass)
		}
	})

	t.Run("a flag wins over the settings file", func(t *testing.T) {
		clearEnv(t)
		withFlags(t, "-addr", "1.2.3.4:8080", "-operator", "cli")
		cfg := config.Default()

		got, _ := applyOverrides(cfg, overrides{addr: "1.2.3.4:8080", operator: "cli"})

		if got.Addr != "1.2.3.4:8080" {
			t.Errorf("Addr = %q, want the flag value", got.Addr)
		}
		if got.Operator != "cli" {
			t.Errorf("Operator = %q, want the flag value", got.Operator)
		}
	})

	t.Run("the environment fills a value the file left at its default", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("C2TOOL_ADDR", "203.0.113.7:8443")
		withFlags(t)

		got, _ := applyOverrides(config.Default(), overrides{})

		if got.Addr != "203.0.113.7:8443" {
			t.Errorf("Addr = %q, want the environment value", got.Addr)
		}
	})

	t.Run("a flag beats the environment", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("C2TOOL_ADDR", "203.0.113.7:8443")
		withFlags(t, "-addr", "10.1.1.1:80")

		got, _ := applyOverrides(config.Default(), overrides{addr: "10.1.1.1:80"})

		if got.Addr != "10.1.1.1:80" {
			t.Errorf("Addr = %q, want the flag to win over C2TOOL_ADDR", got.Addr)
		}
	})

	// The shipped deployment binds the wildcard so the console is reachable
	// from another machine. Asking for it explicitly must survive untouched --
	// nothing here may rewrite a network bind back to loopback.
	t.Run("an explicit wildcard bind is honoured", func(t *testing.T) {
		clearEnv(t)
		withFlags(t, "-addr", "0.0.0.0:8080")

		got, _ := applyOverrides(config.Default(), overrides{addr: "0.0.0.0:8080"})

		if got.Addr != "0.0.0.0:8080" {
			t.Errorf("Addr = %q, want the explicit wildcard bind preserved", got.Addr)
		}
	})

	t.Run("the multiplayer listener and server-only come from their flags", func(t *testing.T) {
		clearEnv(t)
		withFlags(t, "-mp-host", "10.9.9.9", "-mp-port", "31338", "-server-only")

		got, _ := applyOverrides(config.Default(), overrides{mpHost: "10.9.9.9", mpPort: 31338, serverOnly: true})

		if got.MultiplayerHost != "10.9.9.9" {
			t.Errorf("MultiplayerHost = %q, want the flag value", got.MultiplayerHost)
		}
		if got.MultiplayerPort != 31338 {
			t.Errorf("MultiplayerPort = %d, want 31338", got.MultiplayerPort)
		}
		if !got.ServerOnly {
			t.Error("ServerOnly = false, want true when -server-only was given")
		}
	})

	t.Run("-no-autoconnect turns AutoConnect off, and its absence leaves it on", func(t *testing.T) {
		clearEnv(t)

		withFlags(t, "-no-autoconnect")
		got, _ := applyOverrides(config.Default(), overrides{noAutoConn: true})
		if got.AutoConnect {
			t.Error("AutoConnect = true, want false when -no-autoconnect was given")
		}

		withFlags(t)
		got, _ = applyOverrides(config.Default(), overrides{noAutoConn: true})
		if !got.AutoConnect {
			t.Error("AutoConnect = false, want the file/default value when the flag was not given")
		}
	})
}

// The login switch has three ways in -- the environment, an explicit empty
// -auth-user, and a named -auth-user -- and each one has to land on the right
// Enabled/User pair.
func TestApplyOverridesAuth(t *testing.T) {
	for _, off := range []string{"off", "false", "0"} {
		t.Run("C2TOOL_AUTH="+off+" disables the login", func(t *testing.T) {
			clearEnv(t)
			t.Setenv("C2TOOL_AUTH", off)
			withFlags(t)
			cfg := config.Default()
			if !cfg.Auth.Enabled {
				t.Fatal("precondition: the default enables auth")
			}

			got, _ := applyOverrides(cfg, overrides{})

			if got.Auth.Enabled {
				t.Errorf("Auth.Enabled = true, want false for C2TOOL_AUTH=%s", off)
			}
		})
	}

	t.Run("C2TOOL_AUTH=on leaves the login enabled", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("C2TOOL_AUTH", "on")
		withFlags(t)

		got, _ := applyOverrides(config.Default(), overrides{})

		if !got.Auth.Enabled {
			t.Error("Auth.Enabled = false, want true: only off/false/0 disable the login")
		}
	})

	t.Run("an explicit empty -auth-user disables the login", func(t *testing.T) {
		clearEnv(t)
		withFlags(t, "-auth-user", "")

		got, _ := applyOverrides(config.Default(), overrides{authUser: ""})

		if got.Auth.Enabled {
			t.Error("Auth.Enabled = true, want false when -auth-user was given empty")
		}
		if got.Auth.User != "" {
			t.Errorf("Auth.User = %q, want empty", got.Auth.User)
		}
	})

	t.Run("-auth-user names the account and enables the login", func(t *testing.T) {
		clearEnv(t)
		withFlags(t, "-auth-user", "alice")
		cfg := config.Default()
		cfg.Auth.Enabled = false

		got, _ := applyOverrides(cfg, overrides{authUser: "alice"})

		if got.Auth.User != "alice" {
			t.Errorf("Auth.User = %q, want alice", got.Auth.User)
		}
		if !got.Auth.Enabled {
			t.Error("Auth.Enabled = false, want true for a non-empty -auth-user")
		}
	})

	t.Run("C2TOOL_AUTH_USER is the account when no flag is given", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("C2TOOL_AUTH_USER", "env-user")
		withFlags(t)

		got, _ := applyOverrides(config.Default(), overrides{})

		if got.Auth.User != "env-user" {
			t.Errorf("Auth.User = %q, want env-user", got.Auth.User)
		}
	})

	t.Run("-auth-realm sets the browser prompt", func(t *testing.T) {
		clearEnv(t)
		withFlags(t, "-auth-realm", "Ops console")

		got, _ := applyOverrides(config.Default(), overrides{authRealm: "Ops console"})

		if got.Auth.Realm != "Ops console" {
			t.Errorf("Auth.Realm = %q, want the flag value", got.Auth.Realm)
		}
	})

	// The password never lives on the Config; it comes back as a second return
	// value so the settings file can stay readable and diffable.
	t.Run("C2TOOL_AUTH_PASS supplies the password when no flag is given", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("C2TOOL_AUTH_PASS", "env-secret")
		withFlags(t)

		_, pass := applyOverrides(config.Default(), overrides{})

		if pass != "env-secret" {
			t.Errorf("pass = %q, want the environment value", pass)
		}
	})

	t.Run("-auth-pass beats C2TOOL_AUTH_PASS", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("C2TOOL_AUTH_PASS", "env-secret")
		withFlags(t, "-auth-pass", "flag-secret")

		_, pass := applyOverrides(config.Default(), overrides{authPass: "flag-secret"})

		if pass != "flag-secret" {
			t.Errorf("pass = %q, want the flag value", pass)
		}
	})
}

func TestConsoleScheme(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"plain http without TLS material", config.Config{}, "http"},
		{"a cert without a key is still http", config.Config{TLSCert: "c.pem"}, "http"},
		{"a key without a cert is still http", config.Config{TLSKey: "k.pem"}, "http"},
		{"both halves make it https", config.Config{TLSCert: "c.pem", TLSKey: "k.pem"}, "https"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := consoleScheme(tc.cfg); got != tc.want {
				t.Errorf("consoleScheme(%+v) = %q, want %q", tc.cfg, got, tc.want)
			}
		})
	}
}

// displayAddr is what the banner prints. A wildcard bind is not clickable, so
// it becomes loopback; everything else is left exactly as configured.
func TestDisplayAddr(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{"0.0.0.0:8080", "127.0.0.1:8080"},
		{"[::]:8080", "127.0.0.1:8080"},
		{":8080", "127.0.0.1:8080"},
		{"127.0.0.1:8080", "127.0.0.1:8080"},
		{"10.1.2.3:8080", "10.1.2.3:8080"},
		{"localhost:9000", "localhost:9000"},
		{"garbage", "garbage"},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			if got := displayAddr(tc.addr); got != tc.want {
				t.Errorf("displayAddr(%q) = %q, want %q", tc.addr, got, tc.want)
			}
		})
	}
}

func TestLookupEnv(t *testing.T) {
	t.Run("a value reports set", func(t *testing.T) {
		t.Setenv("C2TOOL_TEST_LOOKUP", "value")
		v, ok := lookupEnv("C2TOOL_TEST_LOOKUP")
		if !ok || v != "value" {
			t.Errorf("lookupEnv = (%q, %v), want (value, true)", v, ok)
		}
	})

	t.Run("an empty value reports unset", func(t *testing.T) {
		t.Setenv("C2TOOL_TEST_LOOKUP_EMPTY", "")
		if v, ok := lookupEnv("C2TOOL_TEST_LOOKUP_EMPTY"); ok || v != "" {
			t.Errorf("lookupEnv = (%q, %v), want (\"\", false)", v, ok)
		}
	})

	t.Run("a missing variable reports unset", func(t *testing.T) {
		const key = "C2TOOL_TEST_LOOKUP_MISSING"
		prev, had := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(key, prev)
			}
		})

		if v, ok := lookupEnv(key); ok || v != "" {
			t.Errorf("lookupEnv = (%q, %v), want (\"\", false)", v, ok)
		}
	})
}

func TestFlagSet(t *testing.T) {
	clearEnv(t)
	withFlags(t, "-addr", "1.2.3.4:1")

	if !flagSet("addr") {
		t.Error("flagSet(addr) = false, want true after -addr was parsed")
	}
	if flagSet("operator") {
		t.Error("flagSet(operator) = true, want false: that flag was not given")
	}

	withFlags(t)
	if flagSet("addr") {
		t.Error("flagSet(addr) = true, want false on a bare command line")
	}
}

func TestGeneratePassword(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

	a, err := generatePassword()
	if err != nil {
		t.Fatal(err)
	}
	b, err := generatePassword()
	if err != nil {
		t.Fatal(err)
	}

	if len(a) != 24 {
		t.Errorf("password length = %d, want 24", len(a))
	}
	for i := 0; i < len(a); i++ {
		if strings.IndexByte(alphabet, a[i]) < 0 {
			t.Fatalf("password contains %q, which is outside the URL-safe alphabet", a[i])
		}
	}
	if a == b {
		t.Error("two generated passwords are identical; the source is not random")
	}
}

func TestResolveHome(t *testing.T) {
	t.Run("an explicit directory is created", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "state", "nested")

		got := resolveHome(dir)

		if got != dir {
			t.Errorf("resolveHome = %q, want %q", got, dir)
		}
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("state directory was not created: %v", err)
		}
		if !fi.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
		// Windows does not carry Unix permission bits; the 0700 request is
		// only meaningful on the platforms that do.
		if runtime.GOOS != "windows" {
			if perm := fi.Mode().Perm(); perm != 0o700 {
				t.Errorf("mode = %o, want 700", perm)
			}
		}
	})

	t.Run("an empty flag falls back to C2TOOL_HOME", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "home")
		t.Setenv("C2TOOL_HOME", dir)

		got := resolveHome("")

		if got != dir {
			t.Errorf("resolveHome(\"\") = %q, want %q from C2TOOL_HOME", got, dir)
		}
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("state directory was not created: %v", err)
		}
	})
}

// provision is the first-run contract in one call: write the settings and the
// deployment notes once, layer the command line over them, and leave a log
// file behind for a detached server.
func TestProvisionWritesFirstRunFiles(t *testing.T) {
	clearEnv(t)
	withFlags(t)
	base := t.TempDir()

	settings, pass := quietProvision(t, base, options{})

	if pass != "" {
		t.Errorf("pass = %q, want empty: no flag or environment supplied one", pass)
	}
	// The shipped default keeps the console reachable from another machine.
	if settings.Addr != "0.0.0.0:8080" {
		t.Errorf("Addr = %q, want the shipped 0.0.0.0:8080 default", settings.Addr)
	}
	if !settings.Auth.Enabled {
		t.Error("Auth.Enabled = false, want true: the login is on by default")
	}
	if _, err := os.Stat(config.Path(base)); err != nil {
		t.Errorf("settings file was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, config.ReadmeName)); err != nil {
		t.Errorf("deployment notes were not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "c2tool.log")); err != nil {
		t.Errorf("log file was not created: %v", err)
	}
}

// A restart must not overwrite an edit: the settings file is the operator's
// document, and the first run is the only time this process writes it.
func TestProvisionKeepsAnEditedSettingsFile(t *testing.T) {
	clearEnv(t)
	withFlags(t)
	base := t.TempDir()

	quietProvision(t, base, options{})

	edited, _, err := config.Ensure(base)
	if err != nil {
		t.Fatal(err)
	}
	edited.Addr = "127.0.0.1:1234"
	edited.Operator = "edited-by-hand"
	if err := config.Save(base, edited); err != nil {
		t.Fatal(err)
	}

	settings, _ := quietProvision(t, base, options{})

	if settings.Addr != "127.0.0.1:1234" {
		t.Errorf("Addr = %q, want the edited value preserved across a restart", settings.Addr)
	}
	if settings.Operator != "edited-by-hand" {
		t.Errorf("Operator = %q, want the edited value preserved", settings.Operator)
	}
}

// The flag path through provision: a value given on the command line has to
// land in the returned settings even though the file already exists.
func TestProvisionAppliesFlagsOverTheFile(t *testing.T) {
	clearEnv(t)
	withFlags(t, "-addr", "192.0.2.10:8443")
	base := t.TempDir()

	settings, _ := quietProvision(t, base, options{addr: "192.0.2.10:8443"})

	if settings.Addr != "192.0.2.10:8443" {
		t.Errorf("Addr = %q, want the flag value", settings.Addr)
	}
}
