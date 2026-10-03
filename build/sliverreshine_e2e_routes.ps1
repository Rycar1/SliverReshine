# sliverreshine route-coverage e2e harness.
#
# Drives EVERY route in the route inventory CSV (default 183 rows) against an
# already-running sliverreshine console and classifies each one:
#
#   PASS           2xx, or a well-formed JSON error that is clearly about the
#                  REQUEST (400/404/422/... from a handler that ran).
#   ACCEPTED-ERROR a well-formed JSON error whose status is 5xx/502 but whose
#                  text is a request-level rejection (invalid session id,
#                  unknown name, unsupported value, ...). No panic, no stack.
#   UNEXPECTED     5xx with a Go panic/stack, 503 "not connected", 404 emitted
#                  by the mux fallback (i.e. the route pattern did NOT match),
#                  405, an empty body, or a non-JSON body where JSON was due.
#   SKIPPED        not called; a documented reason is recorded.
#
# Safety model -- the console under test is live and has a real Windows session:
#
#   * Read-only session routes run against the REAL session id.
#   * Every session route that can change state on the target runs against a
#     NON-EXISTENT session id ("no-such-thing"), so the gRPC call is rejected
#     with "Invalid session ID" before anything happens on the host.
#   * {name} {key} {taskID} {fwdID} {pivotID} {uuid} {iocID} {port} {serverID}
#     always get an obviously-fake value.
#   * Routes that are destructive at the SERVER/CONSOLE level (disconnect,
#     listener stop, prune, monitor toggle) are skipped outright and listed.
#   * Routes that would otherwise write junk into a shared store (credential
#     vault, monitoring providers) are driven with a body that fails the
#     handler's own decode/validation step, so the write path is never reached.
#
# ASCII only: Windows PowerShell 5.1 reads a BOM-less script as ANSI.
#
# Usage:
#   .\sliverreshine_e2e_routes.ps1
#   .\sliverreshine_e2e_routes.ps1 -Base http://127.0.0.1:18500 -Cred 'op:pass'
#   .\sliverreshine_e2e_routes.ps1 -Only 'beacons'      # run a subset by substring

param(
    [string]$Base = 'http://127.0.0.1:18500',
    [string]$Cred = 'op:debt-123456',
    [string]$SidFile = "$env:TEMP\debt_sid.txt",
    [string]$RoutesCsv = "$env:TEMP\routes_full.csv",
    [string]$OutDir = "$env:TEMP\sliverreshine-e2e-routes",
    [int]$TimeoutSec = 30,
    [int]$DelayMs = 25,
    [string]$Only = '',
    # Crashers: routes an earlier run proved will kill the embedded sliver-server.
    # Off by default so the sweep keeps a live console. With -IncludeCrashers they
    # are moved to the very end, so the crash cannot mask the other routes.
    [switch]$IncludeCrashers,
    # Console home directory, used only to snapshot sliverreshine.log and
    # sliver-server.log into the output directory when the run finishes.
    [string]$EntryPoint = ''
)

$ErrorActionPreference = 'Continue'
$curl = "$env:SystemRoot\system32\curl.exe"
if (-not (Test-Path $curl)) { $curl = 'curl.exe' }
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

# ---------------------------------------------------------------- inputs -----

$Sid = ''
if (Test-Path $SidFile) { $Sid = (Get-Content $SidFile -Raw).Trim() }
if ($Sid -eq '') { Write-Host "FATAL: no session id in $SidFile" -ForegroundColor Red; exit 2 }

if (-not (Test-Path $RoutesCsv)) { Write-Host "FATAL: no route inventory at $RoutesCsv" -ForegroundColor Red; exit 2 }
$routes = @(Import-Csv $RoutesCsv)

$Fake = 'no-such-thing'
$SiteFixture = 'sliverreshine-e2e-fixture'

# Session routes that only READ from the target: safe against the real id.
$realIdRoutes = @(
    'GET /api/sessions/{id}/fs'
    'GET /api/sessions/{id}/fs/pwd'
    'GET /api/sessions/{id}/fs/cat'
    'GET /api/sessions/{id}/fs/download'
    'GET /api/sessions/{id}/ifconfig'
    'GET /api/sessions/{id}/ps'
    'GET /api/sessions/{id}/netstat'
    'GET /api/sessions/{id}/env'
    'GET /api/sessions/{id}/screenshot'
    'GET /api/sessions/{id}/privs'
    'GET /api/sessions/{id}/token-owner'
    'GET /api/sessions/{id}/pivots/listeners'
    'GET /api/sessions/{id}/extensions'
    'GET /api/sessions/{id}/persistence'
    'GET /api/sessions/{id}/reg/subkeys'
    'GET /api/sessions/{id}/reg/values'
    'GET /api/sessions/{id}/reg/read'
    'GET /api/sessions/{id}/memfiles'
    'GET /api/sessions/{id}/wg/forwarders'
    'GET /api/sessions/{id}/wg/socks'
    'GET /api/sessions/{id}/rportfwd'
    'GET /api/sessions/{id}/wasm'
    'POST /api/sessions/{id}/ping'
    'POST /api/sessions/{id}/fs/grep'
)

# Routes that must not be called at all, with the reason recorded.
$skips = @{
    'POST /api/disconnect'              = 'clears the live console<->sliver-server client; every remaining session route would 503 and the live session id would become unusable'
    'DELETE /api/listeners/{id}'        = 'the only job on this console is the mTLS listener carrying the live session; stopping it would kill the C2 transport for the rest of the run'
    'POST /api/monitor/start'           = 'server-global watchtower toggle with no argument that makes it a no-op; the console does not report the current state, so a test call could silently change operator-visible behaviour'
    'POST /api/monitor/stop'            = 'server-global watchtower toggle with no argument that makes it a no-op; see monitor/start'
    'POST /api/sessions/prune'          = 'kills every session the server flags dead, server-wide and unbounded; there is no argument that limits it'
    'POST /api/sessions/{id}/backdoor'  = 'KNOWN SERVER KILLER #1 - verified 2026-10-01 20:40:15: this call made sliver-server panic and exit (nil pointer dereference, server/rpc/rpc-backdoor.go:60). Left out of the default sweep so the rest of the run keeps a live console.'
    'POST /api/shellcode/rdi'           = 'KNOWN SERVER KILLER #2 - verified 2026-10-01 20:51:43: this call made sliver-server panic and exit (slice bounds out of range [:64] with capacity 8, server/generate/srdi.go:1001). Left out of the default sweep so the rest of the run keeps a live console.'
}

# Routes held back from the default sweep and moved to the end when explicitly
# requested. Keyed the same way as $skips.
$deferred = @{
    'POST /api/sessions/{id}/backdoor' = $true
    'POST /api/shellcode/rdi'          = $true
}

# Per-route request plan.  body = JSON sent; extra = query string appended;
# note = why this shape was chosen; bin = the handler may answer with bytes.
$plan = @{}

function P([string]$k, [string]$body, [string]$note, [string]$extra = '', [bool]$bin = $false) {
    $script:plan[$k] = [pscustomobject]@{ body = $body; note = $note; extra = $extra; bin = $bin }
}

# --- console / connection ---------------------------------------------------
P 'POST /api/connect' '{}' 'empty content -> 400 before any dial; a real profile would replace the live client, so it is deliberately not sent'
P 'POST /api/profiles/{name}' '' 'non-existent profile -> LoadProfile error; never connects'

# --- session control (all against a fake session id) ------------------------
P 'POST /api/sessions/{id}/kill' '' 'fake session id: the RPC is rejected before the live session can be killed'
P 'POST /api/sessions/{id}/close' '' 'fake session id: closing the real session would end the run'

# --- filesystem -------------------------------------------------------------
P 'GET /api/sessions/{id}/fs' '' 'real session id: list current directory'
P 'GET /api/sessions/{id}/fs/pwd' '' 'real session id: read-only'
P 'POST /api/sessions/{id}/fs/cd' '{"path":"C:\\Windows"}' 'fake session id: cd mutates the target session cwd'
P 'GET /api/sessions/{id}/fs/cat' '' 'real session id: read-only download of a harmless system file' '?path=C:%5CWindows%5Cwin.ini' $true
P 'GET /api/sessions/{id}/fs/download' '' 'real session id: read-only download of a harmless system file' '?path=C:%5CWindows%5Cwin.ini' $true
P 'POST /api/sessions/{id}/fs/upload' '{"path":"C:\\Windows\\Temp\\sliverreshine-e2e-nope.txt","data":"QUJD"}' 'fake session id: upload would write to the target'
P 'POST /api/sessions/{id}/fs/mkdir' '{"path":"C:\\Windows\\Temp\\sliverreshine-e2e-nope"}' 'fake session id: mkdir would create a directory on the target'
P 'DELETE /api/sessions/{id}/fs' '' 'fake session id: rm would delete on the target' '?path=C:%5CWindows%5CTemp%5Csliverreshine-e2e-nope.txt'
P 'POST /api/sessions/{id}/fs/mv' '{"src":"C:\\Windows\\Temp\\sliverreshine-e2e-a","dst":"C:\\Windows\\Temp\\sliverreshine-e2e-b"}' 'fake session id: mv would rename on the target'
P 'POST /api/sessions/{id}/fs/chmod' '{"path":"C:\\Windows\\Temp\\sliverreshine-e2e-nope","mode":"0644","recursive":false}' 'fake session id: chmod mutates the target'
P 'POST /api/sessions/{id}/fs/chown' '{"path":"C:\\Windows\\Temp\\sliverreshine-e2e-nope","uid":"0","gid":"0","recursive":false}' 'fake session id: chown mutates the target'
P 'POST /api/sessions/{id}/fs/chtimes' '{"path":"C:\\Windows\\Temp\\sliverreshine-e2e-nope","atime":0,"mtime":0}' 'fake session id: chtimes mutates the target'
P 'POST /api/sessions/{id}/fs/grep' '{"pattern":"sliverreshine-e2e-no-match","path":".","recursive":false,"before":0,"after":0}' 'real session id: content search is read-only'

# --- recon ------------------------------------------------------------------
P 'GET /api/sessions/{id}/ifconfig' '' 'real session id: read-only'
P 'GET /api/sessions/{id}/ps' '' 'real session id: read-only'
P 'POST /api/sessions/{id}/ps/kill' '{"pid":2147483647,"force":false}' 'fake session id: killing a process is destructive'
P 'GET /api/sessions/{id}/netstat' '' 'real session id: read-only'
P 'GET /api/sessions/{id}/env' '' 'real session id: read-only'
P 'POST /api/sessions/{id}/env' '{"key":"SLIVERRESHINE_E2E_PROBE","value":"1"}' 'fake session id: setting an env var mutates the target'
P 'DELETE /api/sessions/{id}/env/{key}' '' 'fake session id + fake key: unset would mutate the target'
P 'POST /api/sessions/{id}/exec' '{"path":"C:\\sliverreshine-e2e-no-such-binary.exe","args":[]}' 'fake session id: exec runs code on the target'
P 'GET /api/sessions/{id}/screenshot' '' 'real session id: read-only screen capture of the same host'

# --- code execution / injection (fake session id everywhere) ----------------
P 'POST /api/sessions/{id}/exec-assembly' '{"assembly":"!!!not-base64!!!","arguments":"","process":""}' 'invalid base64 -> 400 before any RPC; also keeps the handler away from the target'
P 'POST /api/sessions/{id}/sideload' '{"data":"!!!not-base64!!!","processName":"","args":"","entryPoint":""}' 'invalid base64 -> 400 before any RPC'
P 'POST /api/sessions/{id}/spawn-dll' '{"data":"!!!not-base64!!!","processName":"","args":"","entryPoint":""}' 'invalid base64 -> 400 before any RPC'
P 'POST /api/sessions/{id}/migrate' '{"pid":2147483647,"procName":""}' 'fake session id: migrate would move the implant'
P 'POST /api/sessions/{id}/process-dump' '{"pid":4}' 'fake session id: dumping a process reads target memory' '' $true
P 'POST /api/sessions/{id}/av-scan' '{"filter":"sliverreshine-e2e-no-such-process"}' 'fake session id: scan would enumerate the target process list'
P 'POST /api/sessions/{id}/impersonate' '{"username":"sliverreshine-e2e-no-such-user"}' 'fake session id: impersonation changes the target token'
P 'POST /api/sessions/{id}/make-token' '{"username":"sliverreshine-e2e-no-such-user","password":"x","domain":"."}' 'fake session id: token creation is a credential operation'
P 'POST /api/sessions/{id}/rev-to-self' '{}' 'fake session id: reverting a token changes target state'
P 'POST /api/sessions/{id}/getsystem' '{"hostingProcess":""}' 'fake session id: getsystem escalates on the target'
P 'GET /api/sessions/{id}/privs' '' 'real session id: read-only'
P 'POST /api/av/test' '{"url":"http://127.0.0.1:1/","database":"sliverreshine-e2e"}' 'explicit unreachable endpoint so no third-party lookup service is contacted'
P 'POST /api/sessions/{id}/execute-token' '{"path":"C:\\sliverreshine-e2e-no-such-binary.exe","args":[],"output":false}' 'fake session id: runs code under another token'
P 'POST /api/sessions/{id}/runas' '{"username":"sliverreshine-e2e-no-such-user","process_name":"sliverreshine-e2e-no-such-binary.exe","args":""}' 'fake session id: runas spawns a process on the target'

# --- topology / pivots ------------------------------------------------------
P 'GET /api/pivots/graph' '' 'read-only'
P 'GET /api/topology' '' 'read-only'
P 'GET /api/webdelivery/formats' '' 'static list'
P 'POST /api/webdelivery' '{"profile_name":"sliverreshine-e2e-no-such-profile","host":"127.0.0.1","port":1,"format":"psh"}' 'unknown profile -> error before any stage is built or listener started'
P 'GET /api/sessions/{id}/pivots/listeners' '' 'real session id: read-only'
P 'POST /api/sessions/{id}/pivots/listeners' '{"type":"mtls","bind_address":"127.0.0.1"}' 'fake session id: starting a pivot listener binds on the target'
P 'DELETE /api/sessions/{id}/pivots/listeners/{pivotID}' '' 'fake session id + non-numeric pivot id -> 400'

# --- services / ssh ---------------------------------------------------------
P 'POST /api/sessions/{id}/services' '{"service_name":"sliverreshine-e2e-nope","description":"","bin_path":"","hostname":"","arguments":""}' 'fake session id: creating a service changes the target'
P 'POST /api/sessions/{id}/services/stop' '{"service_name":"sliverreshine-e2e-nope","hostname":""}' 'fake session id: stopping a service changes the target'
P 'POST /api/sessions/{id}/services/remove' '{"service_name":"sliverreshine-e2e-nope","hostname":""}' 'fake session id: removing a service changes the target'
P 'POST /api/sessions/{id}/ssh' '{"username":"sliverreshine-e2e","hostname":"127.0.0.1","port":1,"command":"id","password":"x","priv_key":""}' 'fake session id: ssh runs a command on the target'
P 'POST /api/sessions/{id}/services/detail' '{"name":"sliverreshine-e2e-nope","hostname":""}' 'fake session id: service query'
P 'POST /api/sessions/{id}/services/start-by-name' '{"name":"sliverreshine-e2e-nope","hostname":""}' 'fake session id: starting a service changes the target'

# --- extensions / msf / shellcode ------------------------------------------
P 'GET /api/sessions/{id}/extensions' '' 'real session id: read-only'
P 'POST /api/sessions/{id}/extensions/register' '{"name":"sliverreshine-e2e","os":"windows","init":"","data_b64":"!!!"}' 'invalid base64 -> 400 before any RPC'
P 'POST /api/sessions/{id}/extensions/call' '{"name":"sliverreshine-e2e-no-such-extension","export":"","server_store":false,"args_b64":""}' 'fake session id: extension call executes on the target'
P 'POST /api/sessions/{id}/msf' '{"payload":"","lhost":"127.0.0.1","lport":1,"encoder":"","iterations":0}' 'fake session id: msf payloads execute on the target'
P 'POST /api/sessions/{id}/msf/remote' '{"payload":"","lhost":"127.0.0.1","lport":1,"encoder":"","iterations":0,"pid":2147483647}' 'fake session id: msf injection is destructive'
P 'POST /api/msf/stage' '{"arch":"x64","format":"exe","port":4444,"host":"127.0.0.1","os":"windows","protocol":"tcp"}' 'the connected server no longer implements MsfStage; the client returns a deliberate unsupported error'
P 'POST /api/sessions/{id}/backdoor' '{"file_path":"C:\\sliverreshine-e2e-no-such-binary.exe","profile_name":"sliverreshine-e2e-no-such-profile"}' 'fake session id: backdoor patches a file on the target'
P 'POST /api/sessions/{id}/dll-hijack' '{"reference_dll_path":"","target_location":"","reference_dll_b64":"","target_dll_b64":"","profile_name":"sliverreshine-e2e-no-such-profile"}' 'fake session id: dll hijack writes to the target'
P 'POST /api/shellcode/rdi' '{"data_b64":"AAAA","function_name":"","arguments":""}' 'server-side donut encode of an invalid blob; no session, nothing is executed'

# --- persistence / credentials harvesting ----------------------------------
P 'GET /api/persistence/modules' '' 'static catalog'
P 'GET /api/sessions/{id}/persistence' '' 'real session id: read-only inventory'
P 'POST /api/sessions/{id}/persistence/install' '{"platform":"windows","module":"sliverreshine-e2e-no-such-module","payload":"","name":""}' 'fake session id + unknown module: the installer cannot reach the target'
P 'POST /api/sessions/{id}/persistence/remove' '{"platform":"windows","module":"sliverreshine-e2e-no-such-module","payload":"","name":""}' 'fake session id + unknown module: the remover cannot reach the target'
P 'GET /api/mimikatz/modules' '' 'static catalog'
P 'POST /api/sessions/{id}/mimikatz' '{"command":"sliverreshine-e2e-no-such-command","autoAdd":false}' 'fake session id: mimikatz would harvest credentials from the target'
P 'POST /api/mimikatz/parse' '{"text":"sliverreshine e2e probe, no credentials here","source":"sliverreshine-e2e","autoAdd":false}' 'pure parser, autoAdd=false so the credential vault is never written'
P 'POST /api/sessions/{id}/exec-shellcode' '{"data_b64":"!!!not-base64!!!","pid":0,"rwx_pages":false}' 'invalid base64 -> 400 before any RPC'
P 'POST /api/sessions/{id}/psexec' '{"hostname":"","profile_name":"sliverreshine-e2e-no-such-profile","service_name":"sliverreshine-e2e-nope","service_desc":"","bin_path":""}' 'fake session id: psexec executes on a remote host'
P 'POST /api/sessions/{id}/ping' '' 'real session id: ping is read-only'

# --- registry ---------------------------------------------------------------
P 'GET /api/sessions/{id}/reg/subkeys' '' 'real session id: read-only registry enumeration' '?hive=HKEY_LOCAL_MACHINE&path=SOFTWARE'
P 'GET /api/sessions/{id}/reg/values' '' 'real session id: read-only registry enumeration' '?hive=HKEY_LOCAL_MACHINE&path=SOFTWARE'
P 'GET /api/sessions/{id}/reg/read' '' 'real session id: read-only registry read' '?hive=HKEY_LOCAL_MACHINE&path=SOFTWARE%5CMicrosoft%5CWindows%5CCurrentVersion&key=ProgramFilesDir'
P 'POST /api/sessions/{id}/reg/write' '{"hive":"HKEY_LOCAL_MACHINE","path":"Software\\sliverreshine-e2e-nope","key":"sliverreshine-e2e","value":"1","type":"string"}' 'fake session id: a registry write is explicitly off-limits on the live host'
P 'POST /api/sessions/{id}/reg/create-key' '{"hive":"HKEY_LOCAL_MACHINE","path":"Software\\sliverreshine-e2e-nope","key":"sliverreshine-e2e"}' 'fake session id: registry key creation is off-limits'
P 'POST /api/sessions/{id}/reg/delete-key' '{"hive":"HKEY_LOCAL_MACHINE","path":"Software\\sliverreshine-e2e-nope","key":"sliverreshine-e2e"}' 'fake session id: registry key deletion is off-limits'
P 'POST /api/sessions/{id}/reg/hive' '{"rootHive":"HKEY_LOCAL_MACHINE","requestedHive":"SAM"}' 'fake session id: hive extraction reads live hives and streams bytes' '' $true

# --- session bookkeeping ----------------------------------------------------
P 'POST /api/sessions/{id}/reconfigure' '{"reconnect_interval":60}' 'fake session id: reconfigure changes the live implant timing'
P 'POST /api/sessions/{id}/rename' '{"name":"sliverreshine-e2e-rename"}' 'fake session id: renaming the real session would be visible state'
P 'POST /api/beacons/{id}/open-session' '' 'non-existent beacon id'
P 'GET /api/portfwd' '' 'read-only'
P 'POST /api/portfwd' '{}' 'missing session_id/remote_port -> 400 before a local listener is opened'
P 'DELETE /api/portfwd/{port}' '' 'non-numeric port -> 400'
P 'POST /api/beacons/prune' '{"days":100000}' 'a 100000-day window means no beacon can qualify, so this cannot prune anything'
P 'GET /api/aliases' '' 'read-only'
P 'POST /api/aliases' '{}' 'missing bundle_b64 -> 400, nothing is installed'
P 'DELETE /api/aliases/{name}' '' 'non-existent alias name'
P 'POST /api/sessions/{id}/aliases/{name}/run' '{"args":"","process":"","arch":"","method":"","class":""}' 'fake session id + non-existent alias: nothing runs'
P 'GET /api/beacons' '' 'read-only'
P 'GET /api/beacons/{id}' '' 'non-existent beacon id'
P 'POST /api/beacons/{id}/rename' '{"name":"sliverreshine-e2e-rename"}' 'non-existent beacon id'
P 'DELETE /api/beacons/{id}' '' 'non-existent beacon id'
P 'GET /api/beacons/{id}/tasks' '' 'non-existent beacon id'
P 'GET /api/beacons/{id}/tasks/{taskID}' '' 'non-existent beacon id and task id'

# --- implant profiles / builds ---------------------------------------------
P 'GET /api/implant-profiles' '' 'read-only'
P 'POST /api/implant-profiles' '{}' 'empty name -> "profile name is required" before anything is saved'
P 'DELETE /api/implant-profiles/{name}' '' 'non-existent profile name; the client guards this call because the server RPC panics on unknown names'
P 'DELETE /api/implant-builds/{name}' '' 'non-existent build name'
P 'POST /api/regenerate' '{"implantName":"sliverreshine-e2e-no-such-build"}' 'non-existent build name'
P 'GET /api/operators' '' 'read-only'
P 'GET /api/compiler' '' 'read-only'

# --- hosts / websites -------------------------------------------------------
P 'GET /api/hosts' '' 'read-only'
P 'GET /api/hosts/{uuid}' '' 'non-existent host uuid'
P 'DELETE /api/hosts/{uuid}' '' 'non-existent host uuid'
P 'DELETE /api/hosts/{uuid}/iocs/{iocID}' '' 'non-existent host uuid and ioc id'
P 'GET /api/websites' '' 'read-only'
P 'GET /api/websites/{name}' '' 'non-existent website name'
P 'POST /api/websites/{name}/content' '{"path":"/e2e","content_type":"text/plain","text":"sliverreshine e2e fixture"}' 'creates a fixture website named sliverreshine-e2e-fixture; the paired DELETE below removes it'
P 'PUT /api/websites/{name}/content' '{"path":"/e2e","content_type":"text/plain","text":"sliverreshine e2e fixture v2"}' 'updates the same fixture'
P 'DELETE /api/websites/{name}/content' '{"paths":["/e2e"]}' 'removes content from the same fixture'
P 'DELETE /api/websites/{name}' '' 'removes the fixture website created above (never a real site)'
P 'GET /api/canaries' '' 'read-only'

# --- wireguard / socks ------------------------------------------------------
P 'GET /api/wg/config' '' 'read-only'
P 'GET /api/wg/ip' '' 'read-only'
P 'GET /api/sessions/{id}/wg/forwarders' '' 'real session id: read-only'
P 'POST /api/sessions/{id}/wg/forwarders' '{"local_port":0,"remote_address":"127.0.0.1:1"}' 'fake session id: starting a forwarder changes the target'
P 'DELETE /api/sessions/{id}/wg/forwarders/{fwdID}' '' 'non-numeric forwarder id -> 400'
P 'GET /api/sessions/{id}/wg/socks' '' 'real session id: read-only'
P 'POST /api/sessions/{id}/wg/socks' '{"port":0}' 'fake session id: starting a socks server changes the target'
P 'DELETE /api/sessions/{id}/wg/socks/{serverID}' '' 'non-numeric socks id -> 400'
P 'GET /api/socks' '' 'read-only'
P 'POST /api/socks' '{}' 'missing session id -> the manager rejects it before opening a local listener'
P 'DELETE /api/socks/{id}' '' 'non-numeric proxy id -> 400'

# --- loot -------------------------------------------------------------------
P 'GET /api/loot' '' 'read-only'
P 'POST /api/loot' '{"type":"file","name":"sliverreshine-e2e-nope","file_name":"x.txt","file_type":"text","file_data_b64":"!!!"}' 'invalid base64 -> error before anything is stored'
P 'POST /api/loot/{id}/rename' '{"name":"sliverreshine-e2e-rename"}' 'non-existent loot id'
P 'GET /api/loot/{id}' '' 'non-existent loot id'
P 'DELETE /api/loot/{id}' '' 'non-existent loot id'

# --- jobs / events / builders / generate ------------------------------------
P 'GET /api/jobs' '' 'read-only'
P 'GET /api/events' '' 'read-only event stream drained with a bounded timeout'
P 'GET /api/builders' '' 'read-only'
P 'POST /api/generate' '{"name":"sliverreshine-e2e-nope","os":"plan9","arch":"amd64","format":"exe"}' 'invalid os -> rejected by validateBuildRequest before any compile is queued'
P 'POST /api/listeners' '{"type":"sliverreshine-e2e-no-such-type","addr":"127.0.0.1","port":1,"tls":false}' 'unknown listener type -> error before any socket is bound'
P 'GET /api/listeners/bind' '' 'read-only'
P 'POST /api/listeners/bind' '{"host":"","port":0}' 'empty host -> "host is required" before a dialer job is created'
P 'DELETE /api/listeners/bind/{id}' '' 'non-numeric job id -> 400'

# --- raw rpc ----------------------------------------------------------------
P 'GET /api/rpc/methods' '' 'read-only'
P 'POST /api/rpc/call' '{"method":"GetVersion","request":{},"timeout":10}' 'read-only RPC through the generic bridge'

# --- credential vault -------------------------------------------------------
P 'GET /api/creds' '' 'read-only'
P 'POST /api/creds' '{"__sliverreshine_e2e_probe__":true}' 'validation-only probe: the handler has no request validation, and any well-formed body would write an entry into the server vault, so an unknown field is used to force the 400 decode path'
P 'PUT /api/creds' '{}' 'empty credential list -> "no credentials supplied" before the vault is touched'
P 'DELETE /api/creds' '{"ids":["sliverreshine-e2e-no-such-credential"]}' 'non-existent credential id'
P 'GET /api/creds/filter' '' 'read-only' '?type=1000'
P 'POST /api/creds/sniff' '{"hash":"sliverreshine-e2e-no-such-hash"}' 'read-only hash-type identification'
P 'GET /api/creds/{id}' '' 'non-existent credential id'

# --- memfiles ---------------------------------------------------------------
P 'GET /api/sessions/{id}/memfiles' '' 'real session id: read-only'
P 'POST /api/sessions/{id}/memfiles' '' 'fake session id: creating an in-memory file mutates the target'
P 'DELETE /api/sessions/{id}/memfiles' '{"fd":-1}' 'fake session id + impossible fd'

# --- monitoring providers ---------------------------------------------------
P 'GET /api/monitor/providers' '' 'read-only'
P 'POST /api/monitor/providers' '{"__sliverreshine_e2e_probe__":true}' 'validation-only probe: an accepted body would add a real telemetry sink, so an unknown field forces the 400 decode path'
P 'DELETE /api/monitor/providers' '{"__sliverreshine_e2e_probe__":true}' 'validation-only probe: an accepted body would remove a real telemetry sink'

# --- c2 profiles / encoders -------------------------------------------------
P 'GET /api/c2profiles' '' 'read-only'
P 'POST /api/c2profiles' '{}' 'missing profile -> "no profile supplied" before anything is stored'
P 'GET /api/c2profiles/{name}' '' 'non-existent profile name'
P 'GET /api/traffic-encoders' '' 'read-only'
P 'POST /api/traffic-encoders' '{"name":"sliverreshine-e2e-nope","wasm":"!!!not-base64!!!","skipTests":false}' 'invalid base64 -> 400, nothing is registered'
P 'DELETE /api/traffic-encoders/{name}' '' 'non-existent encoder name'
P 'GET /api/shellcode-encoders' '' 'read-only'
P 'POST /api/shellcode-encoders' '{"encoder":"sliverreshine-e2e-no-such-encoder","arch":"amd64","iterations":1,"badChars":"","data":"AAAA"}' 'unknown encoder name; encoding is a pure computation with no side effects'

# --- wasm extensions --------------------------------------------------------
P 'GET /api/sessions/{id}/wasm' '' 'real session id: read-only'
P 'POST /api/sessions/{id}/wasm/register' '{"name":"sliverreshine-e2e","wasm":"!!!not-base64!!!"}' 'invalid base64 -> 400 before any RPC'
P 'POST /api/sessions/{id}/wasm/exec' '{"name":"sliverreshine-e2e-no-such-extension","args":[]}' 'fake session id: wasm exec runs on the target'

# --- reverse port forwards / certs / tunnels --------------------------------
P 'GET /api/sessions/{id}/rportfwd' '' 'real session id: read-only'
P 'POST /api/sessions/{id}/rportfwd' '{"bindAddress":"256.256.256.256","bindPort":1,"forwardAddress":"127.0.0.1","forwardPort":1}' 'fake session id: a reverse forward binds a port on the target'
P 'DELETE /api/sessions/{id}/rportfwd/{fwdID}' '' 'non-numeric forward id -> 400'
P 'GET /api/certificates/ca' '' 'read-only'
P 'GET /api/certificates' '' 'read-only'
P 'POST /api/sessions/{id}/tunnel' '' 'fake session id: opening a tunnel mutates the target'
P 'DELETE /api/tunnels' '{"tunnelID":4294967295,"sessionID":"no-such-thing"}' 'impossible tunnel id and a fake session id'

# --- console auth -----------------------------------------------------------
P 'GET /api/settings/auth' '' 'read-only'
P 'PUT /api/settings/auth' '{"username":"","password":"","currentPassword":"sliverreshine-e2e-wrong-password"}' 'wrong current password -> 403; the console credential is never changed'

# ---------------------------------------------------------------- runner -----

$script:results = @()

# Classification, in the order the rules are checked.
#
# The distinction that matters is not the status code but whether the handler ran
# and rejected the REQUEST. The console reports a rejected argument as 500 as
# often as it reports one as 400, so a marker list over 500 bodies was the wrong
# instrument: it classified "no traffic encoder named X" as a server fault. The
# rules below key off the failure MODE instead.
#
#   * a Go panic in the body          -> the handler crashed            UNEXPECTED
#   * a gRPC transport failure        -> the console lost its server    UNEXPECTED
#   * gRPC Internal / Unknown         -> opaque server-side failure      UNEXPECTED
#   * 503                             -> requireClient saw no client    UNEXPECTED
#   * mux-fallback 404 / 405 / empty  -> the pattern did not match       UNEXPECTED
#   * non-JSON where JSON was due     -> unparseable                      UNEXPECTED
#   * 2xx, or well-formed JSON error  -> handler ran and validated       PASS/ACCEPTED
function Get-Classification {
    param([string]$Code, [string]$CType, [string]$Size, [string]$Body, [bool]$BinOk)

    $b = $Body.Trim()
    $lb = $b.ToLower()

    # A crash inside the handler. The most important thing this harness looks for.
    if ($b -match 'panic:' -or $b -match 'goroutine \d+ \[' -or $b -match 'runtime error:') {
        return 'UNEXPECTED'
    }

    # The console could not reach sliver-server. Whatever the route was going to
    # do, this result says nothing about the route.
    if ($lb.Contains('code = unavailable') -or $lb.Contains('forcibly closed') -or
        $lb.Contains('connection refused') -or $lb.Contains('no such host') -or
        $lb.Contains('transport is closing') -or $lb.Contains('i/o timeout')) {
        return 'UNEXPECTED'
    }

    # An opaque server-side failure: the request was never validated against
    # anything, so it cannot be credited as a clean rejection.
    if ($lb.Contains('code = internal') -or $lb.Contains('code = unknown') -or
        $lb.Contains('code = dataloss')) {
        return 'UNEXPECTED'
    }

    # The wiring check. 503 here is requireClient refusing to run the handler at
    # all, which is the failure the brief calls out by name.
    if ($Code -eq '503') { return 'UNEXPECTED' }
    if ($Code -eq '405') { return 'UNEXPECTED' }

    # The mux fallback writes exactly this body for a path that matched no
    # registered pattern.
    if ($Code -eq '404' -and $b -eq '{"error":"not found"}') { return 'UNEXPECTED' }

    if ([string]::IsNullOrWhiteSpace($Code) -or $Code -eq '000') { return 'UNEXPECTED' }
    if ([string]::IsNullOrWhiteSpace($b)) { return 'UNEXPECTED' }

    $isJson = ($CType -match 'application/json')
    if (-not $isJson) {
        # Streaming endpoints answer with bytes on purpose.
        if ($BinOk -and $Code -match '^2') { return 'PASS' }
        return 'UNEXPECTED'
    }

    if ($Code -match '^2') { return 'PASS' }

    # Everything else is a well-formed JSON error from a handler that ran. 4xx
    # and 5xx alike: the console routes a rejected argument through whatever
    # status the wrapped RPC produced.
    return 'ACCEPTED-ERROR'
}
# Invoke-Probe reads /api/info as a circuit breaker.
#
# /api/info answers 200 even when the client is gone (it reports
# {"connected":false,"error":...}), so the check is on the body, not the status.
# Returns $true while the console can still reach sliver-server.
function Invoke-Probe {
    $probeBody = Join-Path $OutDir 'probe.tmp'
    & $curl -s -u $Cred --max-time 8 -o $probeBody "$Base/api/info" 2>&1 | Out-Null
    if (-not (Test-Path $probeBody)) { return $false }
    $t = ''
    try { $t = [System.IO.File]::ReadAllText($probeBody, [System.Text.Encoding]::UTF8) } catch { return $false }
    if ($t -match '"connected":\s*true') { return $true }
    return $false
}

# Save the console and server logs on every run: a panic is the single most
# important piece of evidence this harness can produce, and it lives in the
# server log rather than in any HTTP response.
function Save-LogSnapshots {
    # $home/$hint are read-only automatic variables in Windows PowerShell, so the
    # destination is held under a different name.
    $consoleHome = $EntryPoint
    if ($consoleHome -eq '' -and (Test-Path $SidFile)) {
        $cand = Join-Path (Split-Path $SidFile -Parent) 'debt'
        if (Test-Path $cand) { $consoleHome = $cand }
    }
    if ($consoleHome -eq '') { return }
    # Two layouts exist in the wild: <home>/data/<log> when the console keeps its
    # state in a data subdirectory, and <home>/<log> when -home names the data
    # directory itself. Copy from whichever is present.
    $roots = @((Join-Path $consoleHome 'data'), $consoleHome)
    foreach ($n in @('sliverreshine.log', 'sliver-server.log')) {
        foreach ($root in $roots) {
            $src = Join-Path $root $n
            if (Test-Path $src) {
                Copy-Item $src (Join-Path $OutDir ("console-" + $n)) -Force
                break
            }
        }
    }
}

function Invoke-Route {
    param([string]$Method, [string]$Pattern, [string]$Path, [string]$Body, [string]$Note, [bool]$BinOk)

    $bodyFile = Join-Path $OutDir 'body.tmp'
    $hdrFile = Join-Path $OutDir 'hdr.tmp'
    if (Test-Path $bodyFile) { Remove-Item $bodyFile -Force }
    if (Test-Path $hdrFile) { Remove-Item $hdrFile -Force }

    $curlArgs = @('-s', '-u', $Cred, '-X', $Method, '--max-time', "$TimeoutSec",
              '-o', $bodyFile, '-D', $hdrFile,
              '-w', '%{http_code}|%{content_type}|%{size_download}')
    if ($Method -ne 'GET') { $curlArgs += @('-H', 'Content-Type: application/json') }
    if ($Body) {
        $bf = Join-Path $OutDir 'req.tmp'
        [System.IO.File]::WriteAllText($bf, $Body, (New-Object System.Text.UTF8Encoding($false)))
        $curlArgs += @('--data-binary', "@$bf")
    }
    $curlArgs += "$Base$Path"

    $out = & $curl @args 2>&1
    $out = & $curl @curlArgs 2>&1
    $meta = $null
    foreach ($line in @($out)) {
        if ("$line" -match '^\d{3}\|') { $meta = "$line" }
    }

    $code = '000'; $ctype = ''; $size = '0'
    if ($meta) {
        $parts = $meta.Split('|')
        if ($parts.Count -ge 3) { $code = $parts[0]; $ctype = $parts[1]; $size = $parts[2] }
    }
    $body = ''
    if (Test-Path $bodyFile) {
        try { $body = [System.IO.File]::ReadAllText($bodyFile, [System.Text.Encoding]::UTF8) } catch { $body = '<unreadable>' }
    }
    if (-not $meta) { $body = 'CURL-FAILED: ' + (($out | Out-String).Trim()) }

    $class = Get-Classification -Code $code -CType $ctype -Size $size -Body $body -BinOk $BinOk
    $script:results += [pscustomobject]@{
        Method = $Method; Pattern = $Pattern; Path = $Path
        Code = $code; Class = $class; CType = $ctype; Size = $size
        Body = $body; Note = $Note
    }
    $colour = 'Gray'
    if ($class -eq 'PASS') { $colour = 'Green' }
    elseif ($class -eq 'ACCEPTED-ERROR') { $colour = 'DarkYellow' }
    elseif ($class -eq 'UNEXPECTED') { $colour = 'Red' }
    $label = '{0,-6} {1,-16} {2,3}  {3} {4}' -f $class, $code, $size, $Method, $Pattern
    Write-Host $label -ForegroundColor $colour
}

# ---------------------------------------------------------------- main -------

Write-Host ""
Write-Host ("sliverreshine route coverage :: base=$Base routes=" + $routes.Count + " session=" + $Sid) -ForegroundColor White

$script:abortedAt = ''
Save-LogSnapshots
if (-not (Invoke-Probe)) {
    Write-Host "WARNING: the console reports it is NOT connected to sliver-server. Session/client routes will all answer Unavailable." -ForegroundColor Yellow
    Write-Host "         Restart sliverreshine first (it relaunches the embedded server) or the sweep measures nothing." -ForegroundColor Yellow
}
Write-Host ""

# Routes are run in CSV order, except that anything in $deferred is moved to the
# very end so a route that can take the server down cannot mask the rest.
$ordered = @()
$tail = @()
foreach ($r in $routes) {
    $k = $r.Method.Trim() + ' ' + $r.Pattern.Trim()
    if ($deferred.ContainsKey($k)) { $tail += $r } else { $ordered += $r }
}
$tail | ForEach-Object { $ordered += $_ }

$index = 0
foreach ($r in $ordered) {
    $index++
    $m = $r.Method.Trim()
    $pat = $r.Pattern.Trim()
    $key = "$m $pat"

    if ($Only -ne '' -and $pat -notlike "*$Only*") { continue }

    $isDeferred = $deferred.ContainsKey($key)
    if ($skips.ContainsKey($key) -and -not ($isDeferred -and $IncludeCrashers)) {
        $skipReason = $skips[$key]
        if ($isDeferred -and -not $IncludeCrashers) {
            $skipReason = $skipReason + ' (re-prove with -IncludeCrashers, which moves it to the very end of the run)'
        }
        $script:results += [pscustomobject]@{
            Method = $m; Pattern = $pat; Path = ''; Code = '-'; Class = 'SKIPPED'
            CType = ''; Size = ''; Body = ''; Note = $skipReason
        }
        Write-Host ('{0,-6} {1,-16} {2,3}  {3} {4}' -f 'SKIP', '-', '', $m, $pat) -ForegroundColor DarkCyan
        continue
    }

    # Guard: if a previous route took sliver-server down, every remaining route
    # would answer 500 "Unavailable" for one reason. Detect that once and stop
    # blaming the routes.
    if ($index -gt 1) {
        $probe = Invoke-Probe
        if (-not $probe) {
            $script:abortedAt = $key
            Write-Host ""
            Write-Host ("!! TRANSPORT LOST before " + $key + " - console no longer reaches sliver-server; aborting the sweep.") -ForegroundColor Red
            Write-Host ""
            $script:results += [pscustomobject]@{
                Method = $m; Pattern = $pat; Path = ''; Code = '-'; Class = 'ABORTED'
                CType = ''; Size = ''; Body = ''; Note = 'not driven: the console lost its sliver-server connection earlier in the run (see the report header)'
            }
            continue
        }
    }

    $p = $plan[$key]
    $body = ''
    $extra = ''
    $note = ''
    $bin = $false
    if ($p) { $body = $p.body; $extra = $p.extra; $note = $p.note; $bin = $p.bin }

    # Path substitution.
    $path = $pat
    $useReal = $realIdRoutes -contains $key
    $sidFor = if ($useReal) { $Sid } else { $Fake }
    $path = $path.Replace('{id}', $sidFor)
    $path = $path.Replace('{name}', $Fake)
    $path = $path.Replace('{key}', $Fake)
    $path = $path.Replace('{taskID}', $Fake)
    $path = $path.Replace('{fwdID}', $Fake)
    $path = $path.Replace('{pivotID}', $Fake)
    $path = $path.Replace('{uuid}', $Fake)
    $path = $path.Replace('{iocID}', $Fake)
    $path = $path.Replace('{port}', $Fake)
    $path = $path.Replace('{serverID}', $Fake)
    if ($pat -like '/api/websites/*') { $path = $path.Replace($Fake, $SiteFixture) }
    $path = $path + $extra

    if ($useReal) { $note = 'REAL session id used. ' + $note }
    elseif ($pat.Contains('{id}')) { $note = 'fake session id. ' + $note }
    if ($isDeferred) { $note = 'DEFERRED TO END OF RUN (known to kill sliver-server). ' + $note }

    Invoke-Route -Method $m -Pattern $pat -Path $path -Body $body -Note $note -BinOk $bin
    if ($DelayMs -gt 0) { Start-Sleep -Milliseconds $DelayMs }
}

# The server log is the only place a panic on the sliver-server side shows up, so
# it is snapshotted after the sweep, not before.
Save-LogSnapshots

# ---------------------------------------------------------------- report -----

$jsonPath = Join-Path $OutDir 'sliverreshine_e2e_routes_results.json'
$csvPath = Join-Path $OutDir 'sliverreshine_e2e_routes_results.csv'
$txtPath = Join-Path $OutDir 'sliverreshine_e2e_routes_report.txt'

$script:results | ConvertTo-Json -Depth 4 | Set-Content -Path $jsonPath -Encoding UTF8
$script:results | Select-Object Method, Pattern, Code, Class, CType, Size, Note, Body |
    Export-Csv -Path $csvPath -NoTypeInformation -Encoding UTF8

$pass = @($script:results | Where-Object { $_.Class -eq 'PASS' })
$acc = @($script:results | Where-Object { $_.Class -eq 'ACCEPTED-ERROR' })
$unx = @($script:results | Where-Object { $_.Class -eq 'UNEXPECTED' })
$skp = @($script:results | Where-Object { $_.Class -eq 'SKIPPED' })
$abt = @($script:results | Where-Object { $_.Class -eq 'ABORTED' })

$lines = New-Object System.Collections.Generic.List[string]
function Add-Line([string]$s) { $script:lines.Add($s) }

Add-Line 'sliverreshine route coverage report'
Add-Line ("base        : " + $Base)
Add-Line ("session id  : " + $Sid)
Add-Line ("inventory   : " + $RoutesCsv + " (" + $routes.Count + " rows)")
Add-Line ("generated   : " + (Get-Date).ToString('s'))
Add-Line ''
Add-Line ("total       : " + $script:results.Count)
Add-Line ("PASS        : " + $pass.Count)
Add-Line ("ACCEPTED    : " + $acc.Count)
Add-Line ("UNEXPECTED  : " + $unx.Count)
Add-Line ("SKIPPED     : " + $skp.Count)
Add-Line ("ABORTED     : " + $abt.Count + " (not driven; transport already lost)")
if ($script:abortedAt -ne '') { Add-Line ("transport lost before: " + $script:abortedAt) }
Add-Line ''

$panic = @($script:results | Where-Object { $_.Body -match 'panic:|goroutine \d+ \[' })
Add-Line ('--- PANICS / GO STACK TRACES IN HTTP RESPONSES (' + $panic.Count + ') ---')
if ($panic.Count -eq 0) { Add-Line '  none' }
foreach ($x in $panic) { Add-Line ("  " + $x.Method + " " + $x.Pattern + " -> " + $x.Code); Add-Line ("    " + $x.Body) }
Add-Line ''

Add-Line ('--- SKIPPED (' + $skp.Count + ') ---')
foreach ($x in $skp) { Add-Line ("  " + $x.Method + " " + $x.Pattern + " : " + $x.Note) }
Add-Line ''

Add-Line ('--- ABORTED (' + $abt.Count + ') ---')
foreach ($x in $abt) { Add-Line ("  " + $x.Method + " " + $x.Pattern + " : " + $x.Note) }
Add-Line ''
Add-Line ('--- SERVER LOG SNAPSHOT (panic markers) ---')
foreach ($n in @('console-sliver-server.log')) {
    $lf = Join-Path $OutDir $n
    if (Test-Path $lf) {
        $hits = @(Get-Content $lf | Select-String -Pattern '^panic:|^\[signal|^goroutine \d+ \[running\]')
        if ($hits.Count -eq 0) { Add-Line ('  ' + $n + ': no panic lines') }
        else {
            Add-Line ('  ' + $n + ': ' + $hits.Count + ' panic marker line(s)')
            foreach ($h in $hits) { Add-Line ('    ' + $h.Line) }
        }
    }
}
Add-Line ''
Add-Line ('--- UNEXPECTED (' + $unx.Count + ') ---')
foreach ($x in $unx) {
    Add-Line ("  " + $x.Method + " " + $x.Pattern + "  -> HTTP " + $x.Code + "  ctype=" + $x.CType + " size=" + $x.Size)
    Add-Line ("    path : " + $x.Path)
    Add-Line ("    body : " + $x.Body)
    if ($x.Note) { Add-Line ("    note : " + $x.Note) }
    Add-Line ''
}
Add-Line ''

Add-Line ('--- SKIPPED (' + $skp.Count + ') ---')
foreach ($x in $skp) { Add-Line ("  " + $x.Method + " " + $x.Pattern + " : " + $x.Note) }
Add-Line ''

Add-Line ('--- ACCEPTED-ERROR (' + $acc.Count + ') ---')
foreach ($x in $acc) { Add-Line ("  " + $x.Method + " " + $x.Pattern + " -> HTTP " + $x.Code + " : " + $x.Body) }
Add-Line ''

Add-Line ('--- PASS (' + $pass.Count + ') ---')
foreach ($x in $pass) { Add-Line ("  " + $x.Method + " " + $x.Pattern + " -> HTTP " + $x.Code) }

$lines -join "`r`n" | Set-Content -Path $txtPath -Encoding UTF8

Write-Host ''
Write-Host '=== SUMMARY ===' -ForegroundColor White
Write-Host ("  total      : " + $script:results.Count)
Write-Host ("  PASS       : " + $pass.Count) -ForegroundColor Green
Write-Host ("  ACCEPTED   : " + $acc.Count) -ForegroundColor DarkYellow
Write-Host ("  UNEXPECTED : " + $unx.Count) -ForegroundColor $(if ($unx.Count) { 'Red' } else { 'Green' })
Write-Host ("  SKIPPED    : " + $skp.Count) -ForegroundColor DarkCyan
Write-Host ''

if ($unx.Count) {
    Write-Host '=== UNEXPECTED (verbatim) ===' -ForegroundColor Red
    foreach ($x in $unx) {
        Write-Host ("  " + $x.Method + " " + $x.Pattern + " -> HTTP " + $x.Code)
        Write-Host ("    body: " + $x.Body)
    }
    Write-Host ''
}
if ($panic.Count) {
    Write-Host '=== PANIC / STACK ===' -ForegroundColor Red
    foreach ($x in $panic) { Write-Host ("  " + $x.Method + " " + $x.Pattern + " -> " + $x.Body) }
    Write-Host ''
}
Write-Host ("artifacts: " + $txtPath)
Write-Host ("           " + $csvPath)
Write-Host ("           " + $jsonPath)
