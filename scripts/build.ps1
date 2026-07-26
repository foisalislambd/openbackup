<#
.SYNOPSIS
    Builds OpenBackup on Windows: the dashboard, then the Go binaries.

.DESCRIPTION
    The Makefile is the reference build, but it needs a POSIX shell. This script
    is the same pipeline for a Windows checkout so contributors on Windows are
    not second-class: build the Next.js static export, copy it into the package
    that embeds it, then build the server and agent with version metadata.

.PARAMETER SkipWeb
    Build only the Go binaries, reusing whatever dashboard is already embedded.

.PARAMETER Desktop
    Also build the Windows desktop app. Needs the Wails CLI and, for an
    installer, NSIS on PATH.

.PARAMETER Installer
    Package the desktop app as an NSIS installer. Implies -Desktop.

.PARAMETER Test
    Run the test suite after building.

.EXAMPLE
    ./scripts/build.ps1
    ./scripts/build.ps1 -SkipWeb -Test
    ./scripts/build.ps1 -Installer
#>

[CmdletBinding()]
param(
    [switch]$SkipWeb,
    [switch]$Desktop,
    [switch]$Installer,
    [switch]$Test
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

function Step($message) {
    Write-Host ""
    Write-Host "==> $message" -ForegroundColor Cyan
}

# Version metadata is injected at link time so the binary reports exactly what
# it is, with no generated file in the tree.
$version = try { (git describe --tags --always --dirty 2>$null) } catch { 'dev' }
if (-not $version) { $version = 'dev' }
$commit = try { (git rev-parse --short HEAD 2>$null) } catch { 'unknown' }
if (-not $commit) { $commit = 'unknown' }
$date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')

$pkg = 'github.com/openbackup/openbackup/internal/version'
$ldflags = "-s -w -X $pkg.Version=$version -X $pkg.Commit=$commit -X $pkg.Date=$date"

$webOut = Join-Path $root 'web\out'
$webDist = Join-Path $root 'internal\server\web\dist'

if (-not $SkipWeb) {
    Step "Building the dashboard"
    Push-Location (Join-Path $root 'web')
    try {
        if (Test-Path 'node_modules') {
            npm install --no-audit --no-fund
        } else {
            npm ci --no-audit --no-fund
        }
        if ($LASTEXITCODE -ne 0) { throw "npm install failed" }
        npm run build
        if ($LASTEXITCODE -ne 0) { throw "next build failed" }
    } finally {
        Pop-Location
    }

    Step "Embedding the dashboard into the server package"
    if (-not (Test-Path $webOut)) { throw "expected the export in $webOut" }
    # Wiping and recreating the directory is the only way to be certain a stale
    # asset from an earlier build is gone. `rd` does the wiping because both
    # Remove-Item and .NET's recursive Delete refuse some of the names Next emits
    # for its route metadata, such as `activity\__next.activity\__PAGE__.txt`.
    if (Test-Path -LiteralPath $webDist) {
        cmd /c "rd /s /q `"$webDist`"" | Out-Null
        if (Test-Path -LiteralPath $webDist) { throw "could not clear $webDist" }
    }
    [System.IO.Directory]::CreateDirectory($webDist) | Out-Null
    # .gitkeep is tracked so //go:embed still resolves in a checkout where the
    # dashboard has never been built.
    Set-Content -LiteralPath (Join-Path $webDist '.gitkeep') -Value @(
        '# The built dashboard is copied here by `make web` or scripts/build.ps1.',
        '# This file only exists so //go:embed has a directory to embed in a fresh',
        '# checkout; the build output itself is not committed.'
    )
    Get-ChildItem -LiteralPath $webOut -Force | ForEach-Object {
        Copy-Item -LiteralPath $_.FullName -Destination $webDist -Recurse -Force
    }
    $files = (Get-ChildItem -LiteralPath $webDist -Recurse -File -Force).Count
    Write-Host "embedded $files files"
}

Step "Building the Go binaries"
New-Item -ItemType Directory -Force -Path (Join-Path $root 'bin') | Out-Null
go build -trimpath -ldflags $ldflags -o "bin/" ./cmd/...
if ($LASTEXITCODE -ne 0) { throw "go build failed" }

Get-ChildItem (Join-Path $root 'bin') -File | ForEach-Object {
    Write-Host ("  {0}  {1:N1} MB" -f $_.Name, ($_.Length / 1MB))
}

if ($Desktop -or $Installer) {
    Step "Building the desktop app"
    if (-not (Get-Command wails -ErrorAction SilentlyContinue)) {
        throw "wails is not on PATH. Install it with: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
    }
    Push-Location (Join-Path $root 'desktop')
    try {
        # Wails runs the frontend build itself, per desktop/wails.json.
        $wailsArgs = @('build', '-trimpath', '-ldflags', $ldflags)
        if ($Installer) {
            if (-not (Get-Command makensis -ErrorAction SilentlyContinue)) {
                throw "makensis is not on PATH. Install NSIS (winget install NSIS.NSIS) or drop -Installer."
            }
            $wailsArgs += '-nsis'
        }
        & wails @wailsArgs
        if ($LASTEXITCODE -ne 0) { throw "wails build failed" }
    } finally {
        Pop-Location
    }
    Get-ChildItem (Join-Path $root 'desktop\build\bin') -File | ForEach-Object {
        Write-Host ("  {0}  {1:N1} MB" -f $_.Name, ($_.Length / 1MB))
    }
}

if ($Test) {
    Step "Running tests"
    # Not ./... : the dashboard's node_modules can contain Go files from npm
    # packages, and those are not ours to test.
    go test ./cmd/... ./internal/...
    if ($LASTEXITCODE -ne 0) { throw "tests failed" }
    # The desktop app is its own module, so ./... in the root never reaches it.
    Push-Location (Join-Path $root 'desktop')
    try {
        go vet ./...
        if ($LASTEXITCODE -ne 0) { throw "desktop vet failed" }
    } finally {
        Pop-Location
    }
}

Step "Done"
Write-Host "Start the server with:  .\bin\openbackup-server.exe"
Write-Host "Then open:              http://localhost:8080"
