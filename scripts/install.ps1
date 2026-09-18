# Installs (or updates) GWatch as a Windows service that starts with Windows.
# Run from an elevated (Administrator) PowerShell:
#   powershell -ExecutionPolicy Bypass -File scripts\install.ps1 -Exe .\dist\gwatch.exe
# Add -Listen 0.0.0.0:8080 to serve the interface to the whole network from the start
# (it can also be switched on later in Settings > Network access).
# Re-running the script with a newer gwatch.exe performs a safe upgrade:
# it stops the service, replaces the executable and starts the service again.
param(
    [string]$Exe = "$PSScriptRoot\..\dist\gwatch.exe",
    [string]$InstallDir = "$env:ProgramFiles\GWatch",
    [string]$DataDir = "$env:ProgramData\GWatch",
    [string]$Listen = "127.0.0.1:8080"
)
$ErrorActionPreference = "Stop"

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Please run this script from an Administrator PowerShell window."
}
if (-not (Test-Path $Exe)) { throw "gwatch.exe not found at $Exe (build it with scripts\build.ps1 first)." }

$target = Join-Path $InstallDir "gwatch.exe"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null

$service = Get-Service -Name "GWatch" -ErrorAction SilentlyContinue
if ($service) {
    Write-Host "Existing GWatch service found: stopping it for the upgrade..."
    if ($service.Status -ne "Stopped") { Stop-Service -Name "GWatch" -Force }
    $service.WaitForStatus("Stopped", [TimeSpan]::FromSeconds(60))
    Copy-Item -Force $Exe $target
    Write-Host "Executable updated. Starting service..."
    Start-Service -Name "GWatch"
} else {
    Copy-Item -Force $Exe $target
    Write-Host "Registering the GWatch service (data in $DataDir)..."
    & $target install --data-dir $DataDir --listen $Listen
    if ($LASTEXITCODE -ne 0) { throw "Service installation failed." }
}

# Start-menu shortcut that opens the local web interface.
try {
    $shell = New-Object -ComObject WScript.Shell
    $programs = [Environment]::GetFolderPath("CommonPrograms")
    $lnk = $shell.CreateShortcut((Join-Path $programs "GWatch Monitor.lnk"))
    $lnk.TargetPath = $target
    $lnk.Arguments = "open --listen $Listen"
    $lnk.Description = "Open the GWatch monitoring interface"
    $lnk.Save()
} catch { Write-Warning "Could not create the Start menu shortcut: $_" }

(Get-Service -Name "GWatch").WaitForStatus("Running", [TimeSpan]::FromSeconds(30))
Write-Host "GWatch is running. Open http://$Listen in your browser."
