# Open Herdr, then wait for you to pick a repo and run gentle-shell; registers HERDR_AGENT_TARGET for Telegram.
#
# Usage: powershell -NoProfile -ExecutionPolicy Bypass -File scripts/start-herdr-workspace.ps1
# Nimbo (silent launcher): use the Start-Process line in the header below — this script re-opens a visible console.
# Optional: -WithNox9Go also runs scripts/start-nox9go.ps1 (gateway + tray)
#
# Nimbo macro (copy as one line):
# Start-Process powershell.exe -ArgumentList '-NoExit','-NoProfile','-ExecutionPolicy','Bypass','-File','C:\Users\spano\Documents\PROYECTOS\NOX9GO\scripts\start-herdr-workspace.ps1','-InConsole'

param(
    [switch]$WithNox9Go,
    [switch]$InConsole
)

$ErrorActionPreference = 'Stop'
$Scripts = $PSScriptRoot

function Test-HasConsole {
    try {
        $null = [Console]::WindowWidth
        return $true
    } catch {
        return $false
    }
}

if (-not $InConsole -and -not (Test-HasConsole)) {
    $childArgs = @(
        '-NoExit', '-NoProfile', '-ExecutionPolicy', 'Bypass',
        '-File', $PSCommandPath, '-InConsole'
    )
    if ($WithNox9Go) { $childArgs += '-WithNox9Go' }
    Start-Process -FilePath 'powershell.exe' -ArgumentList $childArgs -WindowStyle Normal | Out-Null
    exit 0
}

function Resolve-HerdrApp {
    if ($env:HERDR_APP_EXE -and (Test-Path -LiteralPath $env:HERDR_APP_EXE)) {
        return $env:HERDR_APP_EXE
    }
    $default = Join-Path $env:LOCALAPPDATA 'Programs\herdr-app\herdr-app.exe'
    if (Test-Path -LiteralPath $default) {
        return $default
    }
    throw "Herdr app not found. Install herdr-app or set HERDR_APP_EXE."
}

if (-not $env:HERDR_AGENT_TARGET) { $env:HERDR_AGENT_TARGET = 'nox' }
if (-not $env:HERDR_PROJECTS_ROOT) {
    $env:HERDR_PROJECTS_ROOT = Join-Path $env:USERPROFILE 'Documents\PROYECTOS'
}

$app = Resolve-HerdrApp
Write-Host "Opening Herdr: $app"
Start-Process -FilePath $app | Out-Null

if ($WithNox9Go) {
    $noxLauncher = Join-Path $Scripts 'start-nox9go.ps1'
    if (Test-Path -LiteralPath $noxLauncher) {
        Write-Host 'Starting NOX9GO gateway + tray...'
        & $noxLauncher
    } else {
        Write-Warning "Missing $noxLauncher — start nox9go manually."
    }
}

Write-Host ''
Write-Host 'In Herdr (take your time):'
Write-Host '  1. Go to the repo you want under Documents\PROYECTOS'
Write-Host '  2. Run: gentle-shell'
Write-Host ''
Write-Host 'Waiting to register agent' $env:HERDR_AGENT_TARGET 'for Telegram...'

& (Join-Path $Scripts 'ensure-herdr-agent.ps1')
exit $LASTEXITCODE