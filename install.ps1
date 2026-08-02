# Waveloom Windows installer
# Usage: powershell -ExecutionPolicy Bypass -File install.ps1
#
# Prerequisites: Git for Windows (https://git-scm.com/downloads/win)
# Waveloom requires Git Bash to execute shell commands.

param(
    [string]$InstallDir = "$env:USERPROFILE\.local\bin"
)

# Fail fast on any error instead of silently continuing with a broken install
$ErrorActionPreference = "Stop"

$Repo = "Menfre01/waveloom"
$Binary = "waveloom"

# PowerShell 5.1 on older Windows defaults to TLS 1.0/1.1, which GitHub rejects.
# Safe on PowerShell 7+ (property still exists, already defaults to TLS 1.2).
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

# Check for Git for Windows
$gitBash = $null
if (Test-Path "C:\Program Files\Git\bin\bash.exe") {
    $gitBash = "C:\Program Files\Git\bin\bash.exe"
} else {
    $gitCmd = Get-Command git -ErrorAction SilentlyContinue
    if ($gitCmd -and $gitCmd.Source) {
        $gitDir = Split-Path -Parent $gitCmd.Source
        $candidate = Join-Path $gitDir "..\..\bin\bash.exe"
        if (Test-Path $candidate) { $gitBash = (Resolve-Path $candidate).Path }
    }
}

if (-not $gitBash) {
    Write-Host "!  Git for Windows is required but not detected."
    Write-Host "   Download and install from: https://git-scm.com/downloads/win"
    Write-Host "   After installation, re-run this script."
    Write-Host ""
    Write-Host "   If already installed in a non-standard location, set WAVELOOM_GIT_BASH_PATH"
    Write-Host "   to your bash.exe path and launch waveloom directly."
    exit 1
}
Write-Host "v  Git Bash detected: $gitBash"

# Detect architecture
$Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    "ARM64" { "arm64" }
    "AMD64" { "amd64" }
    default { "amd64" }
}

$ZipName = "${Binary}_windows_$Arch.zip"
$Url = "https://github.com/$Repo/releases/latest/download/$ZipName"

Write-Host "-> Downloading Waveloom (windows/$Arch)..."
Write-Host "   $Url"

$TmpDir = Join-Path $env:TEMP "waveloom-install"
New-Item -ItemType Directory -Force -Path $TmpDir | Out-Null

try {
    $ZipFile = Join-Path $TmpDir "waveloom.zip"
    # -UseBasicParsing: required on PowerShell 5.1 without IE engine (Server Core), no-op on 7+
    Invoke-WebRequest -Uri $Url -OutFile $ZipFile -UseBasicParsing

    # Verify SHA256 against the release checksums (skip if the entry is missing)
    $ChecksumsUrl = "https://github.com/$Repo/releases/latest/download/checksums.txt"
    $checksums = (Invoke-WebRequest -Uri $ChecksumsUrl -UseBasicParsing).Content
    $line = $checksums -split '\r?\n' | Where-Object { $_ -match [regex]::Escape($ZipName) } | Select-Object -First 1
    if ($line) {
        $expected = (($line -split '\s+')[0]).ToLower()
        $actual = (Get-FileHash -Path $ZipFile -Algorithm SHA256).Hash.ToLower()
        if ($actual -ne $expected) {
            throw "SHA256 verification failed for $ZipName (expected $expected, got $actual). Aborting."
        }
        Write-Host "v  SHA256 verified."
    } else {
        Write-Host "!  checksums.txt has no entry for $ZipName, skipping verification."
    }

    Expand-Archive -Path $ZipFile -DestinationPath $TmpDir -Force

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $Src = Join-Path $TmpDir "$Binary.exe"
    $Dst = Join-Path $InstallDir "$Binary.exe"
    Move-Item -Force $Src $Dst

    Write-Host ""
    Write-Host "v Waveloom installed to $Dst"
} catch {
    Write-Host ""
    Write-Host "!  Installation failed: $($_.Exception.Message)"
    exit 1
} finally {
    Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
}

# Configure PATH
if ($env:PATH -notlike "*$InstallDir*") {
    Write-Host ""
    Write-Host "-> Adding $InstallDir to your user PATH..."

    try {
        $currentUserPath = [Environment]::GetEnvironmentVariable("PATH", "User")
        if ($null -eq $currentUserPath) { $currentUserPath = "" }
        if ($currentUserPath -notlike "*$InstallDir*") {
            $newPath = if ($currentUserPath) { "$currentUserPath;$InstallDir" } else { $InstallDir }
            [Environment]::SetEnvironmentVariable("PATH", $newPath, "User")
            # Also update current session so it works immediately
            $env:PATH = "$env:PATH;$InstallDir"
            Write-Host "v  PATH updated successfully."
        } else {
            Write-Host "v  $InstallDir is already in your user PATH."
        }
    } catch {
        Write-Host "!  Unable to set PATH automatically (may need elevation)."
        Write-Host "   Run the following in an elevated PowerShell:"
        Write-Host ""
        Write-Host '   [Environment]::SetEnvironmentVariable("PATH", $env:PATH + ";' + $InstallDir + '", "User")'
    }
}

# Ensure Git Bash also picks up the PATH (Git Bash needs /c/... style paths)
$bashRc = "$env:USERPROFILE\.bashrc"
$bashInstallDir = $InstallDir.Replace('\', '/') -replace '^([A-Za-z]):', '/$1'
$exportLine = "export PATH=`"$bashInstallDir`":`$PATH`""
# UTF-8 without BOM: default Set-Content/Add-Content encodings on PowerShell 5.1
# (ANSI) mangle non-ASCII usernames, and a UTF-8 BOM breaks Git Bash sourcing.
$utf8NoBom = New-Object -TypeName System.Text.UTF8Encoding -ArgumentList $false
$bashRcUpdated = $false
if (Test-Path $bashRc) {
    $content = [System.IO.File]::ReadAllText($bashRc)
    # Second check keeps compatibility with lines written by older installers
    if ($content -notlike "*$bashInstallDir*" -and $content -notlike '*$HOME/.local/bin*') {
        [System.IO.File]::WriteAllText($bashRc, $content + "`n" + $exportLine + "`n", $utf8NoBom)
        $bashRcUpdated = $true
    }
} else {
    [System.IO.File]::WriteAllText($bashRc, $exportLine + "`n", $utf8NoBom)
    $bashRcUpdated = $true
}
if ($bashRcUpdated) {
    Write-Host "v  Added PATH entry to ~/.bashrc for Git Bash compatibility."
}

Write-Host ""
Write-Host "Next steps (in Git Bash, not cmd/PowerShell):"
Write-Host "  waveloom setup    # Configure your DeepSeek API Key"
Write-Host "  waveloom          # Launch the TUI"
Write-Host ""
Write-Host "Waveloom requires Git Bash to run — cmd and PowerShell are not supported."
