# Parsar - parsar-daemon installer for Windows x64.
#
# This script is safe to pipe from PowerShell. It only writes below
# PARSAR_DAEMON_OUT_DIR (or the user's Downloads directory), does not require
# elevation, and never puts the pairing token in a URL or process argument.
#
# Environment variables:
#   PARSAR_DAEMON_VERSION       exact GitHub Release tag (optional)
#   PARSAR_DAEMON_REPO          GitHub owner/name override (optional)
#   PARSAR_DAEMON_OUT_DIR       destination directory (optional)
#   PARSAR_DAEMON_CONNECT_URL   Parsar server origin (pairing mode)
#   PARSAR_DAEMON_CONNECT_TOKEN one-time pairing token (pairing mode)
#   PARSAR_DAEMON_CONNECT_DEVICE_NAME device display name (pairing mode)

$ErrorActionPreference = 'Stop'

function Fail([string]$Message) {
    [Console]::Error.WriteLine("parsar-daemon: $Message")
    exit 1
}

$defaultRepo = '__PARSAR_DAEMON_REPO__'
if ($defaultRepo -like '__PARSAR_DAEMON_REPO__*') {
    $defaultRepo = 'MiniMax-AI-Dev/parsar'
}
$repo = if ([string]::IsNullOrWhiteSpace($env:PARSAR_DAEMON_REPO)) {
    $defaultRepo
} else {
    $env:PARSAR_DAEMON_REPO.Trim()
}
if ($repo -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') {
    Fail "invalid PARSAR_DAEMON_REPO '$repo'; expected owner/name"
}

if (-not [Environment]::Is64BitOperatingSystem) {
    Fail 'unsupported architecture: Windows x64 (amd64) is required'
}
$reportedArch = $env:PROCESSOR_ARCHITEW6432
if ([string]::IsNullOrWhiteSpace($reportedArch)) {
    $reportedArch = $env:PROCESSOR_ARCHITECTURE
}
if ($reportedArch -notin @('AMD64', 'amd64')) {
    Fail "unsupported architecture '$reportedArch'; Windows x64 (amd64) is required"
}
$os = 'windows'
$arch = 'amd64'

$outDir = if ([string]::IsNullOrWhiteSpace($env:PARSAR_DAEMON_OUT_DIR)) {
    Join-Path $env:USERPROFILE 'Downloads'
} else {
    $env:PARSAR_DAEMON_OUT_DIR
}
try {
    if (-not [IO.Path]::IsPathRooted($outDir)) {
        throw 'PARSAR_DAEMON_OUT_DIR must be an absolute path'
    }
    $outDir = [IO.Path]::GetFullPath($outDir)
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
} catch {
    Fail "cannot create output directory '$outDir': $($_.Exception.Message)"
}

$version = if ([string]::IsNullOrWhiteSpace($env:PARSAR_DAEMON_VERSION)) {
    ''
} else {
    $env:PARSAR_DAEMON_VERSION.Trim()
}
if ($version -and $version -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$') {
    Fail "invalid PARSAR_DAEMON_VERSION '$version'"
}

$serverOrigin = if ([string]::IsNullOrWhiteSpace($env:PARSAR_DAEMON_CONNECT_URL)) {
    ''
} else {
    $env:PARSAR_DAEMON_CONNECT_URL.Trim().TrimEnd('/')
}
$servedLocally = $false
$outFile = Join-Path $outDir 'parsar-daemon-windows-amd64.exe'
$temporaryFile = "$outFile.download"

if ($serverOrigin) {
    try {
        $serverUri = [Uri]$serverOrigin
        if (-not $serverUri.IsAbsoluteUri -or $serverUri.Scheme -notin @('http', 'https') -or $serverUri.Query) {
            throw 'PARSAR_DAEMON_CONNECT_URL must be an absolute http(s) origin without a query string'
        }
        $downloadUri = [UriBuilder]$serverUri
        $downloadUri.Path = ($serverUri.AbsolutePath.TrimEnd('/') + '/api/v1/parsar-daemon/download')
        $downloadUri.Query = 'os=windows&arch=amd64'
        Write-Host 'Fetching parsar-daemon for windows/amd64 from the Parsar server...'
        Invoke-WebRequest -Uri $downloadUri.Uri -OutFile $temporaryFile -UseBasicParsing
        Move-Item -Force $temporaryFile $outFile
        $servedLocally = $true
        Write-Host "Downloaded to $outFile"
    } catch {
        Remove-Item -Force -ErrorAction SilentlyContinue $temporaryFile
        Write-Warning 'Parsar server did not provide a Windows x64 binary; falling back to GitHub Releases.'
    }
}

if (-not $servedLocally) {
    if (-not $version) {
        try {
            $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers @{ 'User-Agent' = 'parsar-daemon-installer' }
            $version = [string]$release.tag_name
        } catch {
            Fail "could not resolve the latest release from github.com/${repo}: $($_.Exception.Message)"
        }
    }
    if (-not $version) {
        Fail 'the latest GitHub release did not contain a tag name'
    }
    $bareVersion = $version -replace '^parsar-daemon-', ''
    $binary = "parsar-daemon-$bareVersion-windows-amd64.exe"
    $downloadUri = "https://github.com/$repo/releases/download/$version/$binary"
    $outFile = Join-Path $outDir $binary
    $temporaryFile = "$outFile.download"
    try {
        Write-Host "Downloading $binary from GitHub Releases..."
        Invoke-WebRequest -Uri $downloadUri -OutFile $temporaryFile -UseBasicParsing
        Move-Item -Force $temporaryFile $outFile
        Write-Host "Downloaded to $outFile"
    } catch {
        Remove-Item -Force -ErrorAction SilentlyContinue $temporaryFile
        Fail "download failed for $binary from GitHub Releases: $($_.Exception.Message)"
    }
}

if ($env:PARSAR_DAEMON_CONNECT_TOKEN) {
    if ([string]::IsNullOrWhiteSpace($serverOrigin)) {
        Fail 'PARSAR_DAEMON_CONNECT_TOKEN is set but PARSAR_DAEMON_CONNECT_URL is empty'
    }
    Write-Host 'Pairing this device with the Parsar server...'
    try {
        # connect reads URL, token, and device name from its environment. Do
        # not add the token to this command's arguments or any download URL.
        & $outFile connect -b
        if ($LASTEXITCODE -ne 0) {
            Fail "parsar-daemon connect failed with exit code $LASTEXITCODE"
        }
    } catch {
        Fail "parsar-daemon connect failed: $($_.Exception.Message)"
    }
    exit 0
}

Write-Host 'Parsar does not install this file or modify PATH. Review it, then run:'
Write-Host "  & '$outFile' connect"
