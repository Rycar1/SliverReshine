package sliver

import (
	"strconv"
)

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
