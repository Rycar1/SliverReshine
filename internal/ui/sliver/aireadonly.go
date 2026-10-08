package sliver

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Read-only collection policy.
//
// The AI collector runs commands on a live target, and the one thing it must
// never do is change that target. A denylist of dangerous verbs cannot promise
// that: the ways to write a file, start a service or kill a process are
// open-ended, and a model that invents a plausible-looking command would slip
// through. The policy is therefore an allowlist -- a command the operator has
// not been running by hand for years is refused -- plus a hard rejection of the
// shell syntax that turns a read into an arbitrary write.
//
// Three layers, each of which can only refuse:
//
//  1. Shell syntax. Redirection, command substitution, backgrounding, piping
//     and chaining are rejected outright. "cat /etc/passwd > /tmp/x" is not a
//     read, and neither is "whoami && rm -rf /". There is no pipe support at
//     all, because a pipe needs a shell to interpret it and handing the model a
//     shell is the whole thing this exists to avoid; a command that wants to
//     filter output asks the command to filter it (grep, findstr) instead.
//  2. The command allowlist. The first word, with any directory prefix and
//     .exe suffix stripped, must be a command that is read-only by nature.
//  3. Per-command rules. "net user" lists accounts while "net user bob /add"
//     creates one; "reg query" reads the registry while "reg add" writes it.
//     Those are separated by requiring a subcommand or by denying flags.
//
// The policy is deliberately conservative. A command it refuses is one the
// operator can still run by hand; a command it allows is one the AI has already
// decided to run without asking.

// ErrNotReadOnly is the sentinel every refusal wraps, so a caller can tell a
// policy rejection from an execution failure with errors.Is.
var ErrNotReadOnly = errors.New("not a read-only command")

// NotReadOnlyError is a refusal carrying the reason, so the console can show
// the operator which command was rejected and why.
type NotReadOnlyError struct {
	Command string
	Reason  string
}

func (e *NotReadOnlyError) Error() string {
	return fmt.Sprintf("refusing %q: %s", e.Command, e.Reason)
}

func (e *NotReadOnlyError) Unwrap() error { return ErrNotReadOnly }

func refuseReadOnly(command, format string, args ...any) error {
	return &NotReadOnlyError{Command: command, Reason: fmt.Sprintf(format, args...)}
}

// IsNotReadOnly reports whether err is a read-only policy refusal.
func IsNotReadOnly(err error) bool { return errors.Is(err, ErrNotReadOnly) }

// maxReadOnlyCommandLen bounds a command before it is parsed. A collection
// command is a handful of words; anything longer is a payload, not a probe.
const maxReadOnlyCommandLen = 1024

// shellMetacharacters are the characters that let one command become another.
// Each is rejected wherever it appears, quoted or not: the parser below does
// not honour shell quoting, so a quoted ">" would otherwise reach a target that
// does.
const shellMetacharacters = "|><&;`$%()"

// nullRedirectToken is the one redirection the policy tolerates: sending stderr
// to /dev/null. It cannot write anywhere -- /dev/null discards -- and the
// collector captures stderr on its own, so the token is dropped before the
// syntax scan. That keeps "find / -name x 2>/dev/null" a read instead of
// costing the model a step, and it keeps the literal token out of the argv the
// target receives.
//
// It is dropped only as a whole whitespace-delimited word. "2>/dev/nullx",
// "2>/dev/null;" and "2>/dev/null|nc" are left intact and still refused, so the
// strip cannot smuggle a second operator into the command.
const nullRedirectToken = "2>/dev/null"

func stripNullRedirects(command string) string {
	if !strings.Contains(command, nullRedirectToken) {
		return command
	}
	kept := make([]string, 0, 8)
	for _, word := range strings.Fields(command) {
		if word == nullRedirectToken {
			continue
		}
		kept = append(kept, word)
	}
	return strings.Join(kept, " ")
}

// readOnlyRule is the per-command part of the policy.
type readOnlyRule struct {
	// sub, when non-empty, restricts the second word to one of these values.
	// It is what separates "net user" (list) from "net user bob /add" (create).
	sub map[string]bool
	// deny lists words that must not appear anywhere in the segment. It covers
	// the commands whose dangerous form is a flag rather than a subcommand,
	// such as "schtasks /create".
	deny []string
	// denyContains lists substrings that must not appear in any word. It covers
	// assignments -- "set VAR=value" -- where the danger is inside a token
	// rather than being a token of its own.
	denyContains []string
	// positional, when set, decides whether a non-flag word may appear. It
	// covers the commands whose bare argument is itself the mutation -- such as
	// "hostname newname" or "date 010112002020" -- and the ones where a
	// positional argument is normal but must look a certain way, such as
	// "date +%F". A nil value allows any positional argument.
	positional func(word string) bool
}

// noPositionalArg refuses every non-flag word: the command takes flags only.
func noPositionalArg(string) bool { return false }

// plusPositionalArg accepts only a word beginning with "+", which is how date
// and its relatives are asked for a format rather than given a new value.
func plusPositionalArg(word string) bool { return strings.HasPrefix(word, "+") }

func words(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

// readOnlyCommands is the allowlist. It is the union of the reconnaissance
// commands an operator reaches for on a POSIX and on a Windows target, because
// the collector is told the platform at run time and the policy is not.
var readOnlyCommands = map[string]readOnlyRule{
	// --- identity, host and platform ---
	"whoami":     {},
	"hostname":   {positional: noPositionalArg},
	"id":         {},
	"uname":      {},
	"uptime":     {},
	"date":       {deny: []string{"-s", "--set"}, positional: plusPositionalArg},
	"ver":        {},
	"systeminfo": {},
	"getmac":     {},
	"nltest":     {denyContains: []string{"/sc_reset", "/sc_delete", "/dsderegdns", "/dsregdns"}},
	"quser":      {},
	"qwinsta":    {},
	"klist":      {deny: []string{"purge"}},
	"cmdkey":     {denyContains: []string{"/add", "/delete", "/generic", "/pass"}},
	"who":        {},
	"w":          {},
	"last":       {},
	"lastlog":    {},
	"lastb":      {},
	"groups":     {},
	"tlist":      {},

	// --- filesystem, read only ---
	"pwd":      {},
	"ls":       {},
	"dir":      {},
	"cat":      {},
	"type":     {},
	"find":     {deny: []string{"-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprintf", "-fls"}},
	"findstr":  {},
	"grep":     {},
	"egrep":    {},
	"fgrep":    {},
	"awk":      {},
	"cut":      {},
	"sort":     {deny: []string{"-o", "--output"}},
	"uniq":     {},
	"head":     {},
	"tail":     {},
	"tr":       {},
	"wc":       {},
	"strings":  {},
	"file":     {deny: []string{"-C", "--compile"}},
	"stat":     {},
	"readlink": {},
	"vol":      {},
	"label":    {positional: noPositionalArg},

	// --- processes and services ---
	"ps":        {},
	"tasklist":  {},
	"wmic":      {deny: []string{"call", "delete", "set", "create", "terminate"}},
	"sc":        {sub: words("query", "qc", "queryex", "getdisplayname", "getkeyname", "enumdepend")},
	"systemctl": {sub: words("status", "show", "list-units", "list-unit-files", "is-active", "is-enabled", "cat", "list-timers", "list-sockets")},
	"service":   {sub: words("status", "--status-all")},
	"schtasks":  {deny: []string{"/create", "/delete", "/change", "/run", "/end", "/xml"}},
	"crontab":   {deny: []string{"-r", "-e", "-u"}},
	"atq":       {},
	"journalctl": {deny: []string{"--vacuum-time", "--vacuum-size", "--vacuum-files", "--rotate", "--flush", "--sync"},
		denyContains: []string{"="}},
	"driverquery": {},

	// --- network ---
	"netstat":    {},
	"ipconfig":   {},
	"ifconfig":   {deny: []string{"up", "down", "mtu", "promisc", "arp", "add", "del"}},
	"arp":        {deny: []string{"-d", "-s", "--delete", "--set"}},
	"route":      {deny: []string{"add", "del", "delete", "change", "flush"}},
	"nslookup":   {},
	"dig":        {},
	"host":       {},
	"ping":       {},
	"traceroute": {},
	"tracert":    {},
	"pathping":   {},
	"ss":         {},
	"ip": {sub: words("addr", "a", "route", "r", "link", "l", "neigh", "n", "-br"),
		deny: []string{"set", "add", "del", "delete", "change", "replace", "flush", "up", "down"}},
	"lsof": {},
	"net": {sub: words("user", "localgroup", "group", "share", "view", "accounts", "config", "statistics", "session", "time"),
		deny: []string{"/add", "/delete", "/active", "/password", "/random", "/comment", "/times",
			"/workstations", "/domain", "/expires", "/fullname", "/homedir", "/profilepath", "/scriptpath",
			"/countrycode", "/set", "/setsntp", "/grant", "/users", "/unlimited", "/cache", "/autodisconnect",
			"/minpwlen", "/maxpwage", "/minpwage", "/uniquepw", "/forcelogoff", "/addname", "/delname"},
		denyContains: []string{"=", ":"}},

	// --- registry ---
	"reg": {sub: words("query")},

	// --- environment ---
	"env":      {denyContains: []string{"="}},
	"printenv": {},
	"set":      {denyContains: []string{"="}},

	// --- system inventory ---
	"free":        {},
	"df":          {},
	"du":          {},
	"mount":       {deny: []string{"-a", "--all"}, positional: noPositionalArg},
	"lsblk":       {},
	"lsmod":       {},
	"lscpu":       {},
	"lsusb":       {},
	"lspci":       {},
	"dmidecode":   {},
	"hostnamectl": {},
	"dmesg":       {deny: []string{"-C", "-c", "--clear", "-D", "--console-off"}},
	"sysctl":      {deny: []string{"-w", "--write"}, denyContains: []string{"="}},
	"getent":      {},
	"fsutil": {sub: words("fsinfo", "volume", "file"),
		deny: []string{"createnew", "setvaliddata", "setzerodata", "setshortname", "seteof", "allocate"}},
	"rpm":     {sub: words("-qa", "-q", "-qi", "--query", "--list")},
	"dpkg":    {sub: words("-l", "--list", "-s", "--status")},
	"pip":     {sub: words("list", "show", "freeze")},
	"docker":  {sub: words("ps", "images", "inspect", "version", "info")},
	"kubectl": {sub: words("get", "describe", "version")},
}

// ReadOnlyAllowlist returns the allowlisted command names, sorted. It is what
// the collector prompt is built from, so the model is told the policy instead
// of having to guess it.
func ReadOnlyAllowlist() []string {
	out := make([]string, 0, len(readOnlyCommands))
	for name := range readOnlyCommands {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// CheckReadOnlyCommand reports whether command is a read-only probe the
// collector may run, and returns the executable and arguments to spawn when it
// is.
//
// Validation and parsing are one function on purpose: if they were separate, a
// command could be approved by one and executed differently by the other.
func CheckReadOnlyCommand(command string) (path string, args []string, err error) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return "", nil, refuseReadOnly(command, "empty command")
	}
	if len(cmd) > maxReadOnlyCommandLen {
		return "", nil, refuseReadOnly(command, "command is longer than %d characters", maxReadOnlyCommandLen)
	}
	if strings.ContainsAny(cmd, "\r\n") {
		return "", nil, refuseReadOnly(command, "command contains a line break")
	}
	// "2>/dev/null" is dropped before the syntax scan: it is the only redirect
	// that cannot write anywhere, and a model reaching for it is suppressing
	// stderr, not staging a write.
	cmd = stripNullRedirects(cmd)
	if i := strings.IndexAny(cmd, shellMetacharacters); i >= 0 {
		return "", nil, refuseReadOnly(command,
			"%q is shell syntax that could redirect, chain or substitute another command", string(cmd[i]))
	}

	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return "", nil, refuseReadOnly(command, "empty command")
	}

	name := baseCommandName(fields[0])
	if name == "" {
		return "", nil, refuseReadOnly(command, "could not read a command name from %q", fields[0])
	}
	rule, ok := readOnlyCommands[name]
	if !ok {
		return "", nil, refuseReadOnly(command, "%q is not on the read-only allowlist", name)
	}

	rest := fields[1:]
	if len(rule.sub) > 0 {
		if len(rest) == 0 {
			return "", nil, refuseReadOnly(command, "%q needs a read-only subcommand", name)
		}
		if !rule.sub[strings.ToLower(rest[0])] {
			return "", nil, refuseReadOnly(command, "%q %s is not a read-only form of %q", name, rest[0], name)
		}
	}
	for _, word := range rest {
		lower := strings.ToLower(word)
		for _, bad := range rule.deny {
			if lower == strings.ToLower(bad) {
				return "", nil, refuseReadOnly(command, "%q with %s is not read-only", name, word)
			}
		}
		for _, bad := range rule.denyContains {
			if strings.Contains(lower, bad) {
				return "", nil, refuseReadOnly(command, "%q with %s is not read-only", name, word)
			}
		}
		if rule.positional != nil && !strings.HasPrefix(word, "-") && !rule.positional(word) {
			return "", nil, refuseReadOnly(command, "%q does not take a positional argument here", name)
		}
	}

	return fields[0], rest, nil
}

// baseCommandName reduces a word to the command it names: the last path
// element, with a Windows executable suffix removed. "/usr/bin/id" and
// "C:\\Windows\\System32\\whoami.exe" become "id" and "whoami", so the
// allowlist is matched on the command rather than on how it was spelled.
func baseCommandName(word string) string {
	w := strings.TrimSpace(word)
	if w == "" {
		return ""
	}
	if i := strings.LastIndexAny(w, `/\`); i >= 0 {
		w = w[i+1:]
	}
	lower := strings.ToLower(w)
	for _, ext := range []string{".exe", ".com", ".bat", ".cmd"} {
		if strings.HasSuffix(lower, ext) {
			w = w[:len(w)-len(ext)]
			break
		}
	}
	return strings.ToLower(w)
}
