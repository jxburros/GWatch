# Builds gwatch.exe for Windows (64-bit). Requires Go 1.24+ (https://go.dev/dl/).
# Usage:  powershell -ExecutionPolicy Bypass -File scripts\build.ps1 [-Version 1.0.0]
param(
    [string]$Version = "dev",
    [string]$Output = "dist\gwatch.exe"
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
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
