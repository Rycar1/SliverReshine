// Package config holds the console's operator-editable settings and the
// first-run extraction that puts them on disk.
//
// The deployment shape this exists for is a single executable: copy one file to
// a host, run it, and get a working console. Everything the operator might want
// to change lives in a file that the binary writes on first start if it is not
// already there, so there is no separate archive to unpack and no configuration
// step before the first run.
//
// The file is never overwritten once it exists. A restart must not discard the
// operator's edits, and a first-run default that silently replaced them would be
// the worst possible behaviour for a file whose entire purpose is to be edited.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the settings file inside the state directory.
const FileName = "c2tool.json"

// ReadmeName is the operator-facing note written next to the settings file.
const ReadmeName = "README.txt"

// AuthConfig controls the console's HTTP Basic Auth.
type AuthConfig struct {
	// Enabled turns the login prompt on. Disabling it exposes the console to
	// anyone who can reach the port, which is why the default is true and why
	// the warning is printed loudly on every start.
	Enabled bool `json:"enabled"`
	// User is the account name. The password is deliberately NOT here: it lives
	// in a separate 0600 file so that this one can be read, diffed and shared
	// without leaking the secret.
	User string `json:"user"`
	// Realm is shown in the browser's login prompt.
	Realm string `json:"realm"`
}

// Config is the whole settings file.
type Config struct {
	// Addr is the web console's listen address.
	Addr string `json:"addr"`
	// Operator is the name recorded in the generated Sliver profile.
	Operator string `json:"operator"`
	// MultiplayerHost / Port expose the embedded server's gRPC surface. They
	// stay on loopback by default: the console is the only intended client, and
	// the gRPC API has no authentication of its own.
	MultiplayerHost string `json:"mpHost"`
	MultiplayerPort int    `json:"mpPort"`
	// AutoConnect attaches the console to the embedded server on startup.
	AutoConnect bool `json:"autoConnect"`
	// ServerOnly runs the embedded C2 server without the web console.
	ServerOnly bool `json:"serverOnly"`

	Auth AuthConfig `json:"auth"`
}

// Default returns the settings a first run starts from.
func Default() Config {
	return Config{
		Addr:            "0.0.0.0:8080",
		Operator:        "operator",
		MultiplayerHost: "127.0.0.1",
		MultiplayerPort: 31337,
		AutoConnect:     true,
		ServerOnly:      false,
		Auth: AuthConfig{
			Enabled: true,
			User:    "operator",
			Realm:   "",
		},
	}
}

// Path returns the settings file for a state directory.
func Path(home string) string { return filepath.Join(home, FileName) }

// Ensure writes a default settings file when none exists and returns the
// settings to use.
//
// The second return value reports whether the file was created, so the caller
// can tell the operator that a new file appeared rather than leaving them to
// discover it.
//
// A file that exists but cannot be parsed is an error, not a reason to fall back
// to defaults. Silently ignoring a broken config would start the console on a
// different port, or with auth off, and the operator would have no idea why.
func Ensure(home string) (Config, bool, error) {
	path := Path(home)

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		cfg, err := decode(raw)
		if err != nil {
			return Config{}, false, fmt.Errorf("%s: %w", path, err)
		}
		return cfg, false, nil
	case !os.IsNotExist(err):
		return Config{}, false, fmt.Errorf("read %s: %w", path, err)
	}

	cfg := Default()
	if err := Save(home, cfg); err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

// Load reads the settings file.
func Load(home string) (Config, error) {
	raw, err := os.ReadFile(Path(home))
	if err != nil {
		return Config{}, err
	}
	return decode(raw)
}

// Save writes the settings file atomically, so a crash mid-write cannot leave a
// truncated file that the next start would refuse to parse.
func Save(home string, cfg Config) error {
	blob, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	blob = append(blob, '\n')

	path := Path(home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// decode parses the file strictly: an unknown key is a typo, and a typo in a
// settings file that controls the listen address and the login is worth failing
// on rather than ignoring.
func decode(raw []byte) (Config, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	cfg := Default()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Normalize fills in anything left blank and clamps values that would otherwise
// make the console unreachable or unsafe to start.
func (c Config) Normalize() Config {
	if c.Addr == "" {
		c.Addr = Default().Addr
	}
	if c.Operator == "" {
		c.Operator = Default().Operator
	}
	if c.MultiplayerHost == "" {
		c.MultiplayerHost = Default().MultiplayerHost
	}
	if c.MultiplayerPort <= 0 || c.MultiplayerPort > 65535 {
		c.MultiplayerPort = Default().MultiplayerPort
	}
	if c.Auth.Enabled && c.Auth.User == "" {
		c.Auth.User = Default().Auth.User
	}
	return c
}

// Readme is the operator-facing note written beside the settings file. It is
// written once and never replaced, because it is the only documentation that
// exists on a host where a single binary was dropped.
const Readme = `c2tool — deployment notes
=========================

You are running a single self-contained binary. There is nothing else to
install: the Sliver server, the compiler toolchain and the web interface are
all inside it, and they unpack into this directory on first start.

Files in this directory
-----------------------

  c2tool.json      Settings. Edit and restart to apply.
  console-auth     The console login, "user:password". Mode 0600.
  sliver/          Server state: certificates, loot, the unpacked toolchain.
  configs/         The generated operator profile the console connects with.
  c2tool.log       Everything the process prints, mirrored to disk.

The extracted server binary is not here. It lives in the OS cache directory
(~/.cache/c2tool/bin on Linux, or $C2TOOL_HOME/bin when that variable is set),
so deleting this directory never leaves a stale binary behind.

Change the listen address or the login
--------------------------------------

  Edit c2tool.json:

    "addr":  "0.0.0.0:8080"   address the web console binds to
    "auth": {
      "enabled": true,        false turns the login prompt OFF
      "user":    "operator",  the account name
      "realm":   ""           text shown in the browser prompt
    }

  The password is not in that file. It is the second field of console-auth:

    printf 'operator:%s\n' 'a-new-password' > console-auth
    chmod 600 console-auth

  Deleting console-auth makes the next start generate a fresh random password
  and print it.

Turning authentication off
--------------------------

  Set "auth": { "enabled": false } in c2tool.json. The console will start with
  no login prompt and print a warning. Only do this where the port is not
  reachable by anyone you do not trust — an open C2 console is a full remote
  control channel for whoever finds it.

Command line
------------

  Flags override the file, which is what you want for a one-off:

    --addr            listen address
    --home            state directory (default: ~/.c2tool)
    --operator        operator name in the generated profile
    --mp-host/--mp-port   embedded gRPC listener (loopback by default)
    --auth-user       override the account name
    --auth-pass       override the password
    --server-only     run the C2 server without the web console
    --no-autoconnect  do not attach the console to the server on startup

First start
-----------

  The first run unpacks the compiler toolchain and can take a minute. Implant
  generation does not work until that finishes; the log says when it has.
`

// EnsureReadme writes the operator note when it is missing. It never overwrites,
// so an operator who annotated their copy keeps their notes.
func EnsureReadme(home string) (bool, error) {
	path := filepath.Join(home, ReadmeName)
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}

	if err := os.MkdirAll(home, 0o700); err != nil {
		return false, err
	}
	// 0644: this is documentation, and the operator may well read it over a
	// shared login. It carries no secrets.
	if err := os.WriteFile(path, []byte(Readme), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}
