<#
.SYNOPSIS
  Builds the Sliver server binaries that sliverreshine embeds.

.DESCRIPTION
  Builds ./server out of a BishopFox/sliver checkout with the build tags Sliver
  itself uses (`go_sqlite server`) and CGO disabled.

  The `server` tag matters: server/assets/assets_<os>_<arch>.go are gated behind
  it, and without it the package fails to compile because the embedded asset
  filesystem (assetsFs) is never declared.

  The checkout must have run `go run ./util/cmd/assets` at least once, otherwise
  server/assets/fs/<os>/<arch>/ is empty and the embed directive has nothing to
  match.

.EXAMPLE
  ./build/build-server.ps1 -SliverDir ..\..\_refs\sliver-173 -Targets linux/amd64,windows/amd64
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$SliverDir,

    [string[]]$Targets = @('linux/amd64', 'linux/arm64', 'windows/amd64', 'windows/arm64'),

    [string]$Out = (Join-Path $PSScriptRoot '..\..\_out'),

    [switch]$FetchAssets
)

$ErrorActionPreference = 'Stop'

$sliver = (Resolve-Path -LiteralPath $SliverDir).Path
$outDir = [System.IO.Path]::GetFullPath($Out)
New-Item -ItemType Directory -Force -Path $outDir | Out-Null

# The asset fetcher downloads the Go compiler, Zig and garble that get baked
# into the server binary. Without it the build fails on the embed directive.
if ($FetchAssets) {
    Write-Host "[server] fetching embedded compiler assets (Go + Zig + garble)"
    Push-Location $sliver
    try {
        & go run -mod=vendor ./util/cmd/assets
        if ($LASTEXITCODE -ne 0) { throw "asset fetch failed" }
        New-Item -ItemType File -Force -Path (Join-Path $sliver '.downloaded_assets') | Out-Null
    }
    finally {
        Pop-Location
    }
}

# Sliver's own build tags. `server` is the one people forget.
$tags = 'go_sqlite server'

foreach ($target in $Targets) {
    $parts = $target.Split('/')
    if ($parts.Count -ne 2) { throw "invalid target '$target', expected os/arch" }
    $goos, $goarch = $parts[0], $parts[1]

    $suffix = if ($goos -eq 'windows') { '.exe' } else { '' }
    $name = "sliver-server-$goos-$goarch$suffix"
    $dest = Join-Path $outDir $name

    Write-Host "[server] building $goos/$goarch -> $dest"

    $env:GOOS = $goos
    $env:GOARCH = $goarch
    $env:CGO_ENABLED = '0'
    $env:GOFLAGS = '-mod=vendor'

    Push-Location $sliver
    try {
        & go build -tags $tags -trimpath -o $dest ./server
        if ($LASTEXITCODE -ne 0) { throw "build failed for $target" }
    }
    finally {
        Pop-Location
    }

    $len = (Get-Item -LiteralPath $dest).Length
    Write-Host ("[server] {0} ({1:N1} MB)" -f $name, ($len / 1MB))
}

Write-Host '[server] done'
