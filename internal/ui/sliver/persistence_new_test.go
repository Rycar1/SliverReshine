package sliver

import (
	"strings"
	"testing"
)

// newPersistenceModules are the mechanisms added after the first catalog:
// account creation, boot and keep-alive tasks, and the watchdog variants.
var newPersistenceModules = []struct {
	id       string
	platform string
	payload  string
	name     string
}{
	{"win-local-account", platformWindows, "Sup3rSecret!", "svc_backup"},
	{"win-schtask-boot", platformWindows, `C:\Windows\Temp\agent.exe`, "Updater"},
	{"win-watchdog", platformWindows, `C:\Windows\Temp\agent.exe`, "Updater"},
	{"linux-cron-interval", platformLinux, "/tmp/agent", "agent"},
	{"linux-watchdog", platformLinux, "/tmp/agent", "agent"},
	{"linux-systemd-user", platformLinux, "/tmp/agent", "agent"},
}

// Every added module must be in the catalog and must build all three commands.
// A module reachable from the UI but missing one of them would install and then
// be unremovable, or never appear in the inventory.
func TestNewModulesBuildEveryCommand(t *testing.T) {
	catalog := map[string]PersistenceModule{}
	for _, m := range PersistenceModules() {
		catalog[m.ID] = m
	}

	for _, tc := range newPersistenceModules {
		m, ok := catalog[tc.id]
		if !ok {
			t.Errorf("module %q is not in the catalog", tc.id)
			continue
		}
		if m.Technique == "" || m.Description == "" || m.Name == "" {
			t.Errorf("module %q is missing display metadata: %+v", tc.id, m)
		}

		if _, err := installCommand(tc.platform, tc.id, tc.payload, tc.name); err != nil {
			t.Errorf("%s: installCommand: %v", tc.id, err)
		}
		if _, err := removeCommand(tc.platform, tc.id, tc.payload, tc.name); err != nil {
			t.Errorf("%s: removeCommand: %v", tc.id, err)
		}
		if _, err := inspectCommand(tc.platform, tc.id, tc.name); err != nil {
			t.Errorf("%s: inspectCommand: %v", tc.id, err)
		}
		if got := locationFor(tc.id, tc.name); got == tc.id {
			t.Errorf("%s: location falls through to the module id; add a case to locationFor", tc.id)
		}
	}
}

// The commands reach a live target, so the parts that decide what actually
// happens on the host are pinned literally. Asserting only that the payload
// appears would pass for a command that splits a spaced path into two
// arguments, or that runs in the wrong security context.
func TestNewModuleCommandDetails(t *testing.T) {
	joined := func(platform, module, payload, name string) string {
		argv, err := installCommand(platform, module, payload, name)
		if err != nil {
			t.Fatalf("%s: %v", module, err)
		}
		return strings.Join(argv, " ")
	}

	cases := []struct {
		module, platform, payload, name string
		wantSubstrings                  []string
	}{
		{
			"win-local-account", platformWindows, "Sup3rSecret!", "svc_backup",
			[]string{"net user svc_backup", "/add", "net localgroup Administrators svc_backup"},
		},
		{
			// SYSTEM before logon is the whole point of this one.
			"win-schtask-boot", platformWindows, `C:\Windows\Temp\agent.exe`, "Updater",
			[]string{"/sc onstart", "/ru SYSTEM"},
		},
		{
			"win-watchdog", platformWindows, `C:\Windows\Temp\agent.exe`, "Updater",
			[]string{"/sc minute", "/mo 5"},
		},
		{
			"linux-cron-interval", platformLinux, "/tmp/agent", "agent",
			[]string{"*/5 * * * * /tmp/agent", "grep -v", "crontab -"},
		},
		{
			// setsid detaches the loop so it outlives the exec; the pgrep count
			// at the end is what turns a silent failure into a visible one.
			"linux-watchdog", platformLinux, "/tmp/agent", "agent",
			[]string{"setsid", "loop.sh", "pgrep -fc"},
		},
		{
			// The user manager is the unprivileged option, so --user and the
			// user-scoped WantedBy are both required for it to be startable.
			"linux-systemd-user", platformLinux, "/tmp/agent", "agent",
			[]string{"systemctl --user", "WantedBy=default.target", "~/.config/systemd/user/agent.service"},
		},
	}

	for _, tc := range cases {
		got := joined(tc.platform, tc.module, tc.payload, tc.name)
		for _, want := range tc.wantSubstrings {
			if !strings.Contains(got, want) {
				t.Errorf("%s: command %q is missing %q", tc.module, got, want)
			}
		}
	}
}

// An install that reports success without installing anything is the worst
// failure mode here, because the operator stops looking. The watchdog counts
// the loop it just started, so a missing setsid shows up as a zero.
func TestWatchdogInstallVerifiesItself(t *testing.T) {
	argv, err := installCommand(platformLinux, "linux-watchdog", "/tmp/agent", "agent")
	if err != nil {
		t.Fatalf("installCommand: %v", err)
	}
	script := strings.Join(argv, " ")

	if !strings.Contains(script, "sleep 1") {
		t.Error("the watchdog install does not give the loop a moment to start")
	}
	if !strings.Contains(script, "pgrep -fc") {
		t.Error("the watchdog install never confirms the loop is running")
	}
	if !strings.Contains(script, "|| true") {
		t.Error("a zero count would exit non-zero and read as an unknown result")
	}
}

// Removal must stop the loop, not just delete its script: a supervisor left
// running keeps respawning the payload and the operator cannot see why.
func TestWatchdogRemovalStopsTheLoop(t *testing.T) {
	argv, err := removeCommand(platformLinux, "linux-watchdog", "/tmp/agent", "agent")
	if err != nil {
		t.Fatalf("removeCommand: %v", err)
	}
	script := strings.Join(argv, " ")

	if !strings.Contains(script, "pkill") {
		t.Error("removal deletes the script but leaves the respawn loop running")
	}
	if !strings.Contains(script, "rm -rf") {
		t.Error("removal does not delete the loop script")
	}
	// The pattern must match the real command line. Single-quoting the
	// unexpanded $HOME form would hand pkill a literal that matches nothing.
	if strings.Contains(script, "'$HOME") {
		t.Error("pkill pattern is single-quoted, so $HOME never expands and nothing is killed")
	}
}

// The artifact name lands in unquoted tokens and filesystem paths for these
// modules, so it has to stay a plain identifier.
func TestNewNameScopedModulesRejectUnsafeNames(t *testing.T) {
	for _, module := range []string{
		"win-local-account", "win-schtask-boot", "win-watchdog",
		"linux-watchdog", "linux-systemd-user",
	} {
		if !nameModules[module] {
			t.Errorf("%s interpolates its name but is not in nameModules", module)
			continue
		}
		platform := platformWindows
		if strings.HasPrefix(module, "linux-") {
			platform = platformLinux
		}
		for _, bad := range []string{`a;rm -rf /`, `a b`, `a"b`, "a$b", "a|b", ""} {
			if _, err := installCommand(platform, module, "/tmp/p", bad); err == nil {
				t.Errorf("%s accepted unsafe name %q", module, bad)
			}
		}
	}
}

// Detection has to distinguish "not there" from "could not tell", because the
// UI shows the first as a clean host and the second as an unknown.
func TestNewModuleDetection(t *testing.T) {
	cases := []struct {
		label   string
		module  string
		stdout  string
		stderr  string
		status  uint32
		name    string
		want    bool
		wantSub string
	}{
		{
			"account present", "win-local-account",
			"User name                    svc_backup\r\nFull Name                    \r\n",
			"", 0, "svc_backup", true, "svc_backup",
		},
		{
			"account absent", "win-local-account", "",
			"The user name could not be found.\r\n", 1, "svc_backup", false, "",
		},
		{
			// Without a name there is nothing to look up, so the module must
			// report unknown rather than claim the host has an account.
			"account unnamed", "win-local-account",
			"User name                    svc_backup\r\n", "", 0, "", false, "",
		},
		{
			"boot task present", "win-schtask-boot",
			`<?xml version="1.0"?><Task><Triggers><BootTrigger><Enabled>true</Enabled></BootTrigger></Triggers></Task>`,
			"", 0, "Updater", true, "Boot",
		},
		{
			// The task exists, but its trigger is not the boot trigger. All three
			// task modules name their task from the same field, so without the
			// trigger check a keep-alive task loaded this row as installed.
			"boot row must reject a minute task", "win-schtask-boot",
			`<?xml version="1.0"?><Task><Triggers><TimeTrigger><Repetition><Interval>PT5M</Interval></Repetition></TimeTrigger></Triggers></Task>`,
			"", 0, "Updater", false, "",
		},
		{
			"logon row must reject a boot task", "win-schtask",
			`<?xml version="1.0"?><Task><Triggers><BootTrigger/></Triggers></Task>`,
			"", 0, "Updater", false, "",
		},
		{
			"logon task present", "win-schtask",
			`<?xml version="1.0"?><Task><Triggers><LogonTrigger/></Triggers></Task>`,
			"", 0, "Updater", true, "Logon",
		},
		{
			"watchdog task present, interval reported", "win-watchdog",
			`<?xml version="1.0"?><Task><Triggers><TimeTrigger><Repetition><Interval>PT5M</Interval></Repetition></TimeTrigger></Triggers></Task>`,
			"", 0, "Updater", true, "PT5M",
		},
		{
			"watchdog row must reject a boot task", "win-watchdog",
			`<?xml version="1.0"?><Task><Triggers><BootTrigger/></Triggers></Task>`,
			"", 0, "Updater", false, "",
		},
		{
			"boot task missing", "win-schtask-boot", "",
			"ERROR: The system cannot find the file specified.", 1, "Updater", false, "",
		},
		{
			"interval cron present", "linux-cron-interval",
			"SHELL=/bin/sh\n*/5 * * * * /tmp/agent\n", "", 0, "", true, "*/5",
		},
		{
			// An @reboot entry is a different module and must not satisfy this.
			"interval cron only reboot", "linux-cron-interval",
			"SHELL=/bin/sh\n@reboot /tmp/agent\n", "", 0, "", false, "",
		},
		{
			"watchdog running", "linux-watchdog", "1\n", "", 0, "agent", true, "1",
		},
		{
			// The script can survive a killed loop, so the process count is the
			// evidence, not the file.
			"watchdog not running", "linux-watchdog", "0\n", "", 0, "agent", false, "",
		},
		{
			"user unit enabled", "linux-systemd-user", "enabled\n", "", 0, "agent", true, "enabled",
		},
		{
			"user unit disabled", "linux-systemd-user", "disabled\n", "", 0, "agent", false, "",
		},
	}

	for _, tc := range cases {
		got, detail := detectPersistence(tc.module, tc.stdout, tc.stderr, tc.status, tc.name)
		if got != tc.want {
			t.Errorf("%s: installed = %v, want %v", tc.label, got, tc.want)
			continue
		}
		if tc.wantSub != "" && !strings.Contains(detail, tc.wantSub) {
			t.Errorf("%s: detail %q does not mention %q", tc.label, detail, tc.wantSub)
		}
	}
}

// The inventory accepts a name from the query string, so it is the one entry
// point where an unvalidated value could reach a command line. validateName has
// to reject the same charset everywhere, or the inventory becomes the way in.
func TestValidateNameIsTheSharedGate(t *testing.T) {
	for _, ok := range []string{"agent", "svc_backup", "Updater-1", "a.b", "A1"} {
		if err := validateName(ok); err != nil {
			t.Errorf("validateName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{
		"", "   ", "a b", "a;b", "a&b", "a|b", "a`b", "a$b", "a'b", `a"b`,
		"a>b", "a<b", "a\\b", "a/b", "a:b", "a*b", "a\nb",
		strings.Repeat("a", 65),
	} {
		if err := validateName(bad); err == nil {
			t.Errorf("validateName(%q) = nil, want an error", bad)
		}
	}
}

// requireName exists to not get in the way: a module that does not interpolate
// a name must keep accepting an empty one, or the unnamed inventory pass could
// not run at all.
func TestRequireNameOnlyGuardsNameScopedModules(t *testing.T) {
	if err := requireName("linux-cron", ""); err != nil {
		t.Errorf("a module that ignores the name rejected an empty one: %v", err)
	}
	if err := requireName("win-watchdog", ""); err == nil {
		t.Error("a name-scoped module accepted an empty name")
	}
	if err := requireName("win-watchdog", "good-name"); err != nil {
		t.Errorf("a name-scoped module rejected a valid name: %v", err)
	}
}
