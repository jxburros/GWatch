# Builds gwatch.exe for Windows (64-bit). Requires Go 1.24+ (https://go.dev/dl/).
# Usage:  powershell -ExecutionPolicy Bypass -File scripts\build.ps1 [-Version 0.1.0]
# With no -Version, the number in the repository's VERSION file is used, so a
# local build reports the same version a release of this commit would.
param(
    [string]$Version,
    [string]$Output = "dist\gwatch.exe"
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
if (-not $Version) { $Version = (Get-Content (Join-Path $root "VERSION") -Raw).Trim() }
Push-Location $root
try {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Output) | Out-Null
    $env:CGO_ENABLED = "0"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $Output .
    Write-Host "Built $Output (version $Version)"
} finally {
    Pop-Location
}
