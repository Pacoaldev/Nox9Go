# Runs one Telegram agent order on this machine (DEV_ORDER_RUNNER).
# Env: NOX9GO_ORDER_DETAIL, NOX9GO_TASK_ID, DEV_REPO_ROOT

$ErrorActionPreference = 'Stop'
$detail = $env:NOX9GO_ORDER_DETAIL
if (-not $detail) {
    Write-Error 'NOX9GO_ORDER_DETAIL is empty'
}
$repo = $env:DEV_REPO_ROOT
if (-not $repo) {
    $repo = (Split-Path -Parent $PSScriptRoot)
}
Set-Location $repo

if ($env:DEV_AGENT_CMD) {
    Invoke-Expression $env:DEV_AGENT_CMD
    exit $LASTEXITCODE
}

if (Get-Command codex -ErrorAction SilentlyContinue) {
    codex exec $detail
    exit $LASTEXITCODE
}

if (Get-Command agent -ErrorAction SilentlyContinue) {
    agent -p $detail
    exit $LASTEXITCODE
}

Write-Error @"
No agent CLI found. Set DEV_AGENT_CMD in .env, or install codex/agent on PATH.
Order was: $detail
"@
exit 1