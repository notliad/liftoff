#Requires -Version 5.1
<#
.SYNOPSIS
    Install lo CLI on Windows.

.DESCRIPTION
    Installs the lo CLI tool from a Go module or local source.

.EXAMPLE
    irm https://raw.githubusercontent.com/notliad/liftoff/main/install.ps1 | iex

.EXAMPLE
    .\install.ps1 -FromLocal

.EXAMPLE
    .\install.ps1 -FromModule github.com/notliad/liftoff/cmd/lo@latest

.EXAMPLE
    .\install.ps1 -Uninstall
#>

[CmdletBinding()]
param(
    [switch]$FromLocal,
    [string]$FromModule,
    [switch]$Uninstall,
    [string]$InstallDir = "$env:LOCALAPPDATA\Programs\lo"
)

$ErrorActionPreference = "Stop"

$BinName         = "lo.exe"
$BinPath         = Join-Path $InstallDir $BinName
$GoModuleDefault = "github.com/notliad/liftoff/cmd/lo@latest"

function Write-Info($msg) { Write-Host "[info] $msg" }
function Write-Warn($msg) { Write-Host "[warn] $msg" -ForegroundColor Yellow }
function Write-Done($msg) { Write-Host $msg -ForegroundColor Green }
function Write-Err($msg)  { Write-Host "[error] $msg" -ForegroundColor Red }

function Require-Go {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        Write-Err "Go 1.22+ is required. Download from: https://go.dev/dl/"
        exit 1
    }
}

function Add-ToUserPath {
    $current = [Environment]::GetEnvironmentVariable("PATH", "User")
    if ($current -notlike "*$InstallDir*") {
        [Environment]::SetEnvironmentVariable("PATH", "$InstallDir;$current", "User")
        Write-Info "Added $InstallDir to your user PATH."
        Write-Info "Restart your terminal for the change to take effect."
    }
}

function Install-FromLocal {
    if (-not (Test-Path ".\go.mod") -or -not (Test-Path ".\cmd\lo")) {
        return $false
    }
    Require-Go
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    & go build -o $BinPath .\cmd\lo
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    Write-Done "Installed local build to $BinPath"
    return $true
}

function Install-FromModule {
    param([string]$Module)
    Require-Go
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $env:GOBIN = $InstallDir
    & go install $Module
    if ($LASTEXITCODE -ne 0) { throw "go install $Module failed" }
    Write-Done "Installed $Module to $BinPath"
}

function Uninstall-Lo {
    if (Test-Path $BinPath) {
        Remove-Item $BinPath -Force
        Write-Host "Removed $BinPath"
    } else {
        Write-Info "$BinPath not found."
    }
    $configDir = Join-Path $env:APPDATA "lo"
    Write-Info "To remove config, delete: $configDir"
}

# --- Main ---

if ($Uninstall) {
    Uninstall-Lo
    exit 0
}

if ($FromLocal) {
    if (-not (Install-FromLocal)) {
        Write-Err "Could not build from local source (expected .\go.mod and .\cmd\lo)."
        exit 1
    }
} elseif ($FromModule) {
    Install-FromModule -Module $FromModule
} else {
    # Auto: try local build first, then fall back to module install
    if (-not (Install-FromLocal)) {
        Install-FromModule -Module $GoModuleDefault
    }
}

Add-ToUserPath

# Refresh PATH in the current session so we can check immediately
$env:PATH = "$InstallDir;$env:PATH"

if (Get-Command lo -ErrorAction SilentlyContinue) {
    $loPath = (Get-Command lo).Source
    Write-Done "`nlo is available: $loPath"
} else {
    Write-Done "`nInstall completed at $BinPath"
}

Write-Host "Run 'lo --help' to get started."
