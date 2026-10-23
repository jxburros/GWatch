# Authenticode signing for the Windows executables and setup programs (#120).
#
# This is separate from, and comes before, the ed25519 update signatures: the
# release signs gwatch.exe / gwatch-agent.exe here first, packages those signed
# files into the setup programs (which Inno Setup signs too, uninstaller
# included), and only then computes the .sha256 sums and the ed25519 .sig
# files, so both describe the bytes users actually download.
#
#   pwsh -File scripts/authenticode.ps1 -Action Configure
#       Decides whether signing is configured, from the AUTHENTICODE_* variables
#       below. All of them empty: prints a warning (and adds it to the job
#       summary) and writes enabled=false to $GITHUB_OUTPUT -- the release goes
#       on unsigned, as before. All required ones set: enabled=true. Some set and
#       some not: fails, because a half-configured signer is a mistake, not a
#       choice to ship unsigned.
#
#   pwsh -File scripts/authenticode.ps1 -Action Install
#       Fetches the Azure Artifact Signing (formerly Trusted Signing) client --
#       the "dlib" that signtool.exe loads -- pinned by version and SHA-256,
#       writes its metadata.json and exports GWATCH_SIGNTOOL,
#       GWATCH_SIGNING_DLIB and GWATCH_SIGNING_METADATA to $GITHUB_ENV.
#       Authentication is the Azure CLI's login (azure/login with OIDC), so no
#       credential passes through this script.
#
#   pwsh -File scripts/authenticode.ps1 -Action Sign a.exe b.exe
#       Signs each file in place, SHA-256, RFC 3161 timestamped.
#
#   pwsh -File scripts/authenticode.ps1 -Action Verify a.exe b.exe
#       `signtool verify /pa /v` and Get-AuthenticodeSignature must both report
#       a valid signature, or this fails.
#
#   pwsh -File scripts/authenticode.ps1 -Action IsccArgs
#       Prints the ISCC.exe arguments that make Inno Setup sign the setup
#       program and its uninstaller through -Action Sign (one per line), or
#       nothing when no signer is set up -- which is a failure instead once
#       Configure has set AUTHENTICODE_ENABLED=true.
#
# Two signers are supported:
#   - Azure Artifact Signing: GWATCH_SIGNING_DLIB + GWATCH_SIGNING_METADATA
#     (what -Action Install sets up; the release uses this).
#   - A certificate in the current user's store: GWATCH_SIGNING_CERT_THUMBPRINT
#     (a code-signing certificate you hold yourself, or the throwaway one the CI
#     dry run creates). GWATCH_SIGNING_TIMESTAMP_URL adds a timestamp to these.
#
# GWATCH_SIGNING_LOG, when set, receives one line per file signed; the CI dry run
# uses it to prove Inno Setup really called the signer for the uninstaller.
[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidateSet("Configure", "Install", "Sign", "Verify", "IsccArgs")]
    [string]$Action,
    # The files, as the remaining arguments: `pwsh -File` passes "a,b" as one
    # string, so a list has to be separate words.
    [Parameter(ValueFromRemainingArguments)]
    [string[]]$Path = @()
)
$ErrorActionPreference = "Stop"

# The Artifact Signing client, pinned. To move it: pick a version at least two
# weeks old from https://www.nuget.org/packages/Microsoft.ArtifactSigning.Client,
# download https://www.nuget.org/api/v2/package/Microsoft.ArtifactSigning.Client/<version>
# and put its SHA-256 here.
$DlibPackage = "Microsoft.ArtifactSigning.Client"
$DlibVersion = "1.0.128"
$DlibSha256 = "74bd7d27e6ce1051409c38d9b46bc8df0400ecd643d51ffbf2ac00869061e40b"
$TimestampUrl = "http://timestamp.acs.microsoft.com"

function Add-Summary([string]$Markdown) {
    if ($env:GITHUB_STEP_SUMMARY) { Add-Content -Path $env:GITHUB_STEP_SUMMARY -Value $Markdown -Encoding utf8 }
}
function Set-Output([string]$Name, [string]$Value) {
    if ($env:GITHUB_OUTPUT) { Add-Content -Path $env:GITHUB_OUTPUT -Value "$Name=$Value" -Encoding utf8 }
    Write-Host "$Name=$Value"
}
function Set-JobEnv([string]$Name, [string]$Value) {
    if ($env:GITHUB_ENV) { Add-Content -Path $env:GITHUB_ENV -Value "$Name=$Value" -Encoding utf8 }
    Set-Item -Path "env:$Name" -Value $Value
}

function Invoke-Configure {
    # AUTHENTICODE_SUBSCRIPTION_ID is optional (azure/login can sign in to a
    # tenant without one), so it does not count towards "configured".
    $required = [ordered]@{
        AUTHENTICODE_CLIENT_ID = "vars.AZURE_SIGNING_CLIENT_ID"
        AUTHENTICODE_TENANT_ID = "vars.AZURE_SIGNING_TENANT_ID"
        AUTHENTICODE_ENDPOINT  = "vars.AZURE_SIGNING_ENDPOINT"
        AUTHENTICODE_ACCOUNT   = "vars.AZURE_SIGNING_ACCOUNT"
        AUTHENTICODE_PROFILE   = "vars.AZURE_SIGNING_PROFILE"
    }
    $set = @(); $missing = @()
    foreach ($name in $required.Keys) {
        if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($name))) { $missing += $required[$name] }
        else { $set += $required[$name] }
    }
    if ($set.Count -eq 0) {
        $msg = "Authenticode signing is not configured, so the Windows executables and setup programs in this build are NOT Authenticode-signed (SmartScreen will warn about an unknown publisher). Set the AZURE_SIGNING_* variables described in docs/RELEASING.md to sign them."
        Write-Host "::warning title=Unsigned Windows build::$msg"
        Add-Summary "### :warning: Windows binaries are not Authenticode-signed`n`n$msg`n"
        Set-Output "enabled" "false"
        Set-JobEnv "AUTHENTICODE_ENABLED" "false"
        return
    }
    if ($missing.Count -gt 0) {
        $msg = "Authenticode signing is half configured: $($set -join ', ') set, but $($missing -join ', ') empty. Set all of them, or none to release unsigned."
        Write-Host "::error title=Authenticode configuration::$msg"
        Add-Summary "### :x: Authenticode configuration`n`n$msg`n"
        exit 1
    }
    $endpoint = $env:AUTHENTICODE_ENDPOINT.Trim()
    if ($endpoint -notmatch '^https://[a-z0-9]+\.codesigning\.azure\.net/?$') {
        Write-Host "::error title=Authenticode configuration::AZURE_SIGNING_ENDPOINT must be the account's regional endpoint, like https://eus.codesigning.azure.net -- got '$endpoint'."
        exit 1
    }
    Add-Summary "### :lock: Windows binaries are Authenticode-signed`n`nAzure Artifact Signing account ``$($env:AUTHENTICODE_ACCOUNT)``, certificate profile ``$($env:AUTHENTICODE_PROFILE)``.`n"
    Set-Output "enabled" "true"
    Set-JobEnv "AUTHENTICODE_ENABLED" "true"
}

function Find-SignTool {
    if ($env:GWATCH_SIGNTOOL -and (Test-Path $env:GWATCH_SIGNTOOL)) { return $env:GWATCH_SIGNTOOL }
    $kits = Join-Path ${env:ProgramFiles(x86)} "Windows Kits\10\bin"
    if (Test-Path $kits) {
        # The newest SDK first: Artifact Signing needs signtool 10.0.22621.755 or later.
        $found = Get-ChildItem $kits -Directory |
            Where-Object { $_.Name -match '^\d+\.\d+\.\d+\.\d+$' } |
            Sort-Object { [version]$_.Name } -Descending |
            ForEach-Object { Join-Path $_.FullName "x64\signtool.exe" } |
            Where-Object { Test-Path $_ } |
            Select-Object -First 1
        if ($found) { return $found }
    }
    $onPath = Get-Command signtool.exe -ErrorAction SilentlyContinue
    if ($onPath) { return $onPath.Source }
    throw "signtool.exe not found: install the Windows SDK's signing tools, or set GWATCH_SIGNTOOL."
}

function Invoke-Install {
    foreach ($name in "AUTHENTICODE_ENDPOINT", "AUTHENTICODE_ACCOUNT", "AUTHENTICODE_PROFILE") {
        if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($name))) { throw "$name is empty; run -Action Configure first" }
    }
    $signtool = Find-SignTool
    $sdk = [version](Split-Path (Split-Path (Split-Path $signtool -Parent) -Parent) -Leaf)
    if ($sdk -lt [version]"10.0.22621.0") { throw "$signtool is from SDK $sdk; Artifact Signing needs 10.0.22621.755 or later." }

    # The dlib is .NET 8 code; without the runtime signtool fails with a bare
    # "could not load" that does not say why.
    $runtimes = @()
    if (Get-Command dotnet -ErrorAction SilentlyContinue) { $runtimes = @(& dotnet --list-runtimes 2>$null) }
    if (-not ($runtimes -match '^Microsoft\.NETCore\.App 8\.')) {
        throw "The Artifact Signing client needs the .NET 8 runtime (x64), which is not installed here. Add actions/setup-dotnet with dotnet-version 8.0.x before this step."
    }

    $temp = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [IO.Path]::GetTempPath() }
    $root = Join-Path $temp "artifact-signing"
    New-Item -ItemType Directory -Force -Path $root | Out-Null
    # A .nupkg is a zip; Expand-Archive only opens one called .zip.
    $nupkg = Join-Path $root "$DlibPackage.$DlibVersion.zip"
    Invoke-WebRequest -Uri "https://www.nuget.org/api/v2/package/$DlibPackage/$DlibVersion" -OutFile $nupkg -UseBasicParsing
    $hash = (Get-FileHash $nupkg -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($hash -ne $DlibSha256) { throw "$DlibPackage $DlibVersion has SHA-256 $hash, expected $DlibSha256 -- refusing to load it into signtool." }
    $dir = Join-Path $root "client"
    if (Test-Path $dir) { Remove-Item -Recurse -Force $dir }
    Expand-Archive -Path $nupkg -DestinationPath $dir
    $dlib = Join-Path $dir "bin\x64\Azure.CodeSigning.Dlib.dll"
    if (-not (Test-Path $dlib)) { throw "$DlibPackage $DlibVersion has no bin\x64\Azure.CodeSigning.Dlib.dll" }

    # Sign in through the Azure CLI only -- that is the identity azure/login
    # established from the workflow's OIDC token. Excluding the rest stops the
    # client probing a managed identity or a cached account on the runner.
    $metadata = [ordered]@{
        Endpoint               = $env:AUTHENTICODE_ENDPOINT.Trim()
        CodeSigningAccountName = $env:AUTHENTICODE_ACCOUNT.Trim()
        CertificateProfileName = $env:AUTHENTICODE_PROFILE.Trim()
        CorrelationId          = "$env:GITHUB_REPOSITORY/$env:GITHUB_RUN_ID/$env:GITHUB_JOB"
        ExcludeCredentials     = @(
            "EnvironmentCredential", "ManagedIdentityCredential", "WorkloadIdentityCredential",
            "SharedTokenCacheCredential", "VisualStudioCredential", "VisualStudioCodeCredential",
            "AzurePowerShellCredential", "AzureDeveloperCliCredential", "InteractiveBrowserCredential")
    }
    $metadataPath = Join-Path $root "metadata.json"
    $metadata | ConvertTo-Json | Set-Content -Path $metadataPath -Encoding utf8

    Set-JobEnv "GWATCH_SIGNTOOL" $signtool
    Set-JobEnv "GWATCH_SIGNING_DLIB" $dlib
    Set-JobEnv "GWATCH_SIGNING_METADATA" $metadataPath
    Write-Host "signtool: $signtool"
    Write-Host "Artifact Signing client ${DlibVersion}: $dlib"
}

# The signtool arguments for whichever signer is set up, or $null.
function Get-SignArgs {
    if ($env:GWATCH_SIGNING_DLIB -and $env:GWATCH_SIGNING_METADATA) {
        return @("sign", "/v", "/fd", "SHA256", "/tr", $TimestampUrl, "/td", "SHA256",
            "/dlib", $env:GWATCH_SIGNING_DLIB, "/dmdf", $env:GWATCH_SIGNING_METADATA)
    }
    if ($env:GWATCH_SIGNING_CERT_THUMBPRINT) {
        $a = @("sign", "/v", "/fd", "SHA256", "/s", "My", "/sha1", $env:GWATCH_SIGNING_CERT_THUMBPRINT)
        if ($env:GWATCH_SIGNING_TIMESTAMP_URL) { $a += @("/tr", $env:GWATCH_SIGNING_TIMESTAMP_URL, "/td", "SHA256") }
        return $a
    }
    return $null
}

function Invoke-Sign {
    if ($Path.Count -eq 0) { throw "-Action Sign needs the files to sign" }
    $signArgs = Get-SignArgs
    if (-not $signArgs) { throw "No signer is set up: run -Action Install, or set GWATCH_SIGNING_CERT_THUMBPRINT." }
    $signtool = Find-SignTool
    foreach ($file in $Path) {
        if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { throw "no such file: $file" }
        # The timestamp server and the signing service are both network calls;
        # retry a transient failure rather than fail the whole release on it.
        $ok = $false
        for ($attempt = 1; $attempt -le 3 -and -not $ok; $attempt++) {
            & $signtool @signArgs $file
            if ($LASTEXITCODE -eq 0) { $ok = $true }
            elseif ($attempt -lt 3) { Write-Host "signtool failed (exit $LASTEXITCODE), retrying in $(5 * $attempt)s..."; Start-Sleep -Seconds (5 * $attempt) }
        }
        if (-not $ok) { throw "signtool could not sign $file" }
        if ($env:GWATCH_SIGNING_LOG) { Add-Content -Path $env:GWATCH_SIGNING_LOG -Value (Split-Path $file -Leaf) }
    }
}

function Invoke-Verify {
    if ($Path.Count -eq 0) { throw "-Action Verify needs the files to check" }
    $signtool = Find-SignTool
    $failed = $false
    foreach ($file in $Path) {
        if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { Write-Host "::error::no such file: $file"; $failed = $true; continue }
        & $signtool verify /pa /v $file
        if ($LASTEXITCODE -ne 0) { Write-Host "::error file=$file::signtool verify /pa rejects $file"; $failed = $true; continue }
        $sig = Get-AuthenticodeSignature -LiteralPath $file
        if ($sig.Status -ne "Valid") {
            Write-Host "::error file=$file::Get-AuthenticodeSignature says $($sig.Status) for ${file}: $($sig.StatusMessage)"
            $failed = $true; continue
        }
        $line = "$(Split-Path $file -Leaf): $($sig.SignerCertificate.Subject)"
        if ($sig.TimeStamperCertificate) { $line += " (timestamped by $($sig.TimeStamperCertificate.Subject))" }
        Write-Host "ok $line"
        Add-Summary "- ``$(Split-Path $file -Leaf)`` signed by $($sig.SignerCertificate.Subject)"
    }
    if ($failed) { exit 1 }
}

function Invoke-IsccArgs {
    if (-not (Get-SignArgs)) {
        if ($env:AUTHENTICODE_ENABLED -eq "true") { throw "Authenticode signing is enabled but no signer is set up for Inno Setup (did -Action Install run?)" }
        return
    }
    # Inno Setup runs this for the uninstaller it embeds (SignedUninstaller=yes)
    # and for the finished setup program. In the command, $q is a quote and $f
    # the quoted file name; the .iss files pick the tool up from /DCodeSign.
    $script = $PSCommandPath
    Write-Output "/DCodeSign=gwatchsign"
    Write-Output ('/Sgwatchsign=pwsh.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $q' + $script + '$q -Action Sign $f')
}

switch ($Action) {
    "Configure" { Invoke-Configure }
    "Install" { Invoke-Install }
    "Sign" { Invoke-Sign }
    "Verify" { Invoke-Verify }
    "IsccArgs" { Invoke-IsccArgs }
}
