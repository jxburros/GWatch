# Builds the GWatch Windows setup programs end to end: compiles the Go
# executables, then wraps each one with Inno Setup.
#
#   powershell -ExecutionPolicy Bypass -File scripts\build-installer.ps1 -Version 1.0.0
#
# Needs Go 1.24+ and Inno Setup 6. Install Inno Setup with either of:
#   winget install JRSoftware.InnoSetup
#   choco install innosetup --no-progress -y
#
# -Which selects what to build: Monitor, Agent or Both (the default). The
# finished setup programs are copied into dist\ next to the executables.
[CmdletBinding()]
param(
    [string]$Version = "dev",
    [ValidateSet("Monitor", "Agent", "Both")]
    [string]$Which = "Both",
    [string]$Iscc
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$installer = Join-Path $PSScriptRoot "installer"
$dist = Join-Path $root "dist"

# ISCC.exe is not on PATH after a default Inno Setup install, so look where the
# installers actually put it before giving up.
function Resolve-Iscc {
    if ($Iscc) {
        if (Test-Path $Iscc) { return $Iscc }
        throw "No ISCC.exe at $Iscc"
    }
    $onPath = Get-Command iscc.exe -ErrorAction SilentlyContinue
    if ($onPath) { return $onPath.Source }
    $candidates = @(
        "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
        "$env:ProgramFiles\Inno Setup 6\ISCC.exe",
        "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe"
    )
    foreach ($c in $candidates) { if ($c -and (Test-Path $c)) { return $c } }
    throw "Inno Setup 6 not found. Install it with 'winget install JRSoftware.InnoSetup' or pass -Iscc <path to ISCC.exe>."
}

function Build-Exe([string]$Package, [string]$Output) {
    Write-Host "Building $Output (version $Version)..."
    $env:CGO_ENABLED = "0"; $env:GOOS = "windows"; $env:GOARCH = "amd64"
    & go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $Output $Package
    if ($LASTEXITCODE -ne 0) { throw "go build failed for $Package" }
}

function Build-Setup([string]$Script, [string]$Exe) {
    Write-Host "Compiling $Script..."
    & $isccPath "/DAppVersion=$Version" "/DExePath=$Exe" (Join-Path $installer $Script)
    if ($LASTEXITCODE -ne 0) { throw "Inno Setup failed for $Script" }
}

Push-Location $root
try {
    $isccPath = Resolve-Iscc
    Write-Host "Using $isccPath"
    New-Item -ItemType Directory -Force -Path $dist | Out-Null

    if ($Which -in @("Monitor", "Both")) {
        $exe = Join-Path $dist "gwatch.exe"
        Build-Exe "." $exe
        Build-Setup "gwatch.iss" $exe
    }
    if ($Which -in @("Agent", "Both")) {
        $exe = Join-Path $dist "gwatch-agent.exe"
        Build-Exe "./cmd/gwatch-agent" $exe
        Build-Setup "gwatch-agent.iss" $exe
    }

    Copy-Item (Join-Path $installer "Output\*.exe") $dist -Force
    Write-Host ""
    Write-Host "Done. Setup programs in $dist :"
    Get-ChildItem $dist -Filter "*setup*.exe" | ForEach-Object { Write-Host "  $($_.Name)  ($([math]::Round($_.Length/1MB,1)) MB)" }
} finally {
    Pop-Location
}
