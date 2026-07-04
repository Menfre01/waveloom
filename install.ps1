# Waveloom Windows installer
# Usage: powershell -ExecutionPolicy Bypass -File install.ps1
#
# Prerequisites: Git for Windows (https://git-scm.com/downloads/win)
# Waveloom requires Git Bash to execute shell commands.

param(
    [string]$InstallDir = "$env:USERPROFILE\.local\bin"
)

$Repo = "Menfre01/waveloom"
$Binary = "waveloom"

# Check for Git for Windows
$gitBash = $null
if (Test-Path "C:\Program Files\Git\bin\bash.exe") {
    $gitBash = "C:\Program Files\Git\bin\bash.exe"
} elseif (Get-Command git -ErrorAction SilentlyContinue) {
    $gitDir = Split-Path -Parent (Get-Command git).Source
    $candidate = Join-Path $gitDir "..\..\bin\bash.exe"
    if (Test-Path $candidate) { $gitBash = (Resolve-Path $candidate).Path }
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

$Url = "https://github.com/$Repo/releases/latest/download/${Binary}_windows_$Arch.zip"

Write-Host "-> Downloading Waveloom (windows/$Arch)..."
Write-Host "   $Url"

$TmpDir = Join-Path $env:TEMP "waveloom-install"
New-Item -ItemType Directory -Force -Path $TmpDir | Out-Null

try {
    $ZipFile = Join-Path $TmpDir "waveloom.zip"
    Invoke-WebRequest -Uri $Url -OutFile $ZipFile -ErrorAction Stop
    Expand-Archive -Path $ZipFile -DestinationPath $TmpDir -Force

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $Src = Join-Path $TmpDir "$Binary.exe"
    $Dst = Join-Path $InstallDir "$Binary.exe"
    Move-Item -Force $Src $Dst

    Write-Host ""
    Write-Host "v Waveloom installed to $Dst"
} finally {
    Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
}

# Check PATH
if ($env:PATH -notlike "*$InstallDir*") {
    Write-Host ""
    Write-Host "!  $InstallDir is not in your PATH."
    Write-Host "   Run the following in an elevated PowerShell:"
    Write-Host ""
    Write-Host '   [Environment]::SetEnvironmentVariable("PATH", $env:PATH + ";' + $InstallDir + '", "User")'
}

Write-Host ""
Write-Host "Next steps:"
Write-Host "  waveloom setup    # Configure your DeepSeek API Key"
Write-Host "  waveloom          # Launch the TUI"
