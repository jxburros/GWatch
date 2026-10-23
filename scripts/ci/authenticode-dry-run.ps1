# CI dry run of the release's Authenticode signing (#120), with no secrets.
#
#   pwsh -File scripts/ci/authenticode-dry-run.ps1 -Iscc <ISCC.exe> -Monitor <gwatch.exe> -Agent <gwatch-agent.exe>
#
# Part 1 runs scripts/authenticode.ps1 -Action Configure the way the release
# does, with no variables, some and all of them, and checks each outcome: none
# means "unsigned, with a warning in the job summary", some means a failure, all
# means enabled. It touches nothing outside a temporary directory.
#
# Part 2 (Windows only; needs administrator rights, which hosted runners have)
# takes the signed path end to end with a throwaway self-signed certificate in
# place of Azure Artifact Signing: signs copies of both executables, compiles
# both setup programs with Inno Setup signing them and their uninstallers,
# verifies every result the way the release does, and checks Inno really called
# the signer for the uninstaller as well as the setup program. The certificate
# is trusted for the length of the run only and removed afterwards.
[CmdletBinding()]
param(
    [string]$Iscc,
    [string]$Monitor,
    [string]$Agent
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$signer = Join-Path $root "scripts/authenticode.ps1"
$work = Join-Path ([IO.Path]::GetTempPath()) ("authenticode-dry-run-" + [guid]::NewGuid().ToString("n"))
New-Item -ItemType Directory -Force -Path $work | Out-Null
$failures = @()
function Check([bool]$Ok, [string]$What) {
    if ($Ok) { Write-Host "ok   $What" } else { Write-Host "FAIL $What"; $script:failures += $What }
}

# ---- Part 1: the configuration paths ---------------------------------------
$names = "AUTHENTICODE_CLIENT_ID", "AUTHENTICODE_TENANT_ID", "AUTHENTICODE_ENDPOINT", "AUTHENTICODE_ACCOUNT", "AUTHENTICODE_PROFILE"
$full = @{
    AUTHENTICODE_CLIENT_ID = "00000000-0000-0000-0000-000000000001"
    AUTHENTICODE_TENANT_ID = "00000000-0000-0000-0000-000000000002"
    AUTHENTICODE_ENDPOINT  = "https://eus.codesigning.azure.net"
    AUTHENTICODE_ACCOUNT   = "dry-run-account"
    AUTHENTICODE_PROFILE   = "dry-run-profile"
}
# Runs Configure in a child process with exactly $Vars set, and returns its exit
# code with what it wrote to the output, env and summary files.
function Invoke-Configure([hashtable]$Vars) {
    $case = Join-Path $work ([guid]::NewGuid().ToString("n"))
    New-Item -ItemType Directory -Path $case | Out-Null
    $saved = @{}
    foreach ($n in $names + "GITHUB_OUTPUT", "GITHUB_ENV", "GITHUB_STEP_SUMMARY", "AUTHENTICODE_ENABLED") {
        $saved[$n] = [Environment]::GetEnvironmentVariable($n)
        [Environment]::SetEnvironmentVariable($n, $null)
    }
    try {
        foreach ($k in $Vars.Keys) { [Environment]::SetEnvironmentVariable($k, $Vars[$k]) }
        foreach ($f in "output", "env", "summary") { New-Item -ItemType File -Path (Join-Path $case $f) | Out-Null }
        $env:GITHUB_OUTPUT = Join-Path $case "output"
        $env:GITHUB_ENV = Join-Path $case "env"
        $env:GITHUB_STEP_SUMMARY = Join-Path $case "summary"
        $log = & pwsh -NoProfile -File $signer -Action Configure 2>&1 | Out-String
        $code = $LASTEXITCODE
        return @{
            Code    = $code
            Log     = $log
            Output  = Get-Content -Raw (Join-Path $case "output")
            Env     = Get-Content -Raw (Join-Path $case "env")
            Summary = Get-Content -Raw (Join-Path $case "summary")
        }
    } finally {
        foreach ($n in $saved.Keys) { [Environment]::SetEnvironmentVariable($n, $saved[$n]) }
    }
}

$r = Invoke-Configure @{}
Check ($r.Code -eq 0) "unconfigured: the release goes on"
Check ("$($r.Output)" -match '(?m)^enabled=false\s*$') "unconfigured: enabled=false"
Check ("$($r.Env)" -match '(?m)^AUTHENTICODE_ENABLED=false\s*$') "unconfigured: AUTHENTICODE_ENABLED=false for later steps"
Check ("$($r.Summary)" -match 'not Authenticode-signed') "unconfigured: the job summary says the build is unsigned"
Check ("$($r.Log)" -match '::warning') "unconfigured: a ::warning annotation"

$partial = @{} + $full; $partial.Remove("AUTHENTICODE_PROFILE"); $partial.Remove("AUTHENTICODE_TENANT_ID")
$r = Invoke-Configure $partial
Check ($r.Code -ne 0) "half configured: fails rather than shipping unsigned"
Check ("$($r.Log)" -match 'AZURE_SIGNING_PROFILE' -and "$($r.Log)" -match 'AZURE_SIGNING_TENANT_ID') "half configured: names the missing variables"

$bad = @{} + $full; $bad.AUTHENTICODE_ENDPOINT = "https://example.com"
$r = Invoke-Configure $bad
Check ($r.Code -ne 0) "an endpoint that is not *.codesigning.azure.net is refused"

$r = Invoke-Configure $full
Check ($r.Code -eq 0) "configured: succeeds"
Check ("$($r.Output)" -match '(?m)^enabled=true\s*$') "configured: enabled=true"
Check ("$($r.Summary)" -match 'dry-run-account') "configured: the summary names the signing account"

# IsccArgs: nothing when unsigned; a failure, not silence, when signing is on
# but no signer was installed.
foreach ($n in "GWATCH_SIGNING_DLIB", "GWATCH_SIGNING_METADATA", "GWATCH_SIGNING_CERT_THUMBPRINT", "AUTHENTICODE_ENABLED") {
    [Environment]::SetEnvironmentVariable($n, $null)
}
$out = @(& pwsh -NoProfile -File $signer -Action IsccArgs)
Check ($LASTEXITCODE -eq 0 -and $out.Count -eq 0) "unsigned: no extra ISCC arguments"
$env:AUTHENTICODE_ENABLED = "true"
& pwsh -NoProfile -File $signer -Action IsccArgs 2>&1 | Out-Null
Check ($LASTEXITCODE -ne 0) "enabled without a signer: IsccArgs fails"
$env:AUTHENTICODE_ENABLED = $null

# ---- Part 2: the signed path, with a throwaway certificate -------------------
if (-not $IsWindows) {
    Write-Host "skipping the signed path: it needs Windows, signtool and Inno Setup"
} elseif (-not ($Iscc -and $Monitor -and $Agent)) {
    Write-Host "skipping the signed path: pass -Iscc, -Monitor and -Agent to run it"
} else {
    $cert = New-SelfSignedCertificate -Type CodeSigningCert -Subject "CN=GWatch CI throwaway signer" `
        -CertStoreLocation Cert:\CurrentUser\My -KeyAlgorithm RSA -KeyLength 3072 -NotAfter (Get-Date).AddDays(1)
    $cer = Join-Path $work "throwaway.cer"
    Export-Certificate -Cert $cert -FilePath $cer | Out-Null
    try {
        # Trusted as a root and as a publisher so `signtool verify /pa` and
        # Get-AuthenticodeSignature accept it -- machine-wide, which is why this
        # only runs on a disposable runner and undoes it in the finally block.
        Import-Certificate -FilePath $cer -CertStoreLocation Cert:\LocalMachine\Root | Out-Null
        Import-Certificate -FilePath $cer -CertStoreLocation Cert:\LocalMachine\TrustedPublisher | Out-Null
        $env:GWATCH_SIGNING_CERT_THUMBPRINT = $cert.Thumbprint
        $env:AUTHENTICODE_ENABLED = "true"
        $env:GWATCH_SIGNING_LOG = Join-Path $work "signed.log"

        $exes = @()
        foreach ($src in $Monitor, $Agent) {
            $dst = Join-Path $work (Split-Path $src -Leaf)
            Copy-Item $src $dst
            $exes += $dst
        }
        $before = (Get-FileHash $exes[0] -Algorithm SHA256).Hash
        & pwsh -NoProfile -File $signer -Action Sign @exes
        Check ($LASTEXITCODE -eq 0) "signs the executables"
        Check ((Get-FileHash $exes[0] -Algorithm SHA256).Hash -ne $before) "signing changes the bytes (so checksums must come after it)"

        $signArgs = @(& pwsh -NoProfile -File $signer -Action IsccArgs)
        Check ($LASTEXITCODE -eq 0 -and $signArgs.Count -eq 2) "IsccArgs gives Inno Setup a sign tool"
        $setups = @()
        foreach ($pair in @(@{ Script = "gwatch.iss"; Exe = $exes[0] }, @{ Script = "gwatch-agent.iss"; Exe = $exes[1] })) {
            Set-Content -Path $env:GWATCH_SIGNING_LOG -Value $null
            $out = Join-Path $work "Output"
            & $Iscc @signArgs "/DAppVersion=0.0.0-signing-dry-run" "/DExePath=$($pair.Exe)" "/O$out" (Join-Path $root "scripts/installer/$($pair.Script)")
            Check ($LASTEXITCODE -eq 0) "$($pair.Script) compiles with signing on"
            # One call for the uninstaller Inno embeds and one for the setup
            # program. Inno may keep a signed uninstaller in the output directory
            # and reuse it, so only the first compile into it is sure to show both.
            $calls = @(Get-Content $env:GWATCH_SIGNING_LOG | Where-Object { $_ })
            $want = if ($setups.Count -eq 0) { 2 } else { 1 }
            Check ($calls.Count -ge $want) "$($pair.Script): Inno called the signer $($calls.Count) time(s), want at least $want ($($calls -join ', '))"
            $setups += Get-ChildItem $out -Filter "*-0.0.0-signing-dry-run.exe" | Where-Object { $_.Name -like ($pair.Script -replace '\.iss$', '-setup-*') } | ForEach-Object FullName
        }
        Check ($setups.Count -eq 2) "both setup programs were written"
        & pwsh -NoProfile -File $signer -Action Verify @exes @setups
        Check ($LASTEXITCODE -eq 0) "every executable and setup program verifies"
    } finally {
        foreach ($store in "Cert:\LocalMachine\Root", "Cert:\LocalMachine\TrustedPublisher", "Cert:\CurrentUser\My") {
            Get-ChildItem $store | Where-Object Thumbprint -eq $cert.Thumbprint | Remove-Item -Force -ErrorAction SilentlyContinue
        }
        foreach ($n in "GWATCH_SIGNING_CERT_THUMBPRINT", "AUTHENTICODE_ENABLED", "GWATCH_SIGNING_LOG") { [Environment]::SetEnvironmentVariable($n, $null) }
    }
}

Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
if ($failures.Count -gt 0) {
    foreach ($f in $failures) { Write-Host "::error title=Authenticode dry run::$f" }
    exit 1
}
Write-Host "Authenticode dry run passed."
