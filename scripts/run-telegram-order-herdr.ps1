# Run one Telegram order against a live Herdr agent (gentle-shell / Pi pane).
# Env: NOX9GO_ORDER_DETAIL, NOX9GO_TASK_ID, HERDR_AGENT_TARGET, HERDR_PROMPT_TIMEOUT_MS

$ErrorActionPreference = 'Stop'
$detail = $env:NOX9GO_ORDER_DETAIL
if (-not $detail) {
    Write-Error 'NOX9GO_ORDER_DETAIL is empty'
}
if ($env:HERDR_AGENT_TARGET) {
    $target = $env:HERDR_AGENT_TARGET.Trim()
} else {
    $target = 'nox'
}
$timeoutMs = 3600000
if ($env:HERDR_PROMPT_TIMEOUT_MS) {
    $timeoutMs = [int]$env:HERDR_PROMPT_TIMEOUT_MS
}
$waitMs = 120000
if ($env:HERDR_AGENT_WAIT_MS) {
    $waitMs = [int]$env:HERDR_AGENT_WAIT_MS
}

$repo = $env:DEV_REPO_ROOT
if ($repo) {
    $repo = $repo.Trim()
    if (Test-Path -LiteralPath $repo -PathType Container) {
        Set-Location -LiteralPath $repo
    } else {
        Write-Warning "DEV_REPO_ROOT ignored (not a directory): $repo"
    }
}

function Get-HerdrAgentStatus {
    param([string]$Name)
    try {
        $raw = (& herdr agent get $Name 2>&1) | Out-String
        $parsed = $raw | ConvertFrom-Json
        if ($parsed.result.agent.agent_status) {
            return [string]$parsed.result.agent.agent_status
        }
    } catch {
        Write-Warning "could not parse herdr agent get: $_"
    }
    return 'unknown'
}

function Read-AgentTail {
    param([string]$Name, [int]$Lines = 40)
    (& herdr agent read $Name --lines $Lines --source recent-unwrapped 2>&1) | Out-String
}

# Herdr often keeps Pi on "idle" even after a finished turn, so --until done can
# time out while the panel already answered. Snapshot terminal text before submit.
& herdr agent focus $target 2>&1 | Out-Null
$before = (Read-AgentTail $target).Trim()

$promptRaw = (& herdr agent prompt $target $detail 2>&1) | Out-String
if ($LASTEXITCODE -ne 0) {
    if ($promptRaw -match 'agent_blocked') {
        Write-Output (Read-AgentTail $target).Trim()
        exit 2
    }
    Write-Error $promptRaw.Trim()
}

& herdr agent wait $target --until done --until blocked --timeout $waitMs
$waitOk = ($LASTEXITCODE -eq 0)

$status = Get-HerdrAgentStatus $target
if ($status -eq 'blocked') {
    Write-Output (Read-AgentTail $target).Trim()
    exit 2
}
if ($waitOk -and ($status -in @('done', 'idle'))) {
    exit 0
}

# ponytail: output-delta fallback when Herdr state machine never marks "done".
$deadline = [datetime]::UtcNow.AddMilliseconds($timeoutMs)
$lastSnap = ''
$stableTicks = 0
while ([datetime]::UtcNow -lt $deadline) {
    $status = Get-HerdrAgentStatus $target
    if ($status -eq 'blocked') {
        Write-Output (Read-AgentTail $target).Trim()
        exit 2
    }
    if ($status -eq 'done') {
        exit 0
    }

    $snap = (Read-AgentTail $target).Trim()
    if ($snap -ne $before) {
        if ($snap -eq $lastSnap) {
            $stableTicks++
        } else {
            $stableTicks = 0
            $lastSnap = $snap
        }
        # Three stable polls (~6s) after new output => treat turn as finished.
        if ($stableTicks -ge 2 -and $status -eq 'idle') {
            Write-Output $snap
            exit 0
        }
    }
    Start-Sleep -Seconds 2
}

$tail = (Read-AgentTail $target).Trim()
if ($tail -ne $before) {
    Write-Output $tail
    Write-Warning 'Herdr wait timed out but terminal output changed; treating as completed.'
    exit 0
}

Write-Error 'Herdr wait timed out with no new terminal output after prompt.'
exit 1