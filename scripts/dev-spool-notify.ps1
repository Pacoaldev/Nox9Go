# Append one development event to the Telegram feed spool.
# Usage: .\scripts\dev-spool-notify.ps1 -Kind order_done -Detail "agent answer text" -TaskId <correlation id>
param(
    [ValidateSet('order_done', 'order_failed', 'task', 'info', 'commit', 'check', 'blocker')]
    [string]$Kind = 'order_done',
    [string]$Title = '',
    [string]$Detail = '',
    [string]$TaskId = ''
)

$root = Split-Path -Parent $PSScriptRoot
$envFile = Join-Path $root '.env'
if (Test-Path $envFile) {
    Get-Content $envFile | ForEach-Object {
        if ($_ -match '^\s*([^#=]+)=(.*)$') {
            $name = $Matches[1].Trim()
            $value = $Matches[2].Trim().Trim('"')
            if (-not (Get-Item "Env:$name" -ErrorAction SilentlyContinue)) {
                Set-Item -Path "Env:$name" -Value $value
            }
        }
    }
}

$spool = $env:DEV_SPOOL_PATH
if (-not $spool) {
    $data = $env:DATA_DIR
    if (-not $data) { $data = Join-Path $env:APPDATA 'nox9go' }
    $spool = Join-Path $data 'dev-events.jsonl'
}

if (-not $Title) {
    switch ($Kind) {
        'order_done' { $Title = 'Orden completada' }
        'order_failed' { $Title = 'Orden fallida' }
        default { $Title = 'info' }
    }
}

$spoolRecord = [ordered]@{
    id      = [guid]::NewGuid().ToString('n')
    at      = [DateTime]::UtcNow.ToString('o')
    kind    = $Kind
    title   = $Title
    detail  = $Detail
    task_id = $TaskId
}
if (-not $TaskId) {
    $spoolRecord.Remove('task_id')
}
$line = ($spoolRecord | ConvertTo-Json -Compress -Depth 4)
$dir = Split-Path -Parent $spool
if ($dir) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
Add-Content -Path $spool -Value $line -Encoding utf8
Write-Output "Appended to $spool"