// Command sliverreshine ships a self-contained C2 console: it provisions a Sliver
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
	"strconv"
	"syscall"
	"time"

	"sliverreshine/internal/ai"
	"sliverreshine/internal/config"
	"sliverreshine/internal/launch"
	"sliverreshine/internal/ui/api"
	"sliverreshine/internal/ui/sliver"
)

// options is the command line as parsed, before anything has been read from
// disk. Keeping it in a struct is what lets run() be driven by a test or by a
// future subcommand without going through the global flag set.
type options struct {
	addr       string
	home       string
	operator   string
	mpHost     string
	mpPort     int
	serverOnly bool
	noAutoConn bool
	authUser   string
	authPass   string
	authRealm  string
	authFile   string
}

func main() {
	run(parseFlags())
}

// parseFlags registers the command line flags and parses them.
func parseFlags() options {
	var o options
	flag.StringVar(&o.addr, "addr", "", "HTTP listen address for the web console (overrides the settings file)")
	flag.StringVar(&o.home, "home", "", "base directory for state and profiles (default: <exe dir>/data, then ~/.sliverreshine)")
	flag.StringVar(&o.operator, "operator", "", "operator name recorded in the generated profile")
	flag.StringVar(&o.mpHost, "mp-host", "", "host the embedded Sliver gRPC listener binds to")
	flag.IntVar(&o.mpPort, "mp-port", 0, "port the embedded Sliver gRPC listener binds to")
	flag.BoolVar(&o.serverOnly, "server-only", false, "run only the embedded C2 server and skip the web console")
	flag.BoolVar(&o.noAutoConn, "no-autoconnect", false, "do not attach the console to the generated profile on startup")
	flag.StringVar(&o.authUser, "auth-user", "", "HTTP Basic Auth username for the web console (empty disables auth)")
	flag.StringVar(&o.authPass, "auth-pass", "", "HTTP Basic Auth password for the web console")
	flag.StringVar(&o.authRealm, "auth-realm", "", "Basic Auth realm shown in the browser prompt (default: a generic string)")
	flag.StringVar(&o.authFile, "auth-file", "", "credential file holding the console account (default: <home>/console-auth)")
	flag.Parse()
	return o
}

// signalChannel carries the interrupts that stop the launcher: the first one
// starts the orderly shutdown, the second one skips the rest of it.
//
// It is one channel read by two receivers in turn, not two channels. A signal
// is delivered to *every* channel registered for it, so a second channel
// created next to signal.NotifyContext's received the first press as well: the
// "second Ctrl-C" fast path fired on the first one, the grace period became
// unreachable, and a single Ctrl-C left through forceQuitOnSignal. Reading the
// presses in order from one channel keeps them distinct by construction.
//
// The buffer holds both presses, so an operator who hits Ctrl-C twice while a
// slow step is running still gets the fast path instead of losing the second
// press while the first is being handled.
func signalChannel() chan os.Signal {
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	return sig
}

// beginShutdownOnFirstSignal starts the orderly shutdown when the first
// interrupt arrives, and leaves every later one to forceQuitOnSignal.
//
// Splitting this out is what makes the two presses distinct: whoever reads the
// channel first owns the press that begins the shutdown, and waitForForcedQuit
// can only ever observe a later one.
func beginShutdownOnFirstSignal(sig <-chan os.Signal, cancel context.CancelFunc) {
	<-sig
	cancel()
}

// waitForForcedQuit blocks until the shutdown has begun and the next interrupt
// arrives, and reports the signal that ended it.
//
// It is split out of forceQuitOnSignal so the ordering can be tested without
// os.Exit in the same function. The ordering is the whole point: the press that
// starts the shutdown is consumed by beginShutdownOnFirstSignal, so the read
// here is always a later one. Waiting before ctx.Done() would take that first
// press instead and turn a single Ctrl-C into an immediate exit.
func waitForForcedQuit(ctx context.Context, sig <-chan os.Signal) os.Signal {
	<-ctx.Done()
	return <-sig
}

// forceQuitOnSignal turns the next interrupt into an immediate exit.
//
// Shutdown is supposed to be bounded, but "supposed to" is not a guarantee an
// operator can act on: this is what makes the second Ctrl-C work, and the
// daemon is stopped first so the fast path does not leave it behind.
func forceQuitOnSignal(ctx context.Context, sig <-chan os.Signal, srv *launch.Server) {
	s := waitForForcedQuit(ctx, sig)
	log.Printf("[sliverreshine] %v during shutdown; forcing exit", s)
	if srv != nil {
		srv.Stop()
	}
	os.Exit(0)
}

// run is main without the flag parsing: it provisions the state directory,
// starts the embedded C2 server, serves the web console and blocks until the
// process is asked to stop.
//
// It is split out of main so the phases read as a list of what a launch does,
// rather than as two hundred lines of one function. Each phase below owns one
// resource and its own failure message.
func run(o options) {
	base := resolveHome(o.home)
	settings, passOverride := provision(base, o)

	// The first Ctrl-C has to begin the orderly shutdown, and only the next one
	// may skip it. Both presses are read from this one channel, in order: the
	// goroutine below takes the first, forceQuitOnSignal takes whatever follows.
	sig := signalChannel()
	defer signal.Stop(sig)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go beginShutdownOnFirstSignal(sig, cancel)

	// ---- embedded C2 server -------------------------------------------------
	//
	// The daemon's lifetime is owned here, by the defer below. Stopping it
	// anywhere earlier -- in particular inside startEmbeddedServer, which is
	// called from here -- killed the server the moment the launcher had it
	// running. The only symptom was a console that served its UI and said the
	// server was unreachable, because auto-connect timed out ten seconds later
	// against a process that no longer existed.
	srv := startEmbeddedServer(ctx, base, settings)
	defer srv.Stop()

	// A second Ctrl-C has to kill the process, and a hard ceiling has to leave
	// even when nobody presses anything again. Both are armed here, once the
	// daemon exists, because both clean it up on the way out.
	go forceQuitOnSignal(ctx, sig, srv)

	// The ceiling: leave instead of sitting on "shutting down ..." forever.
	// Stop runs first so the backstop does not orphan the daemon it exists to
	// clean up.
	shutdownDone := make(chan struct{})
	defer close(shutdownDone)
	go func() {
		select {
		case <-ctx.Done():
		case <-shutdownDone:
			return
		}
		select {
		case <-shutdownDone:
		case <-time.After(forceExitGrace):
			log.Printf("[sliverreshine] shutdown did not finish within %s; forcing exit", forceExitGrace)
			srv.Stop()
			os.Exit(0)
		}
	}()

	// The launcher can move the daemon off a busy gRPC port, so everything
	// after this point follows the port it actually bound.
	settings.MultiplayerPort = srv.MultiplayerPort()

	if settings.ServerOnly {
		log.Printf("[sliverreshine] server-only mode, gRPC on %s:%d; Ctrl-C to stop",
			settings.MultiplayerHost, settings.MultiplayerPort)
		<-ctx.Done()
		return
	}

	// ---- web console --------------------------------------------------------
	web := newConsole(base, settings)

	// Checked before the listener exists, so a refusal leaves no port bound and
	// nothing to clean up. The warning inside serveConsole is advisory; this is
	// the version an operator can opt into when they want the process held to
	// their TLS policy rather than merely reminded of it.
	if err := checkCleartextPolicy(settings); err != nil {
		srv.Stop()
		log.Fatalf("[sliverreshine] %v", err)
	}

	listener, err := listenConsole(settings.Addr)
	if err != nil {
		srv.Stop()
		log.Fatalf("[sliverreshine] cannot listen on %s: %v", settings.Addr, err)
	}
	// The banner and the browser prompt have to name the address that is really
	// serving; after a port fallback the configured port is no longer it.
	settings.Addr = listener.Addr().String()

	configureAuth(web, base, settings, passOverride, o.authFile)
	serveConsole(ctx, cancel, web, base, settings, listener)
}

// resolveHome resolves the state directory and creates it.
//
// It is the first thing a launch does, because everything else -- the settings
// file, the login record, the unpacked server -- is written underneath it.
func resolveHome(flagHome string) string {
	base := flagHome
	if base == "" {
		dir, err := defaultHome()
		if err != nil {
			log.Fatalf("[sliverreshine] cannot determine home directory: %v", err)
		}
		base = dir
	}
	// The state directory is resolved to an absolute path before anything is
	// derived from it. Sliver builds every path it hands the Go toolchain --
	// GOROOT, GOPATH, GOCACHE -- from the directory it is started with, and the
	// toolchain refuses a relative GOPATH ("GOPATH entry is relative; must be
	// absolute path"). A launch with a relative -home therefore started, served
	// the console, and then failed every implant build with "Invalid compiler
	// target", because `go tool dist list` never ran. Resolving here fixes every
	// derived path at once, and is a no-op for the absolute paths the default
	// home and a normal deployment already use.
	abs, err := filepath.Abs(base)
	if err != nil {
		log.Fatalf("[sliverreshine] cannot resolve %s to an absolute path: %v", base, err)
	}
	base = abs
	if err := os.MkdirAll(base, 0o700); err != nil {
		log.Fatalf("[sliverreshine] cannot create %s: %v", base, err)
	}
	return base
}

// provision writes the first-run files and layers the command line and the
// environment over the settings file.
//
// The password is returned separately rather than stored on the Config: it is a
// secret, and the settings file is a document the operator is invited to read,
// diff and share.
func provision(base string, o options) (config.Config, string) {
	// ---- first-run provisioning --------------------------------------------
	//
	// Everything the operator is expected to be able to edit is written here, on
	// the first start, and never again. A restart must not overwrite an edit.
	settings, created, err := config.Ensure(base)
	if err != nil {
		log.Fatalf("[sliverreshine] %v", err)
	}
	if created {
		log.Printf("[sliverreshine] wrote default settings to %s", config.Path(base))
	}
	if wroteReadme, err := config.EnsureReadme(base); err != nil {
		log.Printf("[sliverreshine] WARNING: cannot write %s: %v", filepath.Join(base, config.ReadmeName), err)
	} else if wroteReadme {
		log.Printf("[sliverreshine] wrote deployment notes to %s", filepath.Join(base, config.ReadmeName))
	}

	settings, passOverride := applyOverrides(settings, overrides{
		addr:       o.addr,
		operator:   o.operator,
		mpHost:     o.mpHost,
		mpPort:     o.mpPort,
		serverOnly: o.serverOnly,
		noAutoConn: o.noAutoConn,
		authUser:   o.authUser,
		authPass:   o.authPass,
		authRealm:  o.authRealm,
	})
	settings = settings.Normalize()

	installLogFile(filepath.Join(base, "sliverreshine.log"))
	return settings, passOverride
}

// startEmbeddedServer starts the embedded Sliver server and reports the operator
// profile it generated.
func startEmbeddedServer(ctx context.Context, base string, settings config.Config) *launch.Server {
	srv, err := launch.Start(ctx, launch.Options{
		StateDir:        filepath.Join(base, "sliver"),
		ConfigDir:       filepath.Join(base, "configs"),
		Operator:        settings.Operator,
		MultiplayerHost: settings.MultiplayerHost,
		MultiplayerPort: settings.MultiplayerPort,
		LogPath:         filepath.Join(base, "sliver-server.log"),
	})
	if err != nil {
		log.Fatalf("[sliverreshine] %v", err)
	}

	// No Stop here: the server's lifetime belongs to run(), which defers it.
	// A defer in this function fires on return -- immediately after the daemon
	// starts -- and kills it. That is the bug this comment exists to prevent
	// from coming back; TestEmbeddedServerLifecycleBelongsToRun enforces it.
	profile, _ := launch.ReadProfile(srv.ProfilePath)
	if profile != nil {
		log.Printf("[sliverreshine] operator %q -> %s", profile.Operator, profile.Path)
	}
	return srv
}

// newConsole builds the web console and, when the settings ask for it, attaches
// it to the profile the launcher just wrote.
func newConsole(base string, settings config.Config) *api.Server {
	web := api.New()
	// The console reads and writes its own settings file, so it has to know
	// where that file lives. Without this the AI panel answers "this console
	// does not manage a settings file" and the read-only policy can only be
	// changed by editing the file and restarting.
	web.SetSettingsHome(base)
	// The process-identification endpoint receives the target's process list, so
	// whether it is used at all is a deployment decision rather than a per-click
	// one. Empty keeps the built-in public default; "off" disables it.
	web.SetAVLookupURL(settings.AVLookupURL)
	// Model access is off unless the settings name an endpoint and a model. The
	// key is resolved here so the environment lookup happens once, at startup,
	// rather than on every request.
	web.SetAIConfig(aiConfigFromSettings(settings.AI))
	// The read-only policy for the assistant is a deployment setting, on by
	// default; see config.AIConfig.ReadOnly.
	web.SetAIReadOnly(settings.AI.ReadOnly)
	if settings.AutoConnect {
		client, err := connectProfile(launchProfileName())
		if err != nil {
			log.Printf("[sliverreshine] auto-connect failed (%v); connect from the web console instead", err)
		} else {
			web.SetClient(client)
			log.Printf("[sliverreshine] web console attached to the embedded server")
		}
	}
	return web
}

// aiConfigFromSettings turns the settings file into the ai package's config.
//
// The split exists because the two packages want different things: the settings
// file stores where the key comes from, and the client wants the key itself.
// Resolving it here keeps the environment lookup at the process boundary.
func aiConfigFromSettings(cfg config.AIConfig) ai.Config {
	return ai.Config{
		BaseURL:  cfg.BaseURL,
		Model:    cfg.Model,
		APIKey:   cfg.ResolveKey(),
		Timeout:  cfg.Timeout(),
		Thinking: cfg.Thinking,
	}
}

// configureAuth resolves the console account and installs it on the server.
//
// One account, one record. The precedence is: an explicit override (flag or
// environment), otherwise the stored record, otherwise a freshly generated
// password. The winner is then written back, so the record, the running console
// and the next launch always converge on the same password. Without that
// write-back an operator could set SLIVERRESHINE_AUTH_PASS once and have it silently
// ignored on the next start in favour of whatever the record happened to hold.
func configureAuth(web *api.Server, base string, settings config.Config, passOverride, authFile string) {
	credPath := authFile
	if credPath == "" {
		credPath = filepath.Join(base, "console-auth")
	}
	store := api.CredentialStore{Path: credPath}

	if !settings.Auth.Enabled {
		log.Printf("[sliverreshine] WARNING: authentication is disabled in %s; anyone who can reach %s gets full control",
			config.Path(base), settings.Addr)
		return
	}

	// Precedence, and the order matters:
	//
	//   username: the stored record wins, so a rename made from the console is
	//             not reverted by the next restart.
	//   password: an explicit -auth-pass / SLIVERRESHINE_AUTH_PASS wins over the
	//             record, then the record, then a generated one.
	//
	// The password used to follow the username's rule -- the record overwrote
	// whatever the operator passed. That made `-auth-pass X` silently do
	// nothing once a record existed, so an operator who set a password and
	// restarted was still asked for the old one. From the outside that is
	// indistinguishable from the console changing their password, and it is
	// the opposite of what the comment below claims the write-back prevents.

	resolved := resolveAccount(settings.Auth.User, passOverride, store)
	user, pass, wasGenerated := resolved.user, resolved.pass, resolved.generated
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
			log.Printf("[sliverreshine] WARNING: cannot persist credentials to %s: %v", credPath, err)
		}
	}
	web.SetBasicAuth(cfg)

	printCredentials(user, pass, credPath, config.Path(base), settings.Addr, consoleScheme(settings), wasGenerated)
}

// listenConsole binds the console listener, moving to a random free port when
// the configured one is already held.
//
// A taken console port used to be fatal: the process logged "cannot listen" and
// exited, even though the embedded server it had just started was healthy. The
// fallback keeps the console reachable, and the port it chose is what the banner
// prints.
//
// Only the port changes. The host is preserved exactly as configured, including
// the wildcard 0.0.0.0 default, because the fallback is about the port being
// free and never about how far the console is exposed.
func listenConsole(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, nil
	}

	port, pickErr := launch.FreeRandomPort(hostOf(addr))
	if pickErr != nil {
		return nil, err
	}
	fallback, ok := consoleFallbackAddr(addr, port)
	if !ok {
		return nil, err
	}
	ln, fbErr := net.Listen("tcp", fallback)
	if fbErr != nil {
		return nil, err
	}
	log.Printf("[sliverreshine] console address %s is in use; switched to %s", addr, fallback)
	return ln, nil
}

// consoleFallbackAddr replaces the port in addr while keeping the host, so a
// fallback never widens or narrows the interface the console listens on.
func consoleFallbackAddr(addr string, port int) (string, bool) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", false
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), true
}

// hostOf returns the host part of a host:port address, or the address itself
// when it does not split (which the caller treats as "no fallback possible").
func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// serveConsole serves the console on an already-bound listener and shuts it
// down when the context is cancelled.
//
// The listener is passed in rather than created here so the address it ended up
// on is known before the login banner is printed. That address can differ from
// the configured one: listenConsole moves to a free port when the configured
// port is taken, and the banner has to name the port that is really serving.
//
// stop is the cancel function for that context: a serve error in either branch
// below has to bring the whole process down, and cancelling the context is how
// that is signalled.
func serveConsole(ctx context.Context, stop context.CancelFunc, web *api.Server, base string, settings config.Config, listener net.Listener) {
	httpSrv := newHTTPServer(listener.Addr().String(), web.Routes())

	scheme := "http"
	switch {
	case settings.TLSHalfConfigured():
		// Half a TLS config is a mistake worth stopping for: guessing which half
		// the operator meant would silently downgrade them to plain HTTP, which
		// is the thing they were trying to avoid by setting it at all.
		log.Fatalf("[sliverreshine] tlsCert and tlsKey must be set together (%s)", config.Path(base))
	case settings.TLSConfigured():
		scheme = "https"
		go func() {
			if err := httpSrv.ServeTLS(listener, settings.TLSCert, settings.TLSKey); err != nil &&
				err.Error() != "http: Server closed" {
				log.Printf("[sliverreshine] https server stopped: %v", err)
				stop()
			}
		}()
	default:
		warnIfCleartextExposed(settings.Addr)
		go func() {
			if err := httpSrv.Serve(listener); err != nil && err.Error() != "http: Server closed" {
				log.Printf("[sliverreshine] http server stopped: %v", err)
				stop()
			}
		}()
	}

	log.Printf("[sliverreshine] web console listening on %s://%s", scheme, displayAddr(listener.Addr().String()))
	<-ctx.Done()
	log.Printf("[sliverreshine] shutting down ...")

	shutdownConsole(httpSrv)
}

// checkCleartextPolicy refuses to start when the operator has asked to be held
// to TLS and the configuration would nevertheless serve plain HTTP.
//
// An operator who sets requireTLS has said, in the settings file, that they
// never want this console on the wire in the clear. Starting anyway and printing
// a warning would be exactly the failure mode they set the flag to avoid -- a
// deployment that looks protected and is not. The only two ways out are the two
// the error names: configure TLS, or bind an address that never leaves the host.
//
// Loopback is allowed through because there is no wire. A tunnel, a VLAN or a
// local reverse proxy terminates the encryption somewhere this process cannot
// see, but it terminates it before the traffic reaches an interface it does not
// own, which is the property that matters here.
func checkCleartextPolicy(settings config.Config) error {
	if !settings.RequireTLS || settings.TLSConfigured() {
		return nil
	}

	host, _, err := net.SplitHostPort(settings.Addr)
	if err != nil {
		host = settings.Addr
	}
	if isLoopbackHost(host) {
		return nil
	}

	return fmt.Errorf("requireTLS is set but the console would serve plain HTTP on %s: "+
		"set tlsCert and tlsKey to serve HTTPS, or bind 127.0.0.1:8080 and reach it "+
		"through an SSH tunnel", settings.Addr)
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
//
// The operator who does not want the choice left open at all sets requireTLS in
// the settings file, and checkCleartextPolicy turns this warning into a refusal
// to start.
func warnIfCleartextExposed(addr string) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if isLoopbackHost(host) {
		return
	}

	const rule = "================================================================"
	log.Printf("[sliverreshine] %s", rule)
	log.Printf("[sliverreshine] WARNING: UNSENCRYPTED CONSOLE ON A NETWORK INTERFACE")
	log.Printf("[sliverreshine] %s", rule)
	log.Printf("[sliverreshine] Listening on %s over plain HTTP.", addr)
	log.Printf("[sliverreshine] The console login and every command result travel in clear text,")
	log.Printf("[sliverreshine] and HTTP Basic is base64, not encryption: anyone on this network can")
	log.Printf("[sliverreshine] read both, and can reuse the login to run commands on every implant.")
	log.Printf("[sliverreshine]")
	log.Printf("[sliverreshine] Three ways to fix it, in order of how much they cost:")
	log.Printf("[sliverreshine]   1. bind 127.0.0.1:8080 here, and put an SSH tunnel in front:")
	log.Printf("[sliverreshine]        ssh -L 8080:127.0.0.1:8080 user@this-host")
	log.Printf("[sliverreshine]   2. set tlsCert and tlsKey in the settings file to serve HTTPS")
	log.Printf("[sliverreshine]   3. set requireTLS to make this console refuse to start like this")
	log.Printf("[sliverreshine] %s", rule)
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
	if v, ok := lookupEnv("SLIVERRESHINE_ADDR"); ok {
		cfg.Addr = v
	}
	if v, ok := lookupEnv("SLIVERRESHINE_AUTH_USER"); ok {
		cfg.Auth.User = v
	}
	if v, ok := lookupEnv("SLIVERRESHINE_AUTH_REALM"); ok {
		cfg.Auth.Realm = v
	}
	if v, ok := lookupEnv("SLIVERRESHINE_AUTH_PASS"); ok {
		pass = v
	}
	if v, ok := lookupEnv("SLIVERRESHINE_AUTH"); ok && (v == "off" || v == "false" || v == "0") {
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
	// how run.sh expressed SLIVERRESHINE_AUTH=off.
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

// consoleScheme reports the scheme the console will actually serve on.
//
// It duplicates one branch of the serving switch below, and that duplication is
// deliberate: the banner is printed before the listener is created, so the
// decision has to be available here. Keeping the predicate in one function is
// what stops the two from disagreeing -- which is exactly the bug this replaces,
// where the banner said http:// unconditionally.
func consoleScheme(settings config.Config) string {
	if settings.TLSConfigured() {
		return "https"
	}
	return "http"
}

// printCredentials shows the login before the console starts serving.
//
// It is printed rather than only logged because it is the one thing the operator
// needs to open the console, and on a first run it has just been generated.
// printCredentials shows the login before the console starts serving.
//
// The frame is plain ASCII on purpose. It used to be box-drawing characters
// written as raw UTF-8, with nothing setting the console code page -- so on any
// Windows console that is not already UTF-8 (the default on a Chinese install is
// 936) each byte was rendered in the local code page and the frame arrived as
// mojibake around otherwise-readable credentials. Switching the code page would
// fix it here and leave every other writer to remember; ASCII cannot be got
// wrong, and this is a five-line box.
//
// scheme is passed in rather than assumed: the URL was hardcoded to http://, so
// an operator running the console over TLS was handed a link that does not
// connect.
// showPassword is true only when this run generated the password. The stored
// password is not reprinted on every start: the banner goes to stderr, and a
// supervisor (systemd, journald, a container log driver, a service wrapper)
// captures stderr into a store whose access control is not the 0600 credential
// file. Repeating a live secret into that store on every boot is the opposite of
// what the 0600 file is for, and the operator can always read the file.
func printCredentials(user, pass, credPath, settingsPath, addr, scheme string, showPassword bool) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = addr, ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "<this-host>"
	}
	url := scheme + "://" + host
	if port != "" {
		url += ":" + port
	}

	const rule = "+" + "-----------------------------------------------------------" + "+"
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  %s\n", rule)
	fmt.Fprintf(os.Stderr, "  | sliverreshine console                                      |\n")
	fmt.Fprintf(os.Stderr, "  %s\n", rule)
	fmt.Fprintf(os.Stderr, "     url      : %s\n", url)
	fmt.Fprintf(os.Stderr, "     username : %s\n", user)
	if showPassword {
		fmt.Fprintf(os.Stderr, "     password : %s\n", pass)
	} else {
		fmt.Fprintf(os.Stderr, "     password : (unchanged; see %s)\n", credPath)
	}
	fmt.Fprintf(os.Stderr, "     stored   : %s\n", credPath)
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "     The browser will prompt for these before serving anything.\n")
	fmt.Fprintf(os.Stderr, "     Edit that file, or %s, to change them.\n", settingsPath)
	fmt.Fprintf(os.Stderr, "\n")
}

// resolvedAccount is the console account that won, and whether this run invented
// the password.
type resolvedAccount struct {
	user      string
	pass      string
	generated bool
}

// resolveAccount applies the credential precedence.
//
// Split out of main so it can be tested. The ordering is the whole content of
// this function and it is easy to get wrong in a way nothing reports:
//
//   - The username comes from the stored record when there is one, so a rename
//     made in the Settings panel is not reverted by the next restart.
//   - The password is the explicit override first. The record only fills in when
//     the operator supplied nothing.
//
// The password used to follow the username's rule -- the record overwrote
// whatever was passed -- which made `-auth-pass X` and SLIVERRESHINE_AUTH_PASS silently
// do nothing once a record existed. An operator who set a password and restarted
// was still asked for the old one, which is indistinguishable from the console
// changing their password.
//
// A record that cannot be read is reported and otherwise ignored rather than
// being fatal: an unreadable file should not stop a console the operator may be
// trying to recover.
func resolveAccount(settingsUser, passOverride string, store api.CredentialStore) resolvedAccount {
	out := resolvedAccount{user: settingsUser, pass: passOverride}

	if storedUser, storedPass, found, err := store.Load(); err != nil {
		log.Printf("[sliverreshine] WARNING: cannot read %s: %v", store.Path, err)
	} else if found {
		out.user = storedUser
		if out.pass == "" {
			out.pass = storedPass
		}
	}

	if out.pass == "" {
		// First run: generate one rather than starting with a known default. A
		// predictable console password on a C2 is worse than no password,
		// because it looks protected.
		generated, err := generatePassword()
		if err != nil {
			log.Fatalf("[sliverreshine] cannot generate a console password: %v", err)
		}
		out.pass = generated
		out.generated = true
	}

	if out.user == "" {
		out.user = "operator"
	}
	return out
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
func launchProfileName() string { return "sliverreshine" }

// connectProfile attaches the console to the launcher-generated profile.
func connectProfile(name string) (*sliver.Client, error) {
	cfg, err := sliver.LoadProfile(name)
	if err != nil {
		return nil, err
	}
	return sliver.Connect(cfg)
}

// defaultHome resolves the state directory: SLIVERRESHINE_HOME, otherwise a data
// directory beside the executable, otherwise ~/.sliverreshine.
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
	return fmt.Sprintf("%s:%s", host, port)
}
