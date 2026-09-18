# Removes the GWatch service. Monitoring data in %ProgramData%\GWatch is kept
# unless -RemoveData is passed. Run from an Administrator PowerShell.
param(
    [string]$InstallDir = "$env:ProgramFiles\GWatch",
    [string]$DataDir = "$env:ProgramData\GWatch",
    [switch]$RemoveData
)
$ErrorActionPreference = "Stop"
$target = Join-Path $InstallDir "gwatch.exe"
if (Test-Path $target) { & $target uninstall }
Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path ([Environment]::GetFolderPath("CommonPrograms")) "GWatch Monitor.lnk")
Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $InstallDir
if ($RemoveData) { Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $DataDir }
Write-Host "GWatch removed."
