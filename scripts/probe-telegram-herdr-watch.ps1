# Quick probe: can NOX9GO's Herdr watcher inputs work on this machine?
$ErrorActionPreference = 'Continue'

Write-Host '=== agent list (focused) ==='
herdr agent list 2>&1

Write-Host "`n=== agent get shell (status) ==="
$get = (herdr agent get shell 2>&1) | Out-String
Write-Host $get

Write-Host '=== agent read shell detection (120 lines) — dialog markers ==='
$read = (herdr agent read shell --lines 120 --source detection 2>&1) | Out-String
$markers = @('navigate', 'ask_user_choice', 'Type your answer', '->', [char]0x2192)
$hit = $false
foreach ($m in $markers) {
    if ($read -match [regex]::Escape($m)) { Write-Host "  FOUND: $m"; $hit = $true }
}
if (-not $hit) { Write-Host '  (no dialog markers — open the Pi menu NOW and re-run)' }
if ($read.Length -lt 4000) { Write-Host $read } else { Write-Host ($read.Substring($read.Length - 4000)) }

Write-Host "`n=== Gentle Shell desktop feed (passive watcher source) ==="
$feedDirs = @(
    (Join-Path $env:USERPROFILE '.gentle-shell\agent\desktop\sessions'),
    (Join-Path $env:USERPROFILE '.pi\agent\desktop\sessions')
)
foreach ($d in $feedDirs) {
    if (-not (Test-Path $d)) { Write-Host "  missing $d"; continue }
    $latest = Get-ChildItem $d -Filter '*.json' -ErrorAction SilentlyContinue | Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (-not $latest) { Write-Host "  (no JSON in $d)"; continue }
    $j = Get-Content $latest.FullName -Raw | ConvertFrom-Json
    $pq = $j.pendingQuestion
    Write-Host "  $($latest.Name) state=$($j.activity.state) blocked=$($j.activity.blockedLabel)"
    if ($pq) { Write-Host "    pending: $($pq.question)" }
}

Write-Host "`n=== HERDR / order runner env (from repo .env) ==="
$envPath = Join-Path (Split-Path $PSScriptRoot -Parent) '.env'
if (Test-Path $envPath) {
    Get-Content $envPath | Where-Object { $_ -match '^(DEV_ORDER|HERDR_|NOX9GO_HERDR)' }
} else {
    Write-Host "  missing $envPath"
}