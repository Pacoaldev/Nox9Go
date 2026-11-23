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
    $target = 'focused'
}

function Test-HerdrFocusedTarget {
    param([string]$Spec)
    $s = $Spec.Trim().ToLowerInvariant()
    return $s -in @('focused', '@focused', '*')
}

function Resolve-HerdrAgentTarget {
    param([string]$Configured)
    $spec = $Configured.Trim()
    if (-not $spec) { $spec = 'focused' }
    if (-not (Test-HerdrFocusedTarget $spec)) {
        return $spec
    }
    $r = Invoke-HerdrArgv @('agent', 'list')
    if ($r.ExitCode -ne 0 -and -not $r.Text) {
        Write-Error "herdr agent list failed"
    }
    try {
        $payload = $r.Text | ConvertFrom-Json
    } catch {
        Write-Error "herdr agent list: invalid JSON: $($r.Text)"
    }
    $focused = @($payload.result.agents | Where-Object { $_.focused -eq $true -and $_.name })
    if ($focused.Count -eq 0) {
        Write-Error 'herdr: no focused agent (open gentle-shell and focus that Pi pane)'
    }
    if ($focused.Count -eq 1) {
        return [string]$focused[0].name
    }
    $shell = @($focused | Where-Object { $_.name -ieq 'shell' })
    if ($shell.Count -eq 1) {
        return [string]$shell[0].name
    }
    $nonNox = @($focused | Where-Object { $_.name -ine 'nox' })
    if ($nonNox.Count -eq 1) {
        return [string]$nonNox[0].name
    }
    $names = ($focused | ForEach-Object { $_.name }) -join ', '
    Write-Error "herdr: multiple focused agents ($names); focus one Pi pane"
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

function Invoke-HerdrArgv {
    param([Parameter(Mandatory)][string[]]$Argv)
    $prevEa = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $out = @(& herdr @Argv 2>&1)
        $text = ($out | ForEach-Object {
            if ($_ -is [System.Management.Automation.ErrorRecord]) { $_.ToString() } else { "$_" }
        }) -join "`n"
        return @{
            ExitCode = $LASTEXITCODE
            Text     = $text.Trim()
        }
    } finally {
        $ErrorActionPreference = $prevEa
    }
}

function Get-HerdrAgentStatus {
    param([string]$Name)
    try {
        $raw = (Invoke-HerdrArgv @('agent', 'get', $Name)).Text
        $parsed = $raw | ConvertFrom-Json
        if ($parsed.result.agent.agent_status) {
            return [string]$parsed.result.agent.agent_status
        }
    } catch {
        Write-Warning "could not parse herdr agent get: $_"
    }
    return 'unknown'
}

function Invoke-HerdrAgentRead {
    param(
        [string]$Name,
        [int]$Lines,
        [string]$Source
    )
    $r = Invoke-HerdrArgv @('agent', 'read', $Name, '--lines', "$Lines", '--source', $Source)
    $text = $r.Text
    if ($text -match '"code"\s*:\s*"agent_not_idle"') { return $null }
    if ($text -match '^\s*\{\s*"error"') { return $null }
    if ($r.ExitCode -ne 0 -and -not $text) { return $null }
    return $text
}

function Read-AgentTail {
    param([string]$Name, [int]$Lines = 40)
    # recent-unwrapped needs an idle pane; while Pi is working, Herdr only allows visible.
    $status = Get-HerdrAgentStatus $Name
    $canUnwrap = $status -in @('idle', 'done', 'blocked')
    if ($canUnwrap) {
        $text = Invoke-HerdrAgentRead $Name $Lines 'recent-unwrapped'
        if ($text) { return $text }
    }
    $text = Invoke-HerdrAgentRead $Name $Lines 'visible'
    if ($text) { return $text }
    return ''
}

function Get-HerdrAgentPaneId {
    param([string]$Name)
    try {
        $parsed = (Invoke-HerdrArgv @('agent', 'get', $Name)).Text | ConvertFrom-Json
        return [string]$parsed.result.agent.pane_id
    } catch {
        return $null
    }
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

function Test-HerdrAwaitingAnswer {
    param([string]$Text)
    if (-not $Text) { return $false }
    return ($Text -match 'Type your answer|Answer in your own words|Escribí tu respuesta|escribí tu respuesta')
}

function Test-HerdrStaleMenu {
    param([string]$Text)
    if (-not $Text) { return $false }
    return ($Text -match '↑↓ navigate')
}

function Clear-HerdrStaleMenu {
    param([string]$Name)
    $tail = (Read-AgentTail $Name 25).Trim()
    if (-not (Test-HerdrStaleMenu $tail)) { return $false }
    Invoke-HerdrArgv @('agent', 'send-keys', $Name, 'esc') | Out-Null
    Start-Sleep -Milliseconds 400
    return $true
}

function Submit-HerdrRelayAnswer {
    param(
        [string]$Name,
        [string]$Answer
    )
    if (-not $Answer) { return $false }
    $pane = Get-HerdrAgentPaneId $Name
    if (-not $pane) { return $false }
    Invoke-HerdrArgv @('pane', 'send-text', $pane, $Answer) | Out-Null
    Invoke-HerdrArgv @('agent', 'send-keys', $Name, 'Enter') | Out-Null
    return $true
}

function Submit-HerdrAgentPrompt {
    param(
        [string]$Name,
        [string]$Detail
    )
    if (-not $Detail) {
        return @{ ExitCode = 1; Text = 'empty prompt' }
    }
    # herdr agent prompt parses the tail as CLI flags ("con" from confirm breaks).
    # pane send-text carries the full line; Enter submits it like the operator typing.
    $pane = Get-HerdrAgentPaneId $Name
    if (-not $pane) {
        return Invoke-HerdrArgv @('agent', 'prompt', $Name, $Detail)
    }
    $text = Invoke-HerdrArgv @('pane', 'send-text', $pane, $Detail)
    if ($text.ExitCode -ne 0) {
        return $text
    }
    return Invoke-HerdrArgv @('agent', 'send-keys', $Name, 'Enter')
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
    if ($t -match '\bTPS\b|\btok/s\b|\bout\s+\d+.*\bin\s+\d+') { return $true }
    if ($t -match '^Pi\s+—') { return $true }
    if ($t -match 'waiting for input') { return $true }
    if ($t -match '\[ Panel \]|^>\s+type, or /') { return $true }
    if ($t -match '/gentle:|^Output exactly') { return $true }
    if ($t -match '^\s*✎\s*\d+\s+files?\s*·|^·\s*\d+\s+files?\s*·') { return $true }
    if ($t -match '^—\s*[^\s─]') { return $true }
    if ($t -match '^·\s*\d+\s+files?\s*·') { return $true }
    if ($t -match '^User says:') { return $true }
    if ($t -match "^(I'll|I will|So I should|Caveman)") { return $true }
    if ($t -match '^next:\s*herdr') { return $true }
    if ($t -match '^`herdr\s') { return $true }
    if ($t -match '^\s*→\s+') { return $true }
    if ($t -match '^Enter select|Esc cancel') { return $true }
    if ($t -match '^(⠋|⠙|⠹|⠸|⠼|⠴|⠦|⠧|⠇|⠏|Working)\s*$') { return $true }
    if ($t -match 'Siguiente paso|quedó tipeado|sin Enter|Nada más en vuelo|pane w\d+:|^>\s+type') { return $true }
    return $false
}

function Test-HerdrRunnerMetaLine {
    param([string]$Line)
    return (Test-HerdrNoiseLine $Line)
}

function Get-HerdrEchoFromLine {
    param(
        [string]$Line,
        [string]$Expected
    )
    $e = $Expected.Trim()
    if (-not $e) { return '' }
    $lt = $Line.Trim()
    if ($lt -eq $e) { return $e }
    $pat = [regex]::Escape($e)
    if ($lt -match "^(?i)($pat)([\.!?])?(?:\s|$)") {
        return $e
    }
    return ''
}

function Compress-HerdrKeptLines {
    param(
        [string[]]$Lines,
        [int]$TailLines = 3
    )
    $lines = @($Lines | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if ($lines.Count -le $TailLines) {
        return ($lines -join "`n").Trim()
    }
    $tail = $lines[($lines.Count - $TailLines)..($lines.Count - 1)]
    return ($tail -join "`n").Trim()
}

function Select-HerdrReplyForOrder {
    param(
        [string]$Text,
        [string[]]$KeptLines,
        [string]$OrderDetail
    )
    if ($OrderDetail -match '(?i)^responde\s+solo:\s*(.+)$') {
        $expected = $Matches[1].Trim()
        foreach ($line in $KeptLines) {
            if ($line.Trim() -eq $expected) {
                return $expected
            }
        }
    }

    $trimDetail = $OrderDetail.Trim()
    if ($trimDetail -and $trimDetail -notmatch '[\r\n]' -and $trimDetail.Length -le 120) {
        foreach ($line in $KeptLines) {
            $echo = Get-HerdrEchoFromLine -Line $line -Expected $trimDetail
            if ($echo) { return $echo }
        }
    }

    $lines = @($Text -split "`r?`n" | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne '' })
    $body = @($lines | Where-Object { -not (Test-HerdrRunnerMetaLine $_) })
    if ($body.Count -gt 0) {
        $Text = ($body -join "`n").Trim()
    }
  # ponytail: one long mixed-status line becomes its first sentence when the order was short.
    if ($trimDetail.Length -le 120 -and $body.Count -eq 1 -and $body[0].Length -gt 80) {
        $m = [regex]::Match($body[0], '^(.{1,80}?)([\.!?])\s')
        if ($m.Success) {
            return ($m.Groups[1].Value + $m.Groups[2].Value).Trim()
        }
    }
    return $Text
}

function Format-HerdrReplyForTelegram {
    param(
        [string]$Before,
        [string]$After,
        [string]$OrderDetail = '',
        [int]$MaxLines = 12,
        [int]$MaxChars = 1200
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
    $text = Compress-HerdrKeptLines @($kept)
    $text = Select-HerdrReplyForOrder -Text $text -KeptLines @($kept) -OrderDetail $OrderDetail
    if ($text.Length -gt $MaxChars) {
        $text = $text.Substring($text.Length - $MaxChars)
    }
    return $text
}

function Write-HerdrBlockerSummary {
    param([string]$Tail)
    $short = Format-HerdrReplyForTelegram -Before '' -After $Tail -MaxLines 12 -MaxChars 1200
    if (-not $short) {
        $short = 'Herdr necesita acción en el panel (aprobación, menú o respuesta).'
    }
    if (Test-HerdrAwaitingAnswer $Tail) {
        $short = "Herdr espera tu respuesta.`n$short"
    }
    Write-Output $short
}

function Write-HerdrTelegramReply {
    param([string]$After)
    $formatted = Format-HerdrReplyForTelegram -Before $before -After $After -OrderDetail $detail
    if ($formatted) {
        Write-Output $formatted
    }
}

# Herdr often keeps Pi on "idle" even after a finished turn, so --until done can
# time out while the panel already answered. Snapshot terminal text before submit.
$target = Resolve-HerdrAgentTarget $target
Invoke-HerdrArgv @('agent', 'focus', $target) | Out-Null
Clear-HerdrStaleMenu $target | Out-Null
$before = (Read-AgentTail $target).Trim()

$tailNow = $before
$statusNow = Get-HerdrAgentStatus $target
$relayEnabled = $env:NOX9GO_HERDR_RELAY -ne '0'
if ($relayEnabled -and (Test-HerdrAwaitingAnswer $tailNow)) {
    if (Submit-HerdrRelayAnswer $target $detail) {
        Start-Sleep -Milliseconds 500
        $before = (Read-AgentTail $target).Trim()
    } else {
        Write-HerdrBlockerSummary $tailNow
        exit 2
    }
} elseif ($statusNow -eq 'blocked') {
    Write-HerdrBlockerSummary $tailNow
    exit 2
} else {
    $prompt = Submit-HerdrAgentPrompt $target $detail
    if ($prompt.ExitCode -ne 0) {
        if ($prompt.Text -match 'agent_blocked') {
            Clear-HerdrStaleMenu $target | Out-Null
            $blockedTail = (Read-AgentTail $target).Trim()
            if ($relayEnabled -and (Test-HerdrAwaitingAnswer $blockedTail)) {
                if (Submit-HerdrRelayAnswer $target $detail) {
                    Start-Sleep -Milliseconds 500
                    $before = (Read-AgentTail $target).Trim()
                } else {
                    Write-HerdrBlockerSummary $blockedTail
                    exit 2
                }
            } else {
                Write-HerdrBlockerSummary $blockedTail
                exit 2
            }
        } else {
            Write-Error $prompt.Text
        }
    }
}
# An approval dialog left over from an earlier turn swallows the prompt: the
# order never reached Pi. Reporting that as a blocker is the honest answer.
$afterPrompt = (Read-AgentTail $target).Trim()
if (Test-AgentBlocked $afterPrompt) {
    Write-HerdrBlockerSummary $afterPrompt
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
        Write-HerdrBlockerSummary (Read-AgentTail $target).Trim()
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
                Write-HerdrBlockerSummary $snap
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
        Write-HerdrBlockerSummary (Read-AgentTail $target).Trim()
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
                Write-HerdrBlockerSummary $snap
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
    Write-HerdrBlockerSummary $tail
    exit 2
}
if ($tail -ne $before) {
    Write-HerdrTelegramReply $tail
    Write-Warning 'Herdr wait timed out but terminal output changed; treating as completed.'
    exit 0
}

Write-Error 'Herdr wait timed out with no new terminal output after prompt.'
exit 1