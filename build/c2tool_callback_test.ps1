# c2tool callback verification.
#
# The 183-route sweep proved routing, auth, validation and error codes. It did NOT
# prove that a payload builds and calls home, because nearly every route was
# driven with a fake identifier. This script closes that gap: it generates a real
# implant, runs it, and waits for the console to report a live session or beacon.
#
# ASCII only. PowerShell 5.1 reads a BOM-less script as ANSI, and it parses "&&"
# and "&" inside double quotes as its own operators, so shell snippets are written
# to files rather than inlined.

param(
    [string]$Base = 'http://127.0.0.1:19000',
    [string]$Cred = '',
    [string]$Work = "$env:TEMP\c2back",
    [string]$Only = '',
    [int]$WaitSec = 40
)

$ErrorActionPreference = 'Continue'
$curl = "$env:SystemRoot\system32\curl.exe"
New-Item -ItemType Directory -Force -Path $Work | Out-Null

$script:pass = 0
$script:fail = 0
$script:skip = 0
$script:rows = @()

function Api {
    param([string]$Path, [string]$Method = 'GET', [string]$Body = '')
    $a = @('-s', '-u', $Cred, '-X', $Method, '--max-time', '400')
    if ($Body) {
        $f = Join-Path $Work 'req.json'
        [System.IO.File]::WriteAllText($f, $Body, (New-Object System.Text.UTF8Encoding($false)))
        $a += @('-H', 'Content-Type: application/json', '--data-binary', "@$f")
    }
    $a += "$Base$Path"
    return ((& $curl @a 2>&1) -join '')
}

function Listener {
    param([string]$Type, [string]$Addr, [int]$Port, [bool]$TLS = $false, [string]$Domains = '')
    $extra = ''
    if ($Domains) { $extra = ',"domains":[' + $Domains + ']' }
    $tlsTxt = if ($TLS) { 'true' } else { 'false' }
    $json = '{"type":"' + $Type + '","addr":"' + $Addr + '","port":' + $Port + ',"tls":' + $tlsTxt + $extra + '}'
    $r = Api -Path '/api/listeners' -Method 'POST' -Body $json
    try { $o = $r | ConvertFrom-Json; return $o.success } catch { return $false }
}

function Generate {
    param(
        [string]$Name, [string]$Os, [string]$Arch, [string]$Format,
        [string]$Proto, [int]$Port,
        [bool]$Beacon = $false, [bool]$Obfuscate = $false, [bool]$Evasion = $false,
        [string]$Ext = ''
    )
    $beaconTxt = if ($Beacon) { 'true' } else { 'false' }
    $evaTxt = if ($Evasion) { 'true' } else { 'false' }
    $obfTxt = if ($Obfuscate) { 'true' } else { 'false' }
    $json = '{"name":"' + $Name + '","os":"' + $Os + '","arch":"' + $Arch + '","format":"' + $Format + '",' +
            '"c2":[{"address":"127.0.0.1:' + $Port + '","protocol":"' + $Proto + '"}],' +
            '"interval":3,"jitter":0,"mtls":true,"http":false,"dns":false,"wireguard":false,' +
            '"debug":false,"evasion":' + $evaTxt + ',"obfuscate":' + $obfTxt + ',' +
            '"isBeacon":' + $beaconTxt + ',' +
            '"limitDomainJoined":false,"transport":"' + $Proto + '","maxConnectionErrors":100}'
    $r = Api -Path '/api/generate' -Method 'POST' -Body $json
    try { $o = $r | ConvertFrom-Json } catch { return @{ ok = $false; err = "bad json: $r" } }
    if ($o.error) { return @{ ok = $false; err = $o.error } }
    if (-not $o.data) { return @{ ok = $false; err = 'no data field' } }
    $bytes = [Convert]::FromBase64String($o.data)
    $file = Join-Path $Work ($Name + $Ext)
    [System.IO.File]::WriteAllBytes($file, $bytes)
    return @{ ok = $true; path = $file; bytes = $bytes.Length }
}

function Snapshot {
    $ids = @()
    try { $s = (Api -Path '/api/sessions') | ConvertFrom-Json; $ids = @($s.sessions | ForEach-Object { $_.ID }) } catch { }
    return $ids
}

function SnapshotBeacons {
    $ids = @()
    try { $b = (Api -Path '/api/beacons') | ConvertFrom-Json; $ids = @($b.beacons | ForEach-Object { $_.ID }) } catch { }
    return $ids
}

function WaitForCallback {
    param([string[]]$BeforeSessions, [string[]]$BeforeBeacons, [int]$Wait, [int]$ProcId = 0)
    $deadline = (Get-Date).AddSeconds($Wait)
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Seconds 3
        try {
            $s = (Api -Path '/api/sessions') | ConvertFrom-Json
            foreach ($sess in @($s.sessions)) {
                if ($BeforeSessions -notcontains $sess.ID) {
                    return @{ ok = $true; kind = 'session'; id = $sess.ID; name = $sess.Name }
                }
            }
            $x = (Api -Path '/api/beacons') | ConvertFrom-Json
            foreach ($be in @($x.beacons)) {
                if ($BeforeBeacons -notcontains $be.ID) {
                    return @{ ok = $true; kind = 'beacon'; id = $be.ID; name = $be.Name }
                }
            }
        } catch { }
        if ($ProcId -gt 0) {
            $pr = Get-Process -Id $ProcId -ErrorAction SilentlyContinue
            if (-not $pr) { return @{ ok = $false; err = 'payload process exited without calling back' } }
        }
    }
    return @{ ok = $false; err = "no callback within ${Wait}s" }
}

function Record {
    param([string]$Case, [string]$Result, [string]$Detail)
    $script:rows += [pscustomobject]@{ Case = $Case; Result = $Result; Detail = $Detail }
    $colour = switch ($Result) { 'PASS' { 'Green' } 'FAIL' { 'Red' } default { 'Yellow' } }
    Write-Host ("  {0,-6} {1,-38} {2}" -f $Result, $Case, $Detail) -ForegroundColor $colour
    switch ($Result) { 'PASS' { $script:pass++ } 'FAIL' { $script:fail++ } default { $script:skip++ } }
}

function WslPath {
    param([string]$WindowsPath)
    return '/mnt/c/' + ($WindowsPath -replace '^C:\\', '' -replace '\\', '/')
}

function WriteShell {
    param([string]$Name, [string]$Body)
    $p = Join-Path $Work $Name
    [System.IO.File]::WriteAllText($p, ($Body -replace "`r`n", "`n"), (New-Object System.Text.UTF8Encoding($false)))
    return (WslPath $p)
}

Write-Host "`n=== 1. listeners ===" -ForegroundColor Cyan
$listeners = @(
    @{ t = 'mtls';      p = 9001; tls = $false; label = 'mtls' },
    @{ t = 'http';      p = 9002; tls = $false; label = 'http' },
    @{ t = 'http';      p = 9003; tls = $true;  label = 'https' },
    @{ t = 'dns';       p = 9053; tls = $false; label = 'dns' },
    @{ t = 'wireguard'; p = 9004; tls = $false; label = 'wireguard' }
)
foreach ($l in $listeners) {
    # Match on the full case name, not the bare label: the label is "mtls" while
    # the case is "listener mtls", so filtering on the label skips every listener.
    if ($Only -and ("listener " + $l.label) -notmatch $Only) { continue }
    if ($l.t -eq 'dns') {
        $ok = Listener -Type $l.t -Addr '127.0.0.1' -Port $l.p -TLS $false -Domains '"c2.example.com"'
    } else {
        $ok = Listener -Type $l.t -Addr '127.0.0.1' -Port $l.p -TLS $l.tls
    }
    Record -Case "listener $($l.label)" -Result $(if ($ok) { 'PASS' } else { 'FAIL' }) -Detail "port $($l.p)"
}

Write-Host "`n=== 2. Windows payloads: generate, run, callback ===" -ForegroundColor Cyan
$winCases = @(
    @{ name = 'p-win-mtls';  proto = 'mtls';      port = 9001; label = 'windows/amd64 exe mtls' },
    @{ name = 'p-win-http';  proto = 'http';      port = 9002; label = 'windows/amd64 exe http' },
    @{ name = 'p-win-https'; proto = 'https';     port = 9003; label = 'windows/amd64 exe https' },
    @{ name = 'p-win-dns';   proto = 'dns';       port = 9053; label = 'windows/amd64 exe dns' },
    @{ name = 'p-win-wg';    proto = 'wireguard'; port = 9004; label = 'windows/amd64 exe wireguard' }
)
foreach ($c in $winCases) {
    if ($Only -and $c.label -notmatch $Only) { continue }
    $g = Generate -Name $c.name -Os 'windows' -Arch 'amd64' -Format 'exe' -Proto $c.proto -Port $c.port -Ext '.exe'
    if (-not $g.ok) { Record -Case $c.label -Result 'FAIL' -Detail "generate: $($g.err)"; continue }

    $bs = Snapshot; $bb = SnapshotBeacons
    $proc = Start-Process -FilePath $g.path -PassThru -WindowStyle Hidden
    $r = WaitForCallback -BeforeSessions $bs -BeforeBeacons $bb -Wait $WaitSec -ProcId $proc.Id
    if ($r.ok) {
        Record -Case $c.label -Result 'PASS' -Detail ("{0} {1} ({2}MB)" -f $r.kind, $r.name, [math]::Round($g.bytes / 1MB, 1))
    } else {
        Record -Case $c.label -Result 'FAIL' -Detail $r.err
    }
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
}

Write-Host "`n=== 3. Windows variants: arch, obfuscation, evasion, beacon ===" -ForegroundColor Cyan
$variants = @(
    @{ name = 'p-win386';  arch = '386';   obf = $false; eva = $false; beacon = $false; label = 'windows/386 exe' },
    @{ name = 'p-winobf';  arch = 'amd64'; obf = $true;  eva = $false; beacon = $false; label = 'windows/amd64 obfuscated' },
    @{ name = 'p-wineva';  arch = 'amd64'; obf = $false; eva = $true;  beacon = $false; label = 'windows/amd64 evasion' },
    @{ name = 'p-winboth'; arch = 'amd64'; obf = $true;  eva = $true;  beacon = $false; label = 'windows/amd64 obf+evasion' },
    @{ name = 'p-winbcn';  arch = 'amd64'; obf = $false; eva = $false; beacon = $true;  label = 'windows/amd64 beacon' }
)
foreach ($v in $variants) {
    if ($Only -and $v.label -notmatch $Only) { continue }
    $g = Generate -Name $v.name -Os 'windows' -Arch $v.arch -Format 'exe' -Proto 'mtls' -Port 9001 -Beacon $v.beacon -Obfuscate $v.obf -Evasion $v.eva -Ext '.exe'
    if (-not $g.ok) { Record -Case $v.label -Result 'FAIL' -Detail "generate: $($g.err)"; continue }

    $bs = Snapshot; $bb = SnapshotBeacons
    $proc = Start-Process -FilePath $g.path -PassThru -WindowStyle Hidden
    $r = WaitForCallback -BeforeSessions $bs -BeforeBeacons $bb -Wait $WaitSec -ProcId $proc.Id
    if ($r.ok) {
        Record -Case $v.label -Result 'PASS' -Detail ("{0} {1} ({2}MB)" -f $r.kind, $r.name, [math]::Round($g.bytes / 1MB, 1))
    } else {
        Record -Case $v.label -Result 'FAIL' -Detail $r.err
    }
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
}

Write-Host "`n=== 4. Linux payloads, run inside WSL ===" -ForegroundColor Cyan
$linCases = @(
    @{ name = 'p-lin-mtls'; proto = 'mtls'; port = 9001; label = 'linux/amd64 exe mtls' },
    @{ name = 'p-lin-http'; proto = 'http'; port = 9002; label = 'linux/amd64 exe http' }
)
foreach ($c in $linCases) {
    if ($Only -and $c.label -notmatch $Only) { continue }
    $g = Generate -Name $c.name -Os 'linux' -Arch 'amd64' -Format 'exe' -Proto $c.proto -Port $c.port
    if (-not $g.ok) { Record -Case $c.label -Result 'FAIL' -Detail "generate: $($g.err)"; continue }

    $src = WslPath $g.path
    $shBody = "#!/bin/bash" + "`n" + "cp '" + $src + "' /tmp/payload.bin" + "`n" + "chmod +x /tmp/payload.bin" + "`n" + "setsid nohup /tmp/payload.bin > /tmp/payload.log 2>&1 &" + "`n" + "echo started" + "`n"
    $sh = WriteShell -Name 'run_payload.sh' -Body $shBody
    $bs = Snapshot; $bb = SnapshotBeacons
    $out = (& wsl.exe -- bash $sh 2>&1) -join ' '
    if ($out -notmatch 'started') {
        Record -Case $c.label -Result 'FAIL' -Detail "wsl launch failed: $out"
        continue
    }
    $r = WaitForCallback -BeforeSessions $bs -BeforeBeacons $bb -Wait $WaitSec
    if ($r.ok) {
        Record -Case $c.label -Result 'PASS' -Detail ("{0} {1} ({2}MB)" -f $r.kind, $r.name, [math]::Round($g.bytes / 1MB, 1))
    } else {
        $tailSh = WriteShell -Name 'tail.sh' -Body ("#!/bin/bash" + "`n" + "tail -3 /tmp/payload.log 2>/dev/null" + "`n")
        $log = (& wsl.exe -- bash $tailSh 2>&1) -join ' '
        Record -Case $c.label -Result 'FAIL' -Detail ("{0}; log: {1}" -f $r.err, $log)
    }
    $killSh = WriteShell -Name 'kill.sh' -Body ("#!/bin/bash" + "`n" + "pkill -f /tmp/payload.bin 2>/dev/null; echo done" + "`n")
    & wsl.exe -- bash $killSh 2>&1 | Out-Null
}

Write-Host "`n=== summary ===" -ForegroundColor White
Write-Host ("  PASS: " + $script:pass) -ForegroundColor Green
Write-Host ("  FAIL: " + $script:fail) -ForegroundColor $(if ($script:fail) { 'Red' } else { 'Green' })
Write-Host ("  SKIP: " + $script:skip) -ForegroundColor Yellow
if ($script:fail) {
    Write-Host "`n  failures:" -ForegroundColor Red
    $script:rows | Where-Object { $_.Result -eq 'FAIL' } | ForEach-Object {
        Write-Host ("    {0} : {1}" -f $_.Case, $_.Detail) -ForegroundColor Red
    }
}
$script:rows | Export-Csv -Path (Join-Path $Work 'callback_results.csv') -NoTypeInformation -Encoding UTF8