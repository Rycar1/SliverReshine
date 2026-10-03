package sliver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

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

	switch {
	case strings.HasPrefix(module, "win-"):
		return detectPersistenceWindows(module, out, stderr, name)
	case strings.HasPrefix(module, "linux-"):
		return detectPersistenceLinux(module, out, name)
	}
	return false, ""
}

// detectPersistenceWindows answers presence for the Windows modules.
//
// It is the Windows half of detectPersistence, split out so the dispatcher
// stays a dispatcher. Module names are win-prefixed by construction, which is
// what the caller switches on.
func detectPersistenceWindows(module, out, stderr, name string) (bool, string) {
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
	}
	return false, ""
}

// detectPersistenceLinux answers presence for the POSIX modules.
func detectPersistenceLinux(module, out, name string) (bool, string) {
	switch module {
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
