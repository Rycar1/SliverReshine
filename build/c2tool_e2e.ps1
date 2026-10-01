# c2tool end-to-end functional test harness.
#
# Drives the real HTTP API of a running console: generates payloads across the
# full matrix, starts listeners, and exercises session operations. ASCII only
# (PowerShell 5.1 reads a BOM-less script as ANSI).
#
# Usage:
#   .\c2tool_e2e.ps1 -Base http://127.0.0.1:18180 -Cred "op:pass" -Phase generate
#   .\c2tool_e2e.ps1 -Base ... -Cred ... -Phase listeners
#   .\c2tool_e2e.ps1 -Base ... -Cred ... -Phase sessions -SessionId <id>
#   .\c2tool_e2e.ps1 -Base ... -Cred ... -Phase matrix      # full payload matrix
param(
    [string]$Base = 'http://127.0.0.1:18180',
    [string]$Cred = '',
    [string]$Phase = 'generate',
    [string]$SessionId = '',
    [string]$OutDir = "$env:TEMP\c2tool-e2e",
    [int]$TimeoutSec = 600
)

$ErrorActionPreference = 'Continue'
$curl = "$env:SystemRoot\system32\curl.exe"
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

$script:pass = 0
$script:fail = 0
$script:fails = @()

function T {
    # The parameter is called CaseName, not Name, and that rename is the whole
    # fix.
    #
    # PowerShell variable names are case-insensitive, and `&` invokes the body
    # in a child scope that inherits T's locals. A parameter called $Name is
    # therefore visible to the body as $name -- so every test body's own
    # `$name = "m-$os-$arch-$fmt"` was shadowed by the TEST LABEL, and the
    # matrix posted a name containing a slash and a space. Sliver rejected all
    # 28 with "name must be alphanumeric or .-_ only" and the run reported it
    # as a product failure when nothing had been built at all.
    #
    # GetNewClosure() is NOT the fix: it rebinds the body to the scope where it
    # was written, which detaches it from the script-level variables the bodies
    # depend on ($Base, $Cred, the Api/GenPayload helpers) and breaks every test
    # a different way.
    param([string]$CaseName, [scriptblock]$Body)
    try {
        $r = & $Body
        if ($r -eq $true -or $null -eq $r) {
            $script:pass++
            Write-Host ("  PASS  " + $CaseName) -ForegroundColor Green
        } else {
            $script:fail++
            $script:fails += "$CaseName : $r"
            Write-Host ("  FAIL  " + $CaseName + " -> " + $r) -ForegroundColor Red
        }
    } catch {
        $script:fail++
        $script:fails += "$CaseName : $($_.Exception.Message)"
        Write-Host ("  FAIL  " + $CaseName + " -> " + $_.Exception.Message) -ForegroundColor Red
    }
}

function Api {
    param([string]$Path, [string]$Method = 'GET', [string]$Body = '')
    $args = @('-s', '-u', $Cred, '-X', $Method, '--max-time', "$TimeoutSec")
    if ($Body) {
        $f = [System.IO.Path]::GetTempFileName()
        [System.IO.File]::WriteAllText($f, $Body, (New-Object System.Text.UTF8Encoding($false)))
        $args += @('-H', 'Content-Type: application/json', '--data-binary', "@$f")
    }
    $args += "$Base$Path"
    $out = & $curl @args 2>&1
    return ($out -join '')
}

function GenPayload {
    param([string]$Name, [string]$Os, [string]$Arch, [string]$Format, [string]$C2 = 'mtls://127.0.0.1:8888', [bool]$Obfuscate = $false, [bool]$Evasion = $false, [string]$ExtraJson = '')
    $proto = ($C2 -split ':')[0]
    $addr = $C2 -replace '^[a-z-]+://', ''
    $json = '{"name":"' + $Name + '","os":"' + $Os + '","arch":"' + $Arch + '","format":"' + $Format + '",' +
            '"c2":[{"address":"' + $addr + '","protocol":"' + $proto + '"}],' +
            '"interval":5,"jitter":0,"mtls":true,"http":false,"dns":false,"wireguard":false,' +
            '"debug":false,"evasion":' + $(if ($Evasion) { 'true' } else { 'false' }) + ',' +
            '"obfuscate":' + $(if ($Obfuscate) { 'true' } else { 'false' }) + ',' +
            '"limitDomainJoined":false,"transport":"' + $proto + '","maxConnectionErrors":100' + $ExtraJson + '}'
    $r = Api -Path '/api/generate' -Method 'POST' -Body $json
    $o = $null
    try { $o = $r | ConvertFrom-Json } catch { return @{ ok = $false; err = "not JSON: " + $r.Substring(0, [Math]::Min(200, $r.Length)) } }
    if ($o.error) { return @{ ok = $false; err = $o.error } }
    if (-not $o.data) { return @{ ok = $false; err = "no data field" } }
    return @{ ok = $true; bytes = [Math]::Round($o.data.Length * 3 / 4 / 1MB, 2) }
}

# ---------------------------------------------------------------- phases -----

function Phase-Generate {
    Write-Host "`n=== 基础生成：每种格式 ===" -ForegroundColor Cyan
    T 'windows/amd64 exe' { $r = GenPayload -Name 'g-we' -Os windows -Arch amd64 -Format exe; if ($r.ok) { $true } else { $r.err } }
    T 'windows/amd64 service' { $r = GenPayload -Name 'g-ws' -Os windows -Arch amd64 -Format service; if ($r.ok) { $true } else { $r.err } }
    T 'windows/amd64 shellcode' { $r = GenPayload -Name 'g-wsc' -Os windows -Arch amd64 -Format shellcode; if ($r.ok) { $true } else { $r.err } }
    T 'windows/amd64 shared' { $r = GenPayload -Name 'g-wsh' -Os windows -Arch amd64 -Format shared; if ($r.ok) { $true } else { $r.err } }
    T 'linux/amd64 exe' { $r = GenPayload -Name 'g-le' -Os linux -Arch amd64 -Format exe; if ($r.ok) { $true } else { $r.err } }
    T 'linux/amd64 shared' { $r = GenPayload -Name 'g-lsh' -Os linux -Arch amd64 -Format shared; if ($r.ok) { $true } else { $r.err } }
    T 'darwin/amd64 exe' { $r = GenPayload -Name 'g-de' -Os darwin -Arch amd64 -Format exe; if ($r.ok) { $true } else { $r.err } }
    T 'freebsd/amd64 exe' { $r = GenPayload -Name 'g-fe' -Os freebsd -Arch amd64 -Format exe; if ($r.ok) { $true } else { $r.err } }

    Write-Host "`n=== 混淆与免杀开关 ===" -ForegroundColor Cyan
    T 'windows obfuscate' { $r = GenPayload -Name 'g-obf' -Os windows -Arch amd64 -Format exe -Obfuscate $true; if ($r.ok) { $true } else { $r.err } }
    T 'windows evasion' { $r = GenPayload -Name 'g-eva' -Os windows -Arch amd64 -Format exe -Evasion $true; if ($r.ok) { $true } else { $r.err } }
    T 'windows obfuscate+evasion' { $r = GenPayload -Name 'g-be' -Os windows -Arch amd64 -Format exe -Obfuscate $true -Evasion $true; if ($r.ok) { $true } else { $r.err } }
    T 'linux obfuscate' { $r = GenPayload -Name 'g-lobf' -Os linux -Arch amd64 -Format exe -Obfuscate $true; if ($r.ok) { $true } else { $r.err } }

    Write-Host "`n=== 协议 ===" -ForegroundColor Cyan
    T 'mtls' { $r = GenPayload -Name 'g-mtls' -Os windows -Arch amd64 -Format exe -C2 'mtls://127.0.0.1:8888'; if ($r.ok) { $true } else { $r.err } }
    T 'http' { $r = GenPayload -Name 'g-http' -Os windows -Arch amd64 -Format exe -C2 'http://127.0.0.1:8889'; if ($r.ok) { $true } else { $r.err } }
    T 'https' { $r = GenPayload -Name 'g-https' -Os windows -Arch amd64 -Format exe -C2 'https://127.0.0.1:8890'; if ($r.ok) { $true } else { $r.err } }
    T 'dns' { $r = GenPayload -Name 'g-dns' -Os windows -Arch amd64 -Format exe -C2 'dns://127.0.0.1:8891'; if ($r.ok) { $true } else { $r.err } }
    T 'wireguard' { $r = GenPayload -Name 'g-wg' -Os windows -Arch amd64 -Format exe -C2 'wireguard://127.0.0.1:8892'; if ($r.ok) { $true } else { $r.err } }
    T 'bind' { $r = GenPayload -Name 'g-bind' -Os windows -Arch amd64 -Format exe -C2 'bind://127.0.0.1:8893'; if ($r.ok) { $true } else { $r.err } }
    T 'tcp-pivot' { $r = GenPayload -Name 'g-pivot' -Os windows -Arch amd64 -Format exe -C2 'tcp-pivot://127.0.0.1:8894'; if ($r.ok) { $true } else { $r.err } }

    Write-Host "`n=== 错误路径（应当被拒绝而不是崩）===" -ForegroundColor Cyan
    T 'bad arch rejected' { $r = GenPayload -Name 'g-bad1' -Os windows -Arch mips -Format exe; if (-not $r.ok) { $true } else { 'accepted an invalid arch' } }
    T 'bad os rejected' { $r = GenPayload -Name 'g-bad2' -Os plan9 -Arch amd64 -Format exe; if (-not $r.ok) { $true } else { 'accepted an invalid os' } }
    T 'bad format rejected' { $r = GenPayload -Name 'g-bad3' -Os windows -Arch amd64 -Format rom; if (-not $r.ok) { $true } else { 'accepted an invalid format' } }
    T 'empty name rejected' { $r = GenPayload -Name '' -Os windows -Arch amd64 -Format exe; if (-not $r.ok) { $true } else { 'accepted an empty name' } }
}

function Phase-Matrix {
    Write-Host "`n=== 全矩阵：4 OS x arch x 格式 ===" -ForegroundColor Cyan
    $matrix = @(
        @('windows', 'amd64', @('exe', 'service', 'shellcode', 'shared')),
        @('windows', '386', @('exe', 'shellcode', 'shared')),
        @('windows', 'arm64', @('exe', 'shellcode', 'shared')),
        @('linux', 'amd64', @('exe', 'shared', 'shellcode')),
        @('linux', '386', @('exe', 'shared')),
        @('linux', 'arm64', @('exe', 'shared')),
        @('linux', 'arm', @('exe', 'shared')),
        @('darwin', 'amd64', @('exe', 'shared', 'shellcode')),
        @('darwin', 'arm64', @('exe', 'shared')),
        @('freebsd', 'amd64', @('exe', 'shared')),
        @('freebsd', '386', @('exe', 'shared'))
    )
    foreach ($row in $matrix) {
        $os = $row[0]; $arch = $row[1]
        foreach ($fmt in $row[2]) {
            $name = "m-$os-$arch-$fmt"
            T "$os/$arch $fmt" {
                $r = GenPayload -Name $name -Os $os -Arch $arch -Format $fmt -C2 'mtls://127.0.0.1:8888'
                if ($r.ok) { $true } else { $r.err }
            }
        }
    }
}

function Phase-Listeners {
    Write-Host "`n=== 监听器 ===" -ForegroundColor Cyan
    T 'start mtls listener' {
        $r = Api -Path '/api/listeners' -Method 'POST' -Body '{"type":"mtls","addr":"127.0.0.1","port":8888,"tls":true}'
        $o = $r | ConvertFrom-Json
        if ($o.success) { $true } else { $r }
    }
    T 'start http listener' {
        $r = Api -Path '/api/listeners' -Method 'POST' -Body '{"type":"http","addr":"127.0.0.1","port":8889,"tls":false}'
        $o = $r | ConvertFrom-Json
        if ($o.success) { $true } else { $r }
    }
    T 'start https listener' {
        $r = Api -Path '/api/listeners' -Method 'POST' -Body '{"type":"http","addr":"127.0.0.1","port":8890,"tls":true}'
        $o = $r | ConvertFrom-Json
        if ($o.success) { $true } else { $r }
    }
    T 'start dns listener' {
        $r = Api -Path '/api/listeners' -Method 'POST' -Body '{"type":"dns","addr":"127.0.0.1","port":8053,"tls":false,"domains":["example.com"]}'
        $o = $r | ConvertFrom-Json
        if ($o.success) { $true } else { $r }
    }
    T 'list jobs shows them' {
        $r = Api -Path '/api/jobs'
        $o = $r | ConvertFrom-Json
        if ($o.jobs.Count -ge 4) { $true } else { "jobs = " + $o.jobs.Count }
    }
    T 'duplicate port refused' {
        $r = Api -Path '/api/listeners' -Method 'POST' -Body '{"type":"mtls","addr":"127.0.0.1","port":8888,"tls":true}'
        $o = $r | ConvertFrom-Json
        if ($o.error -or -not $o.success) { $true } else { 'a duplicate listener was accepted' }
    }
}

function Phase-Sessions {
    Write-Host "`n=== 会话操作 ===" -ForegroundColor Cyan
    T 'list sessions' {
        $o = (Api -Path '/api/sessions') | ConvertFrom-Json
        if ($o.sessions -ne $null) { $true } else { 'no sessions field' }
    }
    if (-not $SessionId) {
        Write-Host "  跳过：未提供 -SessionId" -ForegroundColor Yellow
        return
    }
    $sid = $SessionId
    T 'session privs' { $r = Api -Path "/api/sessions/$sid/privs"; $o = $r | ConvertFrom-Json; if ($o -ne $null) { $true } else { $r.Substring(0, [Math]::Min(150, $r.Length)) } }
    T 'ps' { $r = Api -Path "/api/sessions/$sid/ps"; $o = $r | ConvertFrom-Json; if ($o.processes) { $true } else { $r.Substring(0, [Math]::Min(150, $r.Length)) } }
    T 'ifconfig' { $r = Api -Path "/api/sessions/$sid/ifconfig"; $o = $r | ConvertFrom-Json; if ($o.interfaces) { $true } else { $r.Substring(0, [Math]::Min(150, $r.Length)) } }
    T 'netstat' { $r = Api -Path "/api/sessions/$sid/netstat"; $o = $r | ConvertFrom-Json; if ($o.entries -ne $null) { $true } else { $r.Substring(0, [Math]::Min(150, $r.Length)) } }
    T 'env' { $r = Api -Path "/api/sessions/$sid/env"; $o = $r | ConvertFrom-Json; if ($o.env) { $true } else { $r.Substring(0, [Math]::Min(150, $r.Length)) } }
    T 'pwd' { $r = Api -Path "/api/sessions/$sid/fs/pwd"; $o = $r | ConvertFrom-Json; if ($o.Path) { $true } else { $r.Substring(0, [Math]::Min(150, $r.Length)) } }
    T 'ls' { $r = Api -Path "/api/sessions/$sid/fs"; $o = $r | ConvertFrom-Json; if ($o.Path -ne $null) { $true } else { $r.Substring(0, [Math]::Min(150, $r.Length)) } }
    T 'token-owner' { $r = Api -Path "/api/sessions/$sid/token-owner"; if ($r -notmatch 'error') { $true } else { $r.Substring(0, [Math]::Min(150, $r.Length)) } }
    T 'execute' {
        $r = Api -Path "/api/sessions/$sid/exec" -Method 'POST' -Body '{"path":"whoami","args":[]}'
        $o = $r | ConvertFrom-Json
        if ($o.Stdout -ne $null -or $o.Output -ne $null -or $o.stdout -ne $null) { $true } else { $r.Substring(0, [Math]::Min(200, $r.Length)) }
    }
    T 'persistence catalog' { $r = Api -Path "/api/persistence/modules"; $o = $r | ConvertFrom-Json; if ($o.modules) { $true } else { $r.Substring(0, [Math]::Min(150, $r.Length)) } }
    T 'persistence scan' { $r = Api -Path "/api/sessions/$sid/persistence"; $o = $r | ConvertFrom-Json; if ($o.platform -ne $null) { $true } else { $r.Substring(0, [Math]::Min(150, $r.Length)) } }
    T 'mimikatz modules' { $r = Api -Path '/api/mimikatz/modules'; $o = $r | ConvertFrom-Json; if ($o.commands) { $true } else { $r.Substring(0, [Math]::Min(150, $r.Length)) } }
}

Write-Host "`nc2tool e2e :: phase=$Phase base=$Base" -ForegroundColor White

switch ($Phase) {
    'generate' { Phase-Generate }
    'matrix' { Phase-Matrix }
    'listeners' { Phase-Listeners }
    'sessions' { Phase-Sessions }
    'all' {
        Phase-Generate
        Phase-Listeners
        if ($SessionId) { Phase-Sessions }
    }
    default { Write-Host "unknown phase: $Phase" -ForegroundColor Red; exit 2 }
}

Write-Host "`n=== 小结 ===" -ForegroundColor White
Write-Host ("  PASS: " + $script:pass) -ForegroundColor Green
Write-Host ("  FAIL: " + $script:fail) -ForegroundColor $(if ($script:fail) { 'Red' } else { 'Green' })
if ($script:fail) {
    Write-Host "`n失败项:" -ForegroundColor Red
    $script:fails | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
}
