package sliver

import (
	"fmt"
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

	// NameZH and DescriptionZH are the Chinese catalog text. English stays in
	// Name/Description because those doubles as the identifier an operator
	// recognises across tools and write-ups; only the prose around it is localised.
	// They are empty when a module has no translation, and the UI then falls back
	// to the English pair rather than showing a blank card.
	NameZH        string `json:"nameZh,omitempty"`
	DescriptionZH string `json:"descriptionZh,omitempty"`
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

// moduleLocalized carries the Chinese name and description for every catalog
// entry, keyed by module id.
//
// A side table rather than fields on moduleCatalog: the catalog block is what
// gets diffed against upstream technique names and against the command builders,
// so interleaving a second language into it would make both harder to read. The
// cost is that an id can be added to the catalog and forgotten here, which is
// why persistence_i18n_test.go fails when the two sets diverge.
var moduleLocalized = map[string]struct{ Name, Description string }{
	"win-run-key": {
		Name:        "HKCU 运行键",
		Description: "在当前用户的 Run 键下添加开机自启项。",
	},
	"win-run-key-hklm": {
		Name:        "HKLM 运行键",
		Description: "在本地机器 Run 键下添加全局自启项，对所有用户生效。",
	},
	"win-startup-folder": {
		Name:        "启动文件夹",
		Description: "把载荷复制到当前用户的启动文件夹。",
	},
	"win-logon-script": {
		Name:        "登录脚本（HKCU）",
		Description: "把用户登录脚本指向载荷。每次交互登录都会执行，无需提权。",
	},
	"win-office-test": {
		Name:        "Office 测试键",
		Description: "在 Office 测试键下注册载荷，任何 Office 程序启动时都会执行。",
	},
	"win-winlogon-userinit": {
		Name:        "Winlogon Userinit",
		Description: "把载荷追加到 Winlogon Userinit 列表，每次登录都会执行。需要提权。",
	},
	"win-schtask": {
		Name:        "登录计划任务",
		Description: "注册一个在任意用户登录时运行载荷的计划任务。需要提权。",
	},
	"win-service": {
		Name:        "自启动服务",
		Description: "创建一个启动类型为「自动」的服务。",
	},
	"win-local-account": {
		Name:        "本地管理员账户",
		Description: "创建本地账户并加入 Administrators 组，得到一个比磁盘上任何载荷都更持久的登录凭据。",
	},
	"win-schtask-boot": {
		Name:        "开机计划任务（SYSTEM）",
		Description: "注册一个在系统启动时以 SYSTEM 身份运行载荷的计划任务，早于任何用户登录。",
	},
	"win-watchdog": {
		Name:        "保活计划任务",
		Description: "每五分钟重新拉起载荷，杀掉进程无法结束访问。",
	},
	"linux-cron": {
		Name:        "用户 cron @reboot",
		Description: "向当前用户的 crontab 追加一个 @reboot 任务。",
	},
	"linux-bashrc": {
		Name:        "Shell 配置文件钩子",
		Description: "向 ~/.bashrc 追加一行脱离终端的启动命令。",
	},
	"linux-systemd": {
		Name:        "systemd 服务单元",
		Description: "安装并启用一个自启动的 systemd 服务。",
	},
	"linux-cron-interval": {
		Name:        "定时 cron 保活",
		Description: "每五分钟重新拉起载荷，杀掉进程无法结束访问。",
	},
	"linux-watchdog": {
		Name:        "守护进程保活",
		Description: "运行一个脱离终端的后台循环，载荷挂掉后一分钟内自动重启。无需 root，也不依赖服务管理器。",
	},
	"linux-systemd-user": {
		Name:        "用户级 systemd 单元",
		Description: "安装一个用户级 systemd 单元，无需 root 即可自启动。",
	},
	"linux-ssh-authorized-keys": {
		Name:        "authorized_keys 公钥",
		Description: "向当前用户的 authorized_keys 追加一个 SSH 公钥。",
	},
}

// PersistenceModules returns the catalog. Platforms is deep-copied so a caller
// mutating the result cannot corrupt the package-level table, and the Chinese
// strings are attached here rather than stored on the table so that the table
// keeps exactly one language in it.
func PersistenceModules() []PersistenceModule {
	out := make([]PersistenceModule, 0, len(moduleCatalog))
	for _, m := range moduleCatalog {
		m.Platforms = append([]string(nil), m.Platforms...)
		if zh, ok := moduleLocalized[m.ID]; ok {
			m.NameZH = zh.Name
			m.DescriptionZH = zh.Description
		}
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

func supportsPlatform(m PersistenceModule, platform string) bool {
	for _, p := range m.Platforms {
		if p == platform {
			return true
		}
	}
	return false
}
