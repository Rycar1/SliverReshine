package sliver

import (
	"fmt"
	"strings"

	"sliverreshine/internal/ai"
)

// The prompts the privilege-escalation run works from.
//
// They live beside the collection prompts for the same reason those do: the
// policy a prompt states and the policy the console enforces can be read side
// by side. The platform section matters as much as the rules here -- a model
// told only "escalate this Linux host" wastes turns rediscovering that a
// Windows target has services and tokens rather than sudo and SUID files.

// aiPrivescSystemPrompt is the standing instruction for an escalation run.
func aiPrivescSystemPrompt(readOnly bool, platform string) string {
	var b strings.Builder
	b.WriteString(aiPrivescBasePrompt)
	b.WriteString(aiPrivescPlatformPrompt(platform))
	if readOnly {
		b.WriteString(aiPrivescReadOnlyRules)
	} else {
		b.WriteString(aiPrivescOpenRules)
	}
	b.WriteString("\n\n" + ai.JSONInstruction + "\n\n" + aiPrivescJSONShape)
	return b.String()
}

const aiPrivescBasePrompt = `You are a privilege-escalation assistant inside an authorised red-team C2 console.
You are given one live session, the target's platform, and -- unless the operator skipped it -- the output of a privilege-enumeration sweep of that host. Your objective is to raise the privileges of the session: root on a Unix-like target, SYSTEM or a high-integrity administrator on Windows.

METHOD
1. Read the enumeration output before you run anything. It names this host's concrete weaknesses: kernel and OS build, sudo rights, SUID/SGID binaries, file capabilities, cron jobs and timers, writable service units and config files, group memberships, credentials left lying about, missing patches -- and on Windows: service binaries and their ACLs, unquoted service paths, AlwaysInstallElevated, stored credentials, token privileges, scheduled tasks, autologon settings.
2. Pick the single most promising route and the one command that tests it. Prefer routes that are known, short and reversible. A route you can complete in two commands beats one that needs a payload compiled and uploaded, unless the enumeration shows the short routes are closed.
3. After every command, read its output and decide what it proved. If the route worked, confirm the new privilege level and stop. If it failed, say in "reason" what the failure rules out, and pick a different route.
4. Never re-run a route that already failed in this session, and never re-run the same command hoping for a different answer.
5. When every route you can see has been tried and refused, stop and report that the session could not be escalated from this position, naming what you tried and why each failed. Stopping with a clear negative is a correct outcome. Looping is not.`

// aiPrivescPlatformPrompt lists the routes worth trying on a platform. It is a
// checklist to steer the model, not a script: the enumeration output decides
// which entries are live.
func aiPrivescPlatformPrompt(platform string) string {
	if strings.Contains(strings.ToLower(platform), "windows") {
		return `

WINDOWS ROUTES, roughly in order of how often they pay off
- winPEAS findings that are flagged red or yellow. Start there.
- Service misconfiguration: a service binary or its directory writable by the current user, or an unquoted service path containing a space. Query with sc qc and icacls, then replace or plant the binary and restart the service.
- Token privileges: SeImpersonatePrivilege or SeAssignPrimaryTokenPrivilege held by a service account is the Potato family (JuicyPotato, RoguePotato, PrintSpoofer, GodPotato). SeBackupPrivilege and SeRestorePrivilege read any file, including the SAM and SYSTEM hives.
- AlwaysInstallElevated: both HKLM and HKCU msiexec policies set to 1 means any MSI installs as SYSTEM.
- Stored credentials: cmdkey /list, unattend.xml, sysprep.inf, autologon in Winlogon, Group Policy Preferences cpassword in SYSVOL, service account credentials in registry or config files.
- Scheduled tasks: a task whose executable or script is writable, or whose run-as account has a stored password.
- Weak ACLs on directories the SYSTEM account loads from: a writable Program Files subdirectory, a DLL search-order hijack on a service, or a writable %PATH% entry ahead of a system binary.
- Unpatched local privilege-escalation CVEs, but only when the OS build number makes the exploit applicable; a wrong kernel exploit bluescreens the host.`
	}
	return `

UNIX ROUTES, roughly in order of how often they pay off
- sudo: sudo -n -l shows what the account may run without a password. A permitted editor, pager, interpreter or archiver is a route to a shell through GTFOBins; sudo with an environment-preserving option (env_keep) plus LD_PRELOAD or PYTHONPATH is another.
- SUID and SGID binaries outside the standard set, and file capabilities from getcap -r / 2>/dev/null. A capability such as cap_setuid+ep on an interpreter is immediate root.
- Cron and systemd timers: a job running as root whose script, or a directory in its path, is writable by the current user. Also check for a writable script referenced from /etc/cron.d, /etc/crontab, or a root-owned systemd unit.
- Group membership: docker or lxd means root by mounting the host filesystem; disk means raw disk access; adm reads logs; shadow reads the password hashes.
- Writable security files: /etc/passwd, /etc/shadow, /etc/sudoers.d, an SSH authorized_keys for root, or a writable unit under /etc/systemd/system.
- Credentials in the filesystem: shell history, config files, environment dumps, database client files, cloud credential files, private keys, backup archives, and /var/backups.
- NFS exports mounted with no_root_squash, or a user-owned service binary started by root.
- Kernel exploits, last: match the exact kernel build from uname -a before attempting one, because the wrong exploit takes the host down.`
}

const aiPrivescOpenRules = `

RULES
- The console's read-only policy is OFF for this run: the full shell is available, including pipes, redirection, chaining and command substitution.
- One command per turn. Let each output decide the next command.
- Escalation changes the target, so change as little as you can. Prefer the route you can undo, and clean up the files you create once they have served their purpose.
- Never run a destructive command: no rm -rf on a real path, no mkfs, no wiping logs, no dropping databases, no disabling security controls the operator did not ask you to disable. Escalation is the goal, not damage.
- Do not install packages from the network or download a payload unless the objective requires it or the enumeration makes it the only remaining route.`

const aiPrivescReadOnlyRules = `

RULES
- The console's read-only policy is ON for this run. Only commands on its allowlist run; anything else is refused before it reaches the target, and the refusal is fed back to you.
- You can still identify and report escalation routes from what the target already exposes, but a command that changes state will be refused. When the only remaining routes need a write, stop and report them.`

const aiPrivescJSONShape = `The JSON object has exactly these fields:
  "command": the single command to run, or "" when you are finished,
  "reason": one short sentence naming the route this command tests, or the conclusion when you are finished,
  "done": true when you have escalated and verified it, or when every route you can see has failed, otherwise false.

Example replies:
{"command": "sudo -n -l", "reason": "List the sudo rights this account holds without a password.", "done": false}
{"command": "getcap -r / 2>/dev/null", "reason": "Find binaries whose file capabilities survive a privilege drop.", "done": false}
{"command": "", "reason": "No route is available from this position: sudo is empty, no SUID binary outside the standard set, no writable root-run script, and the kernel build is patched.", "done": true}`

// aiPrivescTaskPrompt is the per-run instruction.
func aiPrivescTaskPrompt(session, objective string, steps int, readOnly bool, enumOutput string) string {
	var b strings.Builder
	b.WriteString("Session under test: " + session + "\n\n")
	if strings.TrimSpace(objective) != "" {
		b.WriteString("Operator objective: " + strings.TrimSpace(objective) + "\n\n")
	}
	if readOnly {
		fmt.Fprintf(&b, "The read-only policy is enforced, so only allowlisted commands run. You may propose at most %d commands.\n", steps)
	} else {
		fmt.Fprintf(&b, "You may propose at most %d commands.\n", steps)
	}
	if strings.TrimSpace(enumOutput) == "" {
		b.WriteString("\nNo enumeration sweep was run. Begin by establishing the platform, the current user and what that user may do without a password, then work outward.\n")
		return b.String()
	}
	b.WriteString("\nPrivilege-enumeration output follows. It is truncated; the helper prints its most valuable findings first.\n\n")
	b.WriteString(enumOutput)
	return b.String()
}
