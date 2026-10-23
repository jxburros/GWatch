# Builds the GWatch Windows setup programs end to end: compiles the Go
# executables, then wraps each one with Inno Setup.
#
#   powershell -ExecutionPolicy Bypass -File scripts\build-installer.ps1
#
# With no -Version, the number in the repository's VERSION file is used.
#
# Needs Go 1.24+ and Inno Setup 6. Install Inno Setup with either of:
#   winget install JRSoftware.InnoSetup
#   choco install innosetup --no-progress -y
#
# -Which selects what to build: Monitor, Agent or Both (the default). The
# finished setup programs are copied into dist\ next to the executables.
#
# Authenticode signing is optional and off unless a signer is set up: put a
# code-signing certificate's thumbprint (from Cert:\CurrentUser\My) in
# GWATCH_SIGNING_CERT_THUMBPRINT and each executable is signed before it is
# packaged, then the setup program and its uninstaller as Inno Setup builds them.
# That needs PowerShell 7 (pwsh) and the Windows SDK's signtool.exe; see
# scripts\authenticode.ps1 and docs/RELEASING.md.
[CmdletBinding()]
param(
    [string]$Version,
    [string]$AgentVersion,
    [ValidateSet("Monitor", "Agent", "Both")]
    [string]$Which = "Both",
    [string]$Iscc
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
if (-not $Version) { $Version = (Get-Content (Join-Path $root "VERSION") -Raw).Trim() }
if (-not $AgentVersion) { $AgentVersion = (Get-Content (Join-Path $root "cmd/gwatch-agent/VERSION") -Raw).Trim() }
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

function Build-Exe([string]$Package, [string]$Output, [string]$BuildVersion) {
    Write-Host "Building $Output (version $BuildVersion)..."
    $env:CGO_ENABLED = "0"; $env:GOOS = "windows"; $env:GOARCH = "amd64"
    & go build -trimpath -ldflags "-s -w -X main.version=$BuildVersion" -o $Output $Package
    if ($LASTEXITCODE -ne 0) { throw "go build failed for $Package" }
}

$signer = Join-Path $PSScriptRoot "authenticode.ps1"
$signing = [bool]($env:GWATCH_SIGNING_CERT_THUMBPRINT -or ($env:GWATCH_SIGNING_DLIB -and $env:GWATCH_SIGNING_METADATA))

function Build-Setup([string]$Script, [string]$Exe, [string]$BuildVersion) {
    $signArgs = @()
    if ($signing) {
        # The executable first, so the setup program packages the signed file.
        Write-Host "Signing $Exe..."
        & pwsh -NoProfile -File $signer -Action Sign $Exe
        if ($LASTEXITCODE -ne 0) { throw "Authenticode signing failed for $Exe" }
        $signArgs = @(& pwsh -NoProfile -File $signer -Action IsccArgs)
        if ($LASTEXITCODE -ne 0) { throw "could not set up Inno Setup's sign tool" }
    }
    Write-Host "Compiling $Script..."
    & $isccPath @signArgs "/DAppVersion=$BuildVersion" "/DExePath=$Exe" (Join-Path $installer $Script)
    if ($LASTEXITCODE -ne 0) { throw "Inno Setup failed for $Script" }
}

Push-Location $root
try {
    $isccPath = Resolve-Iscc
    Write-Host "Using $isccPath"
    New-Item -ItemType Directory -Force -Path $dist | Out-Null

    if ($Which -in @("Monitor", "Both")) {
        $exe = Join-Path $dist "gwatch.exe"
        Build-Exe "." $exe $Version
        Build-Setup "gwatch.iss" $exe $Version
    }
    if ($Which -in @("Agent", "Both")) {
        $exe = Join-Path $dist "gwatch-agent.exe"
        Build-Exe "./cmd/gwatch-agent" $exe $AgentVersion
        Build-Setup "gwatch-agent.iss" $exe $AgentVersion
    }

    Copy-Item (Join-Path $installer "Output\*.exe") $dist -Force
    Write-Host ""
    Write-Host "Done. Setup programs in $dist :"
    Get-ChildItem $dist -Filter "*setup*.exe" | ForEach-Object { Write-Host "  $($_.Name)  ($([math]::Round($_.Length/1MB,1)) MB)" }
} finally {
    Pop-Location
}
