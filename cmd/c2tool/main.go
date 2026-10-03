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
	flag.StringVar(&o.home, "home", "", "base directory for state and profiles (default: <exe dir>/data, then ~/.c2tool)")
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ---- embedded C2 server -------------------------------------------------
	srv := startEmbeddedServer(ctx, base, settings)
	defer srv.Stop()

	if settings.ServerOnly {
		log.Printf("[c2tool] server-only mode, gRPC on %s:%d; Ctrl-C to stop",
			settings.MultiplayerHost, settings.MultiplayerPort)
		<-ctx.Done()
		return
	}

	// ---- web console --------------------------------------------------------
	web := newConsole(settings)
	configureAuth(web, base, settings, passOverride, o.authFile)
	serveConsole(ctx, stop, web, base, settings)
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
			log.Fatalf("[c2tool] cannot determine home directory: %v", err)
		}
		base = dir
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		log.Fatalf("[c2tool] cannot create %s: %v", base, err)
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

	installLogFile(filepath.Join(base, "c2tool.log"))
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
		log.Fatalf("[c2tool] %v", err)
	}
	defer srv.Stop()

	profile, _ := launch.ReadProfile(srv.ProfilePath)
	if profile != nil {
		log.Printf("[c2tool] operator %q -> %s", profile.Operator, profile.Path)
	}
	return srv
}

// newConsole builds the web console and, when the settings ask for it, attaches
// it to the profile the launcher just wrote.
func newConsole(settings config.Config) *api.Server {
	web := api.New()
	// The process-identification endpoint receives the target's process list, so
	// whether it is used at all is a deployment decision rather than a per-click
	// one. Empty keeps the built-in public default; "off" disables it.
	web.SetAVLookupURL(settings.AVLookupURL)
	if settings.AutoConnect {
		client, err := connectProfile(launchProfileName())
		if err != nil {
			log.Printf("[c2tool] auto-connect failed (%v); connect from the web console instead", err)
		} else {
			web.SetClient(client)
			log.Printf("[c2tool] web console attached to the embedded server")
		}
	}
	return web
}

// configureAuth resolves the console account and installs it on the server.
//
// One account, one record. The precedence is: an explicit override (flag or
// environment), otherwise the stored record, otherwise a freshly generated
// password. The winner is then written back, so the record, the running console
// and the next launch always converge on the same password. Without that
// write-back an operator could set C2TOOL_AUTH_PASS once and have it silently
// ignored on the next start in favour of whatever the record happened to hold.
func configureAuth(web *api.Server, base string, settings config.Config, passOverride, authFile string) {
	credPath := authFile
	if credPath == "" {
		credPath = filepath.Join(base, "console-auth")
	}
	store := api.CredentialStore{Path: credPath}

	if !settings.Auth.Enabled {
		log.Printf("[c2tool] WARNING: authentication is disabled in %s; anyone who can reach %s gets full control",
			config.Path(base), settings.Addr)
		return
	}

	// Precedence, and the order matters:
	//
	//   username: the stored record wins, so a rename made from the console is
	//             not reverted by the next restart.
	//   password: an explicit -auth-pass / C2TOOL_AUTH_PASS wins over the
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
			log.Printf("[c2tool] WARNING: cannot persist credentials to %s: %v", credPath, err)
		}
	}
	web.SetBasicAuth(cfg)

	printCredentials(user, pass, credPath, config.Path(base), settings.Addr, consoleScheme(settings), wasGenerated)
}

// serveConsole binds the listener, serves the console and shuts it down when the
// context is cancelled.
//
// stop is the cancel function for that context: a serve error in either branch
// below has to bring the whole process down, and cancelling the context is how
// that is signalled.
func serveConsole(ctx context.Context, stop context.CancelFunc, web *api.Server, base string, settings config.Config) {
	// ---- cleartext policy ---------------------------------------------------
	//
	// Checked before the listener exists, so a refusal leaves no port bound and
	// nothing to clean up. The warning below is advisory; this is the version an
	// operator can opt into when they want the process to hold them to it.
	if err := checkCleartextPolicy(settings); err != nil {
		log.Fatalf("[c2tool] %v", err)
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
	log.Printf("[c2tool] %s", rule)
	log.Printf("[c2tool] WARNING: UNSENCRYPTED CONSOLE ON A NETWORK INTERFACE")
	log.Printf("[c2tool] %s", rule)
	log.Printf("[c2tool] Listening on %s over plain HTTP.", addr)
	log.Printf("[c2tool] The console login and every command result travel in clear text,")
	log.Printf("[c2tool] and HTTP Basic is base64, not encryption: anyone on this network can")
	log.Printf("[c2tool] read both, and can reuse the login to run commands on every implant.")
	log.Printf("[c2tool]")
	log.Printf("[c2tool] Three ways to fix it, in order of how much they cost:")
	log.Printf("[c2tool]   1. bind 127.0.0.1:8080 here, and put an SSH tunnel in front:")
	log.Printf("[c2tool]        ssh -L 8080:127.0.0.1:8080 user@this-host")
	log.Printf("[c2tool]   2. set tlsCert and tlsKey in the settings file to serve HTTPS")
	log.Printf("[c2tool]   3. set requireTLS to make this console refuse to start like this")
	log.Printf("[c2tool] %s", rule)
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
	fmt.Fprintf(os.Stderr, "  | c2tool console                                            |\n")
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
// whatever was passed -- which made `-auth-pass X` and C2TOOL_AUTH_PASS silently
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
		log.Printf("[c2tool] WARNING: cannot read %s: %v", store.Path, err)
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
			log.Fatalf("[c2tool] cannot generate a console password: %v", err)
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
