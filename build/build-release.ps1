<#
.SYNOPSIS
  Assembles the shippable c2tool archive.

.DESCRIPTION
  Produces dist/c2tool-<os>-<arch>.zip containing the single launcher binary.
  The Sliver server for that same platform is embedded inside it as a gzip
  payload, so nothing else needs to ship.

  Steps: build the frontend, collect it into web/dist (go:embed), compress the
  matching server binary into internal/embed/serverbin/payload-<goos>-<goarch>.gz,
  cross-compile the launcher with the embedserver tag, and zip it.

  The server binary is chosen from -Server (or -ServerDir) by reading each
  candidate's own header, not by trusting its filename. Pairing the launcher
  with the server for the same platform is the whole point: a Windows launcher
  carrying a Linux server starts and then dies at exec time with an error that
  names neither.

  Exactly one payload is present at build time. internal/embed embeds every
  payload-*.gz it finds, so leaving a second platform's file behind would not
  break the launcher — it would silently double the archive to no purpose, since
  a launcher can only ever run the server for the platform it was built for.

.EXAMPLE
  ./build/build-release.ps1 -ServerDir ..\..\_out -GOOS windows -GOARCH amd64
  ./build/build-release.ps1 -ServerDir ..\..\_out -GOOS linux -GOARCH amd64
#>
[CmdletBinding()]
param(
    # Either an explicit server binary, or a directory to pick one out of by
    # platform. Both are validated against the binary's own header.
    [string]$Server = '',

    [string]$ServerDir = '',

    [string]$GOOS = 'linux',

    [string]$GOARCH = 'amd64',

    [switch]$SkipFrontend,

    [switch]$SkipServerEmbed,

    # Pack the launcher with UPX and name the archive accordingly.
    #
    # The README has always advertised a *-upx.zip alongside the *-plain.zip, and
    # the v0.1 release shipped both -- but this script only ever produced one
    # archive, under a name that matched neither. Anyone rebuilding from source
    # got an artifact whose name did not match the documentation, and no way to
    # produce the packed variant at all.
    [switch]$Upx,

    # Path to upx.exe. Defaults to a copy in tools\upx\ or one on PATH.
    [string]$UpxPath = ''
)

$ErrorActionPreference = 'Stop'

$root = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$dist = Join-Path $root 'dist'
# The variant is part of both the archive name and the staging directory name
# because the two archives are different artifacts: an operator picking one from
# a release page needs to know which they are getting, and
# "c2tool-linux-amd64.zip" cannot say.
#
# Scoping the stage per variant also matters for correctness. The stage holds the
# binary between the build and the zip, and the UPX branch rewrites it in place
# before probing it. When both variants stage through one directory, a plain
# build running alongside an -Upx build can replace the packed launcher with an
# unpacked one between the pack and the liveness check -- and that check then
# reports "the packed launcher does not run" for a binary it never packed.
$variant = if ($Upx) { 'upx' } else { 'plain' }
$stage = Join-Path $dist "c2tool-$GOOS-$GOARCH-$variant"
$zip = Join-Path $dist "c2tool-$GOOS-$GOARCH-$variant.zip"

function Step($msg) { Write-Host "`n=== $msg ===" -ForegroundColor Cyan }

# Runs a native command whose stderr may carry warnings.
#
# Windows PowerShell 5.1 turns any stderr write from a native process into a
# terminating NativeCommandError while $ErrorActionPreference is 'Stop', even
# when the process succeeded with exit code 0. npm and node routinely write
# progress and warnings to stderr, so those calls run with the preference
# relaxed and are judged by $LASTEXITCODE instead.
function Invoke-Native {
    param([scriptblock]$Command)
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        & $Command
        return $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $prev
    }
}


# --- 1. frontend -------------------------------------------------------------
if (-not $SkipFrontend) {
    Step 'building frontend'
    Push-Location (Join-Path $root 'frontend')
    try {
        # Invoke npm.cmd, not npm: PowerShell resolves the bare name to npm.ps1,
        # whose argument handling mangles a redirected invocation into
        # "Unknown command: pm".
        $npm = 'npm.cmd'
        if (-not (Test-Path 'node_modules')) {
            $ciCode = Invoke-Native { & $npm ci --no-audit --no-fund }
            if ($ciCode -ne 0) { throw "npm ci failed (exit $ciCode)" }
        }
        # Judge success by the artifact, not by stderr noise: Vite prints a
        # chunk-size warning on every build.
        $indexHtml = Join-Path $root 'frontend\dist\index.html'
        $feLog = Join-Path $root 'build\frontend.log'
        if (Test-Path $indexHtml) { Remove-Item -Force $indexHtml }
        $feCode = Invoke-Native { & $npm run build *> $feLog }
        Get-Content $feLog -Tail 6 | ForEach-Object { Write-Host "    $_" }
        if (-not (Test-Path $indexHtml)) {
            Get-Content $feLog -Tail 40 | ForEach-Object { Write-Host "    $_" }
            throw 'frontend build produced no dist/index.html'
        }
    }
    finally {
        Pop-Location
    }

    Step 'collecting frontend into web/dist'
    $webDist = Join-Path $root 'web\dist'
    if (Test-Path $webDist) { Remove-Item -Recurse -Force "$webDist\*" }
    else { New-Item -ItemType Directory -Force -Path $webDist | Out-Null }
    Copy-Item -Recurse -Force (Join-Path $root 'frontend\dist\*') $webDist

    # .gitkeep is the only file in this directory that git tracks, and the wipe
    # above removes it along with the previous build's output. Restoring it keeps
    # a release run from leaving the worktree dirty with a deleted file -- which
    # a CI clean-tree check reads as a build failure unrelated to the build.
    New-Item -ItemType File -Path (Join-Path $webDist '.gitkeep') -Force | Out-Null
}

# --- 2. server payload -------------------------------------------------------
#
# The payload must be the server for the platform the launcher is being built
# for. embed-payload.ps1 reads the platform out of the binary itself, so a
# mislabelled file cannot get through; what this block adds is picking the right
# file in the first place, and removing any other platform's payload so the
# launcher does not carry a second 300 MB server it can never run.
if (-not $SkipServerEmbed) {
    Step 'embedding server payload'

    $serverBin = $Server
    if (-not $serverBin) {
        if (-not $ServerDir) {
            throw 'pass -Server <binary> or -ServerDir <dir> so the launcher can embed the server for its own platform'
        }
        if (-not (Test-Path -LiteralPath $ServerDir)) {
            throw "server directory not found: $ServerDir"
        }

        # Candidate names differ per platform (sliver-server.exe on Windows,
        # sliver-server-<os>-<arch> from build-server.ps1), so the directory is
        # scanned and each candidate's header decides whether it fits. The
        # filename is only used to skip obviously unrelated files.
        $candidates = Get-ChildItem -LiteralPath $ServerDir -File |
            Where-Object { $_.Name -like 'sliver-server*' -and $_.Name -notlike '*.pre-*' -and $_.Name -notlike '*.log' }

        foreach ($cand in $candidates) {
            # -Probe detects the platform and writes nothing. It throws when
            # the binary is for another platform, or is not an executable at
            # all; either way the answer is "keep looking". Success of the call
            # is the match — the probe's text goes to the host stream, which
            # PowerShell does not let us capture, so it is not what we test.
            try {
                & (Join-Path $PSScriptRoot 'embed-payload.ps1') -Server $cand.FullName -GOOS $GOOS -GOARCH $GOARCH -Probe | Out-Null
            }
            catch {
                Write-Host "    skip $($cand.Name): $($_.Exception.Message)"
                continue
            }
            $serverBin = $cand.FullName
            break
        }
        if (-not $serverBin) {
            $names = ($candidates | ForEach-Object { $_.Name }) -join ', '
            throw "no sliver-server for $GOOS/$GOARCH in $ServerDir (found: $names). Build it with build/build-server.ps1 -Targets $GOOS/$GOARCH"
        }
    }

    & (Join-Path $PSScriptRoot 'embed-payload.ps1') -Server $serverBin -GOOS $GOOS -GOARCH $GOARCH
    # Check the artifact, not $LASTEXITCODE: Write-Host output from the child
    # script can make the caller observe a non-zero code even on success.
    $payload = Join-Path $root "internal\embed\serverbin\payload-$GOOS-$GOARCH.gz"
    if (-not (Test-Path $payload)) { throw "embed-payload produced no payload-$GOOS-$GOARCH.gz" }

    # Exactly one payload stays in the tree: the one just written. Anything else
    # is another platform's server, which would be embedded and shipped dead.
    Get-ChildItem -LiteralPath (Join-Path $root 'internal\embed\serverbin') -Filter 'payload-*.gz' |
        Where-Object { $_.Name -ne "payload-$GOOS-$GOARCH.gz" } |
        ForEach-Object {
            Write-Host "    removing unused payload $($_.Name)"
            Remove-Item -LiteralPath $_.FullName -Force
        }
}

# --- 3. launcher -------------------------------------------------------------
Step "building launcher for $GOOS/$GOARCH"
New-Item -ItemType Directory -Force -Path $dist | Out-Null
if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }
New-Item -ItemType Directory -Force -Path $stage | Out-Null

$binName = if ($GOOS -eq 'windows') { 'c2tool.exe' } else { 'c2tool' }
$binPath = Join-Path $stage $binName

$env:GOOS = $GOOS
$env:GOARCH = $GOARCH
$env:CGO_ENABLED = '0'

# Pin the module mode instead of inheriting it. build-server.ps1 sets
# GOFLAGS=-mod=vendor for the sliver tree, and when the two scripts run in one
# shell that value survives into this one — where it fails with "inconsistent
# vendoring", because this module has no vendor directory and resolves sliver
# through the replace directive in go.mod. -mod=mod is the mode that directive
# needs.
$env:GOFLAGS = '-mod=mod'

# The embedserver tag swaps internal/embed/embed_dev.go for embed_release.go,
# which is what actually carries the server payload. The mimikatzshellcode tag is
# added only when the donut blob is on disk: the //go:embed directive for it fails
# to compile otherwise, so an unconditional tag would make every build depend on
# a tool the operator may not have.
#
# The blob is what makes the credential harvest run without writing mimikatz to
# the target's disk, so it is worth generating — but its absence is a working
# build, not a broken one.
$buildTags = 'embedserver'
$shellcodeBlob = Join-Path $PSScriptRoot '..\internal\embed\mimikatz\mimikatz.x64.bin'
if (Test-Path $shellcodeBlob) {
    $buildTags = "$buildTags,mimikatzshellcode"
    Write-Host ("    in-memory harvest enabled (shellcode blob {0:N0} KB)" -f ((Get-Item $shellcodeBlob).Length / 1KB))
} else {
    Write-Host '    in-memory harvest disabled (no shellcode blob; run build/build-mimikatz-shellcode.ps1)'
}

& go build -tags $buildTags -trimpath -o $binPath ./cmd/c2tool
if ($LASTEXITCODE -ne 0) { throw 'launcher build failed' }

# --- 3b. optional UPX packing ------------------------------------------------
if ($Upx) {
    Step 'packing with upx'

    if (-not $UpxPath) {
        $candidates = @(
            (Join-Path $root 'tools\upx\upx.exe'),
            (Join-Path $root 'tools\upx.exe'),
            (Join-Path $PSScriptRoot 'upx.exe')
        )
        foreach ($c in $candidates) {
            if (Test-Path $c) { $UpxPath = $c; break }
        }
    }
    if (-not $UpxPath) {
        $onPath = Get-Command upx.exe, upx -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($onPath) { $UpxPath = $onPath.Source }
    }
    if (-not $UpxPath -or -not (Test-Path $UpxPath)) {
        throw 'upx was not found. Put upx.exe in tools\upx\ or pass -UpxPath. Get it from https://github.com/upx/upx/releases. Without it, build the plain variant.'
    }

    $before = (Get-Item $binPath).Length
    # --best is slow but this is a one-off release step. The launcher is mostly
    # the gzipped server payload, which UPX cannot compress further, so the win
    # is on the Go code around it.
    & $UpxPath --best --lzma -q --force $binPath
    if ($LASTEXITCODE -ne 0) { throw "upx failed with $LASTEXITCODE" }
    $after = (Get-Item $binPath).Length

    Write-Host ("    {0:N1} MB -> {1:N1} MB ({2:N0}%)" -f ($before / 1MB), ($after / 1MB), (100 * $after / $before))

    # A packed binary is only useful if it still runs, and UPX corrupts a
    # binary often enough that shipping one unchecked is not worth the disk it
    # saves. --help answers without touching any state, so this is a safe
    # liveness check rather than a functional one.
    # The launcher prints its usage to stderr, and Windows PowerShell 5.1 turns
    # any stderr write from a native process into a terminating
    # NativeCommandError while $ErrorActionPreference is 'Stop' -- even when the
    # process succeeded. That is the same trap Invoke-Native exists for, and it
    # made this check, the one meant to catch a broken binary, kill a healthy
    # build instead: the packed launcher ran, printed its usage and exited 0,
    # and the script still died on the line below without ever reaching the
    # packaging step, so no archive was produced. Relax the preference and
    # judge the binary by its exit code and its output, not by which stream it
    # chose to write to.
    # The probe can only run when the target IS the host. Trying to execute a
    # linux binary on Windows does not fail with a useful exit code: PowerShell
    # tries to open the file as a document and throws "Cannot run a document in
    # the middle of a pipeline" from inside this script, aborting the build
    # before packaging -- so a perfectly good binary produced no archive at all.
    #
    # That is worse than skipping the check, because it reports a launcher
    # problem when the real situation is that the launcher targets another OS.
    $hostOs = if ($env:OS -eq 'Windows_NT') { 'windows' } elseif ($IsMacOS) { 'darwin' } else { 'linux' }
    if ($GOOS -ne $hostOs) {
        Write-Host "    skipping the --help probe: target is $GOOS, this host is $hostOs"
    }
    else {
        $probeEap = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        try {
            $probe = (& $binPath --help 2>&1 | Out-String)
            $probeExit = $LASTEXITCODE
        }
        finally {
            $ErrorActionPreference = $probeEap
        }
        if ($probeExit -ne 0) {
            throw "the packed launcher does not run (exit $probeExit); rebuild without -Upx"
        }
        if ($probe -notmatch 'Usage of') {
            throw 'the packed launcher ran but printed no usage text'
        }
        Write-Host '    packed launcher responds to --help'
    }
}

# --- 4. package --------------------------------------------------------------
#
# The archive holds the binary and nothing else. The binary writes its own
# settings, its own login record and its own deployment notes on first start, so
# shipping run.sh or a README alongside it would be redundant — and worse, it
# would imply the operator has to run a script rather than just the file.
Step 'packaging'

if (Test-Path $zip) { Remove-Item -Force $zip }

# Compress-Archive cannot write unix mode bits, which makes the archive
# unusable after a plain `unzip` on Linux: both files land as 0644 and
# `./run.sh` fails with "Permission denied". Build the archive with the .NET
# ZipArchive API instead and set the external attributes so the executable bits
# survive the round trip.
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem

# unix mode -> zip external attributes: mode << 16
function Get-ZipExternalAttrs([int]$Mode) {
    return [int]($Mode -shl 16)
}

$zipStream = [System.IO.File]::Open($zip, [System.IO.FileMode]::CreateNew)
try {
    $archive = New-Object System.IO.Compression.ZipArchive(
        $zipStream, [System.IO.Compression.ZipArchiveMode]::Create
    )
    try {
        # One entry, 0755. The archive exists only so the executable bit
        # survives transport: a plain copy of the binary over a Windows share or
        # through a browser would land as 0644 and `./c2tool` would fail with
        # "Permission denied" on the target.
        $entries = @(
            @{ Path = $binPath; Mode = 0x81ED } # 0100755
        )
        foreach ($e in $entries) {
            $name = [System.IO.Path]::GetFileName($e.Path)
            $entry = $archive.CreateEntry($name, [System.IO.Compression.CompressionLevel]::Optimal)
            $entry.ExternalAttributes = Get-ZipExternalAttrs $e.Mode
            $inStream = [System.IO.File]::OpenRead($e.Path)
            try {
                $outStream = $entry.Open()
                try { $inStream.CopyTo($outStream) } finally { $outStream.Dispose() }
            } finally { $inStream.Dispose() }
        }
    } finally { $archive.Dispose() }
} finally { $zipStream.Dispose() }

# The .NET ZipArchive still stamps "version made by" with the MS-DOS host id
# when it runs on Windows, and Info-ZIP's unzip only honours the high 16 bits of
# external attributes when the host id says UNIX. Without this pass everything
# extracts as 0644 and ./run.sh fails. Walk the central directory and flip the
# host byte of every entry.
function Set-ZipUnixHost {
    param([Parameter(Mandatory = $true)][string]$ZipPath)

    $bytes = [System.IO.File]::ReadAllBytes($ZipPath)

    # Locate the end-of-central-directory record (PK\x05\x06) from the tail.
    $eocd = -1
    $floor = [Math]::Max(0, $bytes.Length - 66000)
    for ($i = $bytes.Length - 22; $i -ge $floor; $i--) {
        if ($bytes[$i] -eq 0x50 -and $bytes[$i + 1] -eq 0x4B -and
            $bytes[$i + 2] -eq 0x05 -and $bytes[$i + 3] -eq 0x06) {
            $eocd = $i
            break
        }
    }
    if ($eocd -lt 0) { throw 'zip end-of-central-directory record not found' }

    $pos = [int][BitConverter]::ToUInt32($bytes, $eocd + 16)
    $patched = 0
    while (($pos + 46) -le $bytes.Length -and
           $bytes[$pos] -eq 0x50 -and $bytes[$pos + 1] -eq 0x4B -and
           $bytes[$pos + 2] -eq 0x01 -and $bytes[$pos + 3] -eq 0x02) {
        # "version made by": low byte is the spec version, high byte the host.
        $bytes[$pos + 5] = 0x03   # 3 = UNIX
        $patched++

        $nameLen    = [BitConverter]::ToUInt16($bytes, $pos + 28)
        $extraLen   = [BitConverter]::ToUInt16($bytes, $pos + 30)
        $commentLen = [BitConverter]::ToUInt16($bytes, $pos + 32)
        $pos += 46 + $nameLen + $extraLen + $commentLen
    }

    [System.IO.File]::WriteAllBytes($ZipPath, $bytes)
    return $patched
}

$patchedEntries = Set-ZipUnixHost -ZipPath $zip

$zipLen = (Get-Item -LiteralPath $zip).Length
$binLen = (Get-Item -LiteralPath $binPath).Length

Write-Host ''
Write-Host ("  archive : {0}" -f $zip)
Write-Host ("  size    : {0:N1} MB" -f ($zipLen / 1MB))
Write-Host ("  launcher: {0:N1} MB (server embedded)" -f ($binLen / 1MB))
Write-Host ''
Write-Host '  Deploy: copy the binary (or unzip it) and run ./c2tool — no other files needed'
Write-Host ("  modes   : unix host id stamped on {0} entries" -f $patchedEntries)
