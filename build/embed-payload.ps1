<#
.SYNOPSIS
  Bakes a Sliver server binary into the launcher as a gzip payload.

.DESCRIPTION
  internal/embed/embed_release.go embeds every serverbin/payload-<goos>-<goarch>.gz
  under the `embedserver` build tag and selects the one matching the host at
  runtime. This script produces one such file from a sliver-server binary built
  out of the BishopFox/sliver v1.7.3 tree.

  The platform key is NOT taken on trust from -Server's filename. It is read out
  of the binary's own header (PE / ELF / Mach-O), because the failure this script
  exists to prevent is exactly a payload whose name and contents disagree: a
  Linux server compressed into a payload a Windows launcher then embeds, which
  surfaced only at exec time as "This version of %1 is not compatible with the
  version of Windows you're running".

  Run build/build-server.ps1 first, or point -Server at any sliver-server binary
  built from that tree with the `server` build tag.

.EXAMPLE
  ./build/embed-payload.ps1 -Server ../_out/sliver-server-linux-amd64
  ./build/embed-payload.ps1 -Server ../_out/sliver-server-linux-amd64 -GOOS linux -GOARCH amd64
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Server,

    # Optional assertion. When set, the binary's detected platform must match,
    # so a mislabelled file fails the release instead of shipping.
    [string]$GOOS = '',

    [string]$GOARCH = '',

    # Detect and report only; write nothing. Used by build-release.ps1 to pick
    # the right server binary out of a directory without guessing from names.
    [switch]$Probe,

    [string]$Out = ''
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path -LiteralPath $Server)) {
    throw "server binary not found: $Server"
}

$src = (Resolve-Path -LiteralPath $Server).Path

# --- detect the binary's real platform ---------------------------------------
function Get-BinaryPlatform {
    param([Parameter(Mandatory = $true)][string]$Path)

    $fs = [System.IO.File]::OpenRead($Path)
    try {
        $head = New-Object byte[] 64
        $n = $fs.Read($head, 0, 64)
    }
    finally {
        $fs.Dispose()
    }
    if ($n -lt 4) { throw "not an executable (too short): $Path" }

    # PE: "MZ" ... e_lfanew at 0x3C -> "PE\0\0" -> machine word.
    if ($head[0] -eq 0x4D -and $head[1] -eq 0x5A) {
        $peOff = [BitConverter]::ToInt32($head, 0x3C)
        $fs = [System.IO.File]::OpenRead($Path)
        try {
            $pe = New-Object byte[] 6
            $fs.Position = $peOff
            $null = $fs.Read($pe, 0, 6)
        }
        finally {
            $fs.Dispose()
        }
        if (-not ($pe[0] -eq 0x50 -and $pe[1] -eq 0x45)) { throw "MZ header without a PE signature: $Path" }
        $machine = [BitConverter]::ToUInt16($pe, 4)
        $arch = switch ($machine) {
            0x8664 { 'amd64' }
            0xAA64 { 'arm64' }
            0x014C { '386' }
            default { throw ("unknown PE machine 0x{0:X4} in {1}" -f $machine, $Path) }
        }
        return @{ GOOS = 'windows'; GOARCH = $arch }
    }

    # ELF: 0x7F 'E' 'L' 'F', EI_DATA at 5, e_machine at 18.
    if ($head[0] -eq 0x7F -and $head[1] -eq 0x45 -and $head[2] -eq 0x4C -and $head[3] -eq 0x46) {
        $le = $head[5] -eq 1
        $machine = if ($le) { [BitConverter]::ToUInt16($head, 18) } else { [BitConverter]::ToUInt16(@($head[19], $head[18]), 0) }
        $arch = switch ($machine) {
            0x3E { 'amd64' }
            0xB7 { 'arm64' }
            0x03 { '386' }
            0x28 { 'arm' }
            default { throw ("unknown ELF machine 0x{0:X4} in {1}" -f $machine, $Path) }
        }
        return @{ GOOS = 'linux'; GOARCH = $arch }
    }

    # Mach-O, thin only: 0xFEEDFACF (64-bit).
    if ($head[0] -eq 0xCF -and $head[1] -eq 0xFA -and $head[2] -eq 0xED -and $head[3] -eq 0xFE) {
        $cpu = [BitConverter]::ToUInt32($head, 4)
        $arch = switch ($cpu) {
            0x01000007 { 'amd64' }
            0x0100000C { 'arm64' }
            default { throw ("unknown Mach-O cputype 0x{0:X8} in {1}" -f $cpu, $Path) }
        }
        return @{ GOOS = 'darwin'; GOARCH = $arch }
    }

    $magic = ($head[0..3] | ForEach-Object { $_.ToString('X2') }) -join ' '
    throw "unrecognised executable format (magic $magic): $Path"
}

$detected = Get-BinaryPlatform -Path $src

# These names must not differ from $GOOS/$GOARCH only by case. PowerShell variable
# names are case-insensitive, so an earlier version that stored the detected
# platform in $goos silently overwrote the -GOOS parameter, and the assertion
# below then compared a value against itself and never fired — which is how a
# Windows launcher came to embed the Linux server. Distinct names, deliberately.
$detectedGOOS = $detected.GOOS
$detectedGOARCH = $detected.GOARCH

if ($GOOS -and ($GOOS -ne $detectedGOOS)) {
    throw ("platform mismatch: {0} is a {1} binary but -GOOS {2} was requested" -f $src, $detectedGOOS, $GOOS)
}
if ($GOARCH -and ($GOARCH -ne $detectedGOARCH)) {
    throw ("platform mismatch: {0} is a {1} binary but -GOARCH {2} was requested" -f $src, $detectedGOARCH, $GOARCH)
}

if ($Probe) {
    Write-Host ("platform ok: {0}/{1} ({2})" -f $detectedGOOS, $detectedGOARCH, (Split-Path -Leaf $src))
    exit 0
}

if (-not $Out) {
    $Out = Join-Path $PSScriptRoot ("..\internal\embed\serverbin\payload-{0}-{1}.gz" -f $detectedGOOS, $detectedGOARCH)
}
$dst = [System.IO.Path]::GetFullPath($Out)
$dstDir = Split-Path -Parent $dst
if (-not (Test-Path -LiteralPath $dstDir)) {
    New-Item -ItemType Directory -Force -Path $dstDir | Out-Null
}

$inLen = (Get-Item -LiteralPath $src).Length
Write-Host ("[embed] {0} -> {1}/{2} ({3:N1} MB)" -f (Split-Path -Leaf $src), $detectedGOOS, $detectedGOARCH, ($inLen / 1MB))

# Compress to a temp file first so a failure never leaves a partial payload
# that would silently embed a truncated server.
$tmp = "$dst.part"
if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Force }

$in = [System.IO.File]::OpenRead($src)
try {
    $outStream = [System.IO.File]::Create($tmp)
    try {
        # Optimal is the highest level available on .NET Framework, which is what
        # Windows PowerShell 5.1 runs on. (SmallestSize is .NET Core only, and
        # resolving it there yields $null rather than an error.) The enum is
        # resolved to a variable first because New-Object cannot evaluate a static
        # member access inline on 5.1.
        $level = [System.IO.Compression.CompressionLevel]::Optimal
        $gz = New-Object System.IO.Compression.GZipStream -ArgumentList $outStream, $level
        try {
            $in.CopyTo($gz, 1MB)
        }
        finally {
            $gz.Dispose()
        }
    }
    finally {
        $outStream.Dispose()
    }
}
finally {
    $in.Dispose()
}

Move-Item -LiteralPath $tmp -Destination $dst -Force

$outLen = (Get-Item -LiteralPath $dst).Length
$ratio = if ($inLen -gt 0) { 100.0 * $outLen / $inLen } else { 0 }
Write-Host ("[embed] wrote {0} ({1:N1} MB, {2:N0}% of original)" -f $dst, ($outLen / 1MB), $ratio)
Write-Host "[embed] build the launcher for this platform with: go build -tags embedserver ./cmd/c2tool"
