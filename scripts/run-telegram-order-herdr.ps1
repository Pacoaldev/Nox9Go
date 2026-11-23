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

function Test-AgentBlocked {
    param([string]$Text)
    # Herdr reports "idle" while Pi waits on a tool-permission prompt, so the
    # state machine alone cannot tell a finished turn from a question. The
    # prompt itself is the only reliable signal: matching it is what stops a
    # blocked agent from being reported as a completed order.
    if (-not $Text) { return $false }
    return ($Text -match 'Allow once|Allow for this session|allow guarded|↑↓ navigate')
}

function Test-HerdrNoiseLine {
    param([string]$Line)
    $t = $Line.Trim()
    if (-not $t) { return $true }
    if ($t -match '^(main|master|develop)\s+[±+\-]') { return $true }
    if ($t -match '\bctx\s*\[|usage\s*·\s*alt\+u|\$0\.000') { return $true }
    if ($t -match '^type, or / for commands') { return $true }
    if ($t -match '^\d+\s+files?\s+[+\-]') { return $true }
    if ($t -match '^[│┃─┌┐└┘├┤╭╮╰╯■□●○\s]+$') { return $true }
    return $false
}

function Format-HerdrReplyForTelegram {
    param(
        [string]$Before,
        [string]$After,
        [int]$MaxLines = 25,
        [int]$MaxChars = 3500
    )
    $beforeLines = @($Before -split "`r?`n" | ForEach-Object { $_.TrimEnd() })
    $afterLines = @($After -split "`r?`n" | ForEach-Object { $_.TrimEnd() })
    $start = 0
    while ($start -lt $beforeLines.Count -and $start -lt $afterLines.Count -and
        $beforeLines[$start] -eq $afterLines[$start]) {
        $start++
    }
    $delta = @()
    if ($start -lt $afterLines.Count) {
        $delta = $afterLines[$start..($afterLines.Count - 1)]
    }
    $kept = foreach ($line in $delta) {
        if (-not (Test-HerdrNoiseLine $line)) { $line }
    }
    $kept = @($kept | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if ($kept.Count -gt $MaxLines) {
        $kept = $kept[($kept.Count - $MaxLines)..($kept.Count - 1)]
    }
    $text = ($kept -join "`n").Trim()
    if ($text.Length -gt $MaxChars) {
        $text = $text.Substring($text.Length - $MaxChars)
    }
    return $text
}

function Write-HerdrTelegramReply {
    param([string]$After)
    $formatted = Format-HerdrReplyForTelegram -Before $before -After $After
    if ($formatted) {
        Write-Output $formatted
    }
}

# Herdr often keeps Pi on "idle" even after a finished turn, so --until done can
# time out while the panel already answered. Snapshot terminal text before submit.
& herdr agent focus $target 2>&1 | Out-Null
$before = (Read-AgentTail $target).Trim()

$promptRaw = (& herdr agent prompt $target $detail 2>&1) | Out-String
if ($LASTEXITCODE -ne 0) {
    if ($promptRaw -match 'agent_blocked') {
        Write-HerdrTelegramReply (Read-AgentTail $target).Trim()
        exit 2
    }
    Write-Error $promptRaw.Trim()
}
# An approval dialog left over from an earlier turn swallows the prompt: the
# order never reached Pi. Reporting that as a blocker is the honest answer.
$afterPrompt = (Read-AgentTail $target).Trim()
if (Test-AgentBlocked $afterPrompt) {
    Write-HerdrTelegramReply $afterPrompt
    exit 2
}

# Bounded poll instead of one blocking `herdr agent wait`: the state machine
# often keeps Pi on "idle" after a finished turn, so a wait that only returns on
# "done" burns the full --timeout before this script can look at the panel. The
# loop below starts looking immediately and treats new, settled terminal output
# as a finished turn, which is what the operator sees anyway.
$pollMs = 2000
if ($env:HERDR_AGENT_POLL_MS) {
    $pollMs = [int]$env:HERDR_AGENT_POLL_MS
}
$deadline = [datetime]::UtcNow.AddMilliseconds($waitMs)
$lastSnap = ''
$stableTicks = 0
while ([datetime]::UtcNow -lt $deadline) {
    $status = Get-HerdrAgentStatus $target
    if ($status -eq 'blocked') {
        Write-HerdrTelegramReply (Read-AgentTail $target).Trim()
        exit 2
    }
    if ($status -eq 'done') {
        $tail = (Read-AgentTail $target).Trim()
        if ($tail -ne $before) { Write-HerdrTelegramReply $tail }
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
        # Two stable polls (~4s) of the same new output means the turn settled:
        # waiting for "done" is what cost the operator minutes. Settling on a
        # permission prompt is not a finished turn, so that case is a blocker.
        if ($stableTicks -ge 1) {
            if (Test-AgentBlocked $snap) {
                Write-HerdrTelegramReply $snap
                exit 2
            }
            Write-HerdrTelegramReply $snap
            exit 0
        }
    }
    Start-Sleep -Milliseconds $pollMs
}
# The last few polls above own the short deadline; a slow turn falls through to
# the long fallback below so a real multi-minute job is still reported.
$deadline = [datetime]::UtcNow.AddMilliseconds($timeoutMs)
$lastSnap = ''
$stableTicks = 0
while ([datetime]::UtcNow -lt $deadline) {
    $status = Get-HerdrAgentStatus $target
    if ($status -eq 'blocked') {
        Write-HerdrTelegramReply (Read-AgentTail $target).Trim()
        exit 2
    }
    if ($status -eq 'done') {
        $tail = (Read-AgentTail $target).Trim()
        if ($tail -ne $before) { Write-HerdrTelegramReply $tail }
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
        # Three stable polls (~6s) after new output => treat turn as finished,
        # unless what settled is a permission prompt waiting on the operator.
        if ($stableTicks -ge 2 -and $status -eq 'idle') {
            if (Test-AgentBlocked $snap) {
                Write-HerdrTelegramReply $snap
                exit 2
            }
            Write-HerdrTelegramReply $snap
            exit 0
        }
    }
    Start-Sleep -Seconds 2
}

$tail = (Read-AgentTail $target).Trim()
if (Test-AgentBlocked $tail) {
    Write-HerdrTelegramReply $tail
    exit 2
}
if ($tail -ne $before) {
    Write-HerdrTelegramReply $tail
    Write-Warning 'Herdr wait timed out but terminal output changed; treating as completed.'
    exit 0
}

Write-Error 'Herdr wait timed out with no new terminal output after prompt.'
exit 1