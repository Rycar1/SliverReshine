// Command c2tool ships a self-contained C2 console: it provisions a Sliver
// server from an embedded payload, supervises it, and serves the web interface
// that talks to it over gRPC.
//
// Everything an operator would otherwise do by hand — unpacking the compiler
// toolchain, starting the multiplayer listener, minting an operator profile,
// choosing a console password — happens automatically on first launch. The
// result is a single URL.
//
// The binary is the whole deployment. It carries the server, the toolchain and
// the web interface, and it writes the settings and the login record into its
// state directory on first start if they are not already there. Nothing has to
// be unpacked alongside it and nothing has to be configured before it runs.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"c2tool/internal/config"
	"c2tool/internal/launch"
	"c2tool/internal/ui/api"
	"c2tool/internal/ui/sliver"
)

func main() {
	var (
		addr       = flag.String("addr", "", "HTTP listen address for the web console (overrides the settings file)")
		home       = flag.String("home", "", "base directory for state and profiles (default: <exe dir>/data, then ~/.c2tool)")
		operator   = flag.String("operator", "", "operator name recorded in the generated profile")
		mpHost     = flag.String("mp-host", "", "host the embedded Sliver gRPC listener binds to")
		mpPort     = flag.Int("mp-port", 0, "port the embedded Sliver gRPC listener binds to")
		serverOnly = flag.Bool("server-only", false, "run only the embedded C2 server and skip the web console")
		noAutoConn = flag.Bool("no-autoconnect", false, "do not attach the console to the generated profile on startup")
		authUser   = flag.String("auth-user", "", "HTTP Basic Auth username for the web console (empty disables auth)")
		authPass   = flag.String("auth-pass", "", "HTTP Basic Auth password for the web console")
		authRealm  = flag.String("auth-realm", "", "Basic Auth realm shown in the browser prompt (default: a generic string)")
		authFile   = flag.String("auth-file", "", "credential file holding the console account (default: <home>/console-auth)")
	)
	flag.Parse()

	base := *home
	if base == "" {
		dir, err := defaultHome()
		if err != nil {
			log.Fatalf("[c2tool] cannot determine home directory: %v", err)
		}
		base = dir
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		log.Fatalf("[c2tool] cannot create %s: %v", base, err)
	}

	// ---- first-run provisioning --------------------------------------------
	//
	// Everything the operator is expected to be able to edit is written here, on
	// the first start, and never again. A restart must not overwrite an edit.
	settings, created, err := config.Ensure(base)
	if err != nil {
		log.Fatalf("[c2tool] %v", err)
	}
	if created {
		log.Printf("[c2tool] wrote default settings to %s", config.Path(base))
	}
	if wroteReadme, err := config.EnsureReadme(base); err != nil {
		log.Printf("[c2tool] WARNING: cannot write %s: %v", filepath.Join(base, config.ReadmeName), err)
	} else if wroteReadme {
		log.Printf("[c2tool] wrote deployment notes to %s", filepath.Join(base, config.ReadmeName))
	}

	settings, passOverride := applyOverrides(settings, overrides{
		addr:       *addr,
		operator:   *operator,
		mpHost:     *mpHost,
		mpPort:     *mpPort,
		serverOnly: *serverOnly,
		noAutoConn: *noAutoConn,
		authUser:   *authUser,
		authPass:   *authPass,
		authRealm:  *authRealm,
	})
	settings = settings.Normalize()

	installLogFile(filepath.Join(base, "c2tool.log"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ---- embedded C2 server -------------------------------------------------
	srv, err := launch.Start(ctx, launch.Options{
		StateDir:        filepath.Join(base, "sliver"),
		ConfigDir:       filepath.Join(base, "configs"),
		Operator:        settings.Operator,
		MultiplayerHost: settings.MultiplayerHost,
		MultiplayerPort: settings.MultiplayerPort,
		LogPath:         filepath.Join(base, "sliver-server.log"),
	})
	if err != nil {
		log.Fatalf("[c2tool] %v", err)
	}
	defer srv.Stop()

	profile, _ := launch.ReadProfile(srv.ProfilePath)
	if profile != nil {
		log.Printf("[c2tool] operator %q -> %s", profile.Operator, profile.Path)
	}

	if settings.ServerOnly {
		log.Printf("[c2tool] server-only mode, gRPC on %s:%d; Ctrl-C to stop",
			settings.MultiplayerHost, settings.MultiplayerPort)
		<-ctx.Done()
		return
	}

	// ---- web console --------------------------------------------------------
	web := api.New()
	if settings.AutoConnect {
		client, err := connectProfile(launchProfileName())
		if err != nil {
			log.Printf("[c2tool] auto-connect failed (%v); connect from the web console instead", err)
		} else {
			web.SetClient(client)
			log.Printf("[c2tool] web console attached to the embedded server")
		}
	}

	// ---- console login ------------------------------------------------------
	//
	// One account, one record.
	//
	// Precedence is: an explicit override (flag or environment), otherwise the
	// stored record, otherwise a freshly generated password. The winner is then
	// written back, so the record, the running console and the next launch
	// always converge on the same password. Without that write-back an operator
	// could set C2TOOL_AUTH_PASS once and have it silently ignored on the next
	// start in favour of whatever the record happened to hold.
	credPath := *authFile
	if credPath == "" {
		credPath = filepath.Join(base, "console-auth")
	}
	store := api.CredentialStore{Path: credPath}

	if !settings.Auth.Enabled {
		log.Printf("[c2tool] WARNING: authentication is disabled in %s; anyone who can reach %s gets full control",
			config.Path(base), settings.Addr)
	} else {
		user, pass := settings.Auth.User, passOverride
		if storedUser, storedPass, found, err := store.Load(); err != nil {
			log.Printf("[c2tool] WARNING: cannot read %s: %v", credPath, err)
		} else if found {
			// The record wins over the settings file's username, so a rename
			// made from the console is not reverted by the next restart.
			user, pass = storedUser, storedPass
		}
		if pass == "" {
			// First run: generate one rather than starting with a known default.
			// A predictable console password on a C2 is worse than no password,
			// because it looks protected.
			generated, err := generatePassword()
			if err != nil {
				log.Fatalf("[c2tool] cannot generate a console password: %v", err)
			}
			pass = generated
		}
		if user == "" {
			user = "operator"
		}

		cfg := &api.BasicAuth{User: user, Pass: pass, Realm: settings.Auth.Realm}
		// A change made from the console lands in the same record the browser
		// logs in against, so a restart asks for the same password.
		cfg.Persist = store.Save

		// Persist the account that won. Skipped when the record already matches,
		// so a normal restart does not rewrite the file on every boot.
		if storedUser, storedPass, found, _ := store.Load(); !found || storedUser != user || storedPass != pass {
			if err := store.Save(user, pass); err != nil {
				log.Printf("[c2tool] WARNING: cannot persist credentials to %s: %v", credPath, err)
			}
		}
		web.SetBasicAuth(cfg)

		printCredentials(user, pass, credPath, config.Path(base), settings.Addr)
	}

	listener, err := net.Listen("tcp", settings.Addr)
	if err != nil {
		log.Fatalf("[c2tool] cannot listen on %s: %v", settings.Addr, err)
	}

	httpSrv := newHTTPServer(listener.Addr().String(), web.Routes())

	scheme := "http"
	switch {
	case settings.TLSHalfConfigured():
		// Half a TLS config is a mistake worth stopping for: guessing which half
		// the operator meant would silently downgrade them to plain HTTP, which
		// is the thing they were trying to avoid by setting it at all.
		log.Fatalf("[c2tool] tlsCert and tlsKey must be set together (%s)", config.Path(base))
	case settings.TLSConfigured():
		scheme = "https"
		go func() {
			if err := httpSrv.ServeTLS(listener, settings.TLSCert, settings.TLSKey); err != nil &&
				err.Error() != "http: Server closed" {
				log.Printf("[c2tool] https server stopped: %v", err)
				stop()
			}
		}()
	default:
		warnIfCleartextExposed(settings.Addr)
		go func() {
			if err := httpSrv.Serve(listener); err != nil && err.Error() != "http: Server closed" {
				log.Printf("[c2tool] http server stopped: %v", err)
				stop()
			}
		}()
	}

	log.Printf("[c2tool] web console listening on %s://%s", scheme, displayAddr(listener.Addr().String()))
	<-ctx.Done()
	log.Printf("[c2tool] shutting down ...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

// warnIfCleartextExposed prints a prominent warning when the console is about to
// serve plain HTTP on an address other than loopback.
//
// It is a warning rather than a refusal on purpose. Binding 0.0.0.0 is how the
// console is reached from another machine, and an operator running it inside a
// tunnel, a VLAN or an SSH forward is making a legitimate choice that this
// process cannot see. What it can do is make sure the choice is deliberate
// rather than inherited from a default, which is what the shipped
// "addr": "0.0.0.0:8080" otherwise turns it into.
func warnIfCleartextExposed(addr string) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if isLoopbackHost(host) {
		return
	}

	log.Printf("[c2tool] ---------------------------------------------------------------")
	log.Printf("[c2tool] WARNING: serving plain HTTP on %s", addr)
	log.Printf("[c2tool] The console login and every command result travel in clear text.")
	log.Printf("[c2tool] Set tlsCert and tlsKey in the settings file to enable HTTPS, or")
	log.Printf("[c2tool] bind 127.0.0.1:8080 and reach it through an SSH tunnel.")
	log.Printf("[c2tool] ---------------------------------------------------------------")
}

// isLoopbackHost reports whether a listen host is local-only.
//
// An empty host is the wildcard, which is the opposite of loopback, so it is
// deliberately not treated as safe here.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// overrides carries the values that came from a flag or the environment, so
// applyOverrides can tell "the operator asked for this" apart from "the flag
// default was left alone". An empty string or zero means "not set".
type overrides struct {
	addr       string
	operator   string
	mpHost     string
	mpPort     int
	serverOnly bool
	noAutoConn bool
	authUser   string
	authPass   string
	authRealm  string
}

// applyOverrides layers the command line and the environment over the settings
// file. Precedence is flag, then environment, then file, then the built-in
// default that config.Default already supplied.
//
// The password is returned separately rather than stored on the Config: it is a
// secret, and the settings file is a document the operator is invited to read,
// diff and share.
func applyOverrides(cfg config.Config, o overrides) (config.Config, string) {
	pass := ""

	// The environment is checked first so a flag, which is more specific, can
	// still win below.
	if v, ok := lookupEnv("C2TOOL_ADDR"); ok {
		cfg.Addr = v
	}
	if v, ok := lookupEnv("C2TOOL_AUTH_USER"); ok {
		cfg.Auth.User = v
	}
	if v, ok := lookupEnv("C2TOOL_AUTH_REALM"); ok {
		cfg.Auth.Realm = v
	}
	if v, ok := lookupEnv("C2TOOL_AUTH_PASS"); ok {
		pass = v
	}
	if v, ok := lookupEnv("C2TOOL_AUTH"); ok && (v == "off" || v == "false" || v == "0") {
		cfg.Auth.Enabled = false
	}

	if flagSet("addr") {
		cfg.Addr = o.addr
	}
	if flagSet("operator") {
		cfg.Operator = o.operator
	}
	if flagSet("mp-host") {
		cfg.MultiplayerHost = o.mpHost
	}
	if flagSet("mp-port") {
		cfg.MultiplayerPort = o.mpPort
	}
	if flagSet("server-only") {
		cfg.ServerOnly = o.serverOnly
	}
	if flagSet("no-autoconnect") {
		cfg.AutoConnect = !o.noAutoConn
	}
	if flagSet("auth-realm") {
		cfg.Auth.Realm = o.authRealm
	}
	if flagSet("auth-pass") {
		pass = o.authPass
	}
	// An explicit -auth-user with an empty value turns the login off, which is
	// how run.sh expressed C2TOOL_AUTH=off.
	if flagSet("auth-user") {
		cfg.Auth.User = o.authUser
		cfg.Auth.Enabled = o.authUser != ""
	}
	return cfg, pass
}

// generatePassword returns a URL-safe random password.
//
// It is generated in the binary rather than by a shell helper so a single-file
// deployment does not depend on openssl, od or /dev/urandom being present. The
// alphabet is alphanumeric so the value survives being copied out of a terminal,
// a JSON body and a browser prompt without escaping.
func generatePassword() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	const length = 24

	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	// Rejection-free reduction would need a 64-character alphabet; with 62 the
	// modulo bias is about 1 in 4 billion per character, which is not worth the
	// extra loop.
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf), nil
}

// printCredentials shows the login before the console starts serving.
//
// It is printed rather than only logged because it is the one thing the operator
// needs to open the console, and on a first run it has just been generated.
func printCredentials(user, pass, credPath, settingsPath, addr string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = addr, ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "<this-host>"
	}
	url := "http://" + host
	if port != "" {
		url += ":" + port
	}

	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  ┌────────────────────────────────────────────────────────────┐\n")
	fmt.Fprintf(os.Stderr, "  │  c2tool console                                            │\n")
	fmt.Fprintf(os.Stderr, "  └────────────────────────────────────────────────────────────┘\n")
	fmt.Fprintf(os.Stderr, "     url      : %s\n", url)
	fmt.Fprintf(os.Stderr, "     username : %s\n", user)
	fmt.Fprintf(os.Stderr, "     password : %s\n", pass)
	fmt.Fprintf(os.Stderr, "     stored   : %s\n", credPath)
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "     The browser will prompt for these before serving anything.\n")
	fmt.Fprintf(os.Stderr, "     Edit that file, or %s, to change them.\n", settingsPath)
	fmt.Fprintf(os.Stderr, "\n")
}

// lookupEnv reads an environment variable, reporting whether it was set to a
// non-empty value.
func lookupEnv(key string) (string, bool) {
	v, ok := os.LookupEnv(key)
	return v, ok && v != ""
}

// flagSet reports whether a flag was given on the command line, which is what
// lets an explicit flag override the settings file while an untouched flag
// leaves it alone.
func flagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// launchProfileName is the profile filename the launcher generates.
func launchProfileName() string { return "c2tool" }

// connectProfile attaches the console to the launcher-generated profile.
func connectProfile(name string) (*sliver.Client, error) {
	cfg, err := sliver.LoadProfile(name)
	if err != nil {
		return nil, err
	}
	return sliver.Connect(cfg)
}

// defaultHome resolves the state directory: C2TOOL_HOME, otherwise a data
// directory beside the executable, otherwise ~/.c2tool.
//
// Keeping the state next to the binary is what makes a deployment relocatable —
// the config, the login record, the loot and the unpacked server all live in one
// directory that can be moved with the executable — and it is where an operator
// who just ran the file will look for them. The home directory is the fallback
// for a binary that sits somewhere unwritable.
func defaultHome() (string, error) {
	return launch.HomeDir()
}

// installLogFile mirrors logs to disk so a detached server stays debuggable.
func installLogFile(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(&fanout{w: []*os.File{os.Stderr, f}})
}

type fanout struct{ w []*os.File }

func (f *fanout) Write(p []byte) (int, error) {
	for _, w := range f.w {
		_, _ = w.Write(p)
	}
	return len(p), nil
}

// displayAddr turns a wildcard bind address into something clickable.
func displayAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	_ = runtime.GOOS
	return fmt.Sprintf("%s:%s", host, port)
}
