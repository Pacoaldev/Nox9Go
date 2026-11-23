# Ensure Herdr server is up and agent HERDR_AGENT_TARGET is bound to a Pi/gentle-shell pane.
# Typical flow: launch Herdr app -> open repo -> run `gentle-shell` in that pane -> run this script.
# Or: -LaunchHerdr opens HERDR_APP_EXE (herdr-app.exe) and waits until Pi is detected.
#
# Env: HERDR_APP_EXE, HERDR_AGENT_TARGET (default nox), HERDR_AGENT_KIND (default pi),
#      HERDR_PANE_ID (optional), HERDR_PROJECTS_ROOT (default ~/Documents/PROYECTOS),
#      HERDR_REPO_ROOT (optional exact cwd preference — not DEV_REPO_ROOT; that is for nox9go only),
#      HERDR_WAIT_SEC (default 180)

param(
    [switch]$LaunchHerdr,
    [switch]$Quiet
)

$ErrorActionPreference = 'Stop'

function Write-Info([string]$Message) {
    if (-not $Quiet) { Write-Host $Message }
}

$agentName = if ($env:HERDR_AGENT_TARGET) { $env:HERDR_AGENT_TARGET.Trim() } else { 'nox' }
$agentKind = if ($env:HERDR_AGENT_KIND) { $env:HERDR_AGENT_KIND.Trim() } else { 'pi' }
$paneFilter = if ($env:HERDR_PANE_ID) { $env:HERDR_PANE_ID.Trim() } else { '' }
$projectsRoot = $env:HERDR_PROJECTS_ROOT
if (-not $projectsRoot) {
    $projectsRoot = Join-Path $env:USERPROFILE 'Documents\PROYECTOS'
}
$projectsRoot = $projectsRoot.Trim().Replace('/', '\').TrimEnd('\')
$preferRepo = $env:HERDR_REPO_ROOT
if ($preferRepo) { $preferRepo = $preferRepo.Trim().Replace('/', '\').TrimEnd('\') }

$waitSec = 180
if ($env:HERDR_WAIT_SEC) {
    $waitSec = [int]$env:HERDR_WAIT_SEC
}

function Resolve-HerdrAppPath {
    if ($env:HERDR_APP_EXE -and (Test-Path -LiteralPath $env:HERDR_APP_EXE)) {
        return $env:HERDR_APP_EXE
    }
    $defaultApp = Join-Path $env:LOCALAPPDATA 'Programs\herdr-app\herdr-app.exe'
    if (Test-Path -LiteralPath $defaultApp) {
        return $defaultApp
    }
    if (Get-Command herdr -ErrorAction SilentlyContinue) {
        return (Get-Command herdr).Source
    }
    throw 'Herdr not found. Set HERDR_APP_EXE or install herdr CLI on PATH.'
}

function Test-HerdrServerRunning {
    try {
        $out = (& herdr status server 2>&1) | Out-String
        return ($out -match '(?m)^status:\s*running\s*$')
    } catch {
        return $false
    }
}

function Invoke-HerdrJson {
    param([Parameter(Mandatory)][string[]]$ArgumentList)

    $raw = (& herdr @ArgumentList 2>&1) | Out-String
    if ($LASTEXITCODE -ne 0) {
        throw $raw.Trim()
    }
    return ($raw | ConvertFrom-Json)
}

function Get-RegisteredAgent {
    param([string]$Name)
    $list = Invoke-HerdrJson -ArgumentList @('agent', 'list')
    foreach ($a in $list.result.agents) {
        if ([string]$a.name -eq $Name) {
            return $a
        }
    }
    return $null
}

function Get-HerdrPathLiteral([string]$Path) {
    if (-not $Path) { return '' }
    return $Path.Trim().Replace('/', '\').TrimEnd('\')
}

function Test-PathUnderRoot {
    param([string]$Child, [string]$Root)
    $childN = Get-HerdrPathLiteral $Child
    $rootN = Get-HerdrPathLiteral $Root
    if (-not $childN -or -not $rootN) { return $false }
    return $childN.StartsWith($rootN, [System.StringComparison]::OrdinalIgnoreCase)
}

function Select-PiPane {
    param($Panes)

    if ($paneFilter) {
        $hit = @($Panes | Where-Object { $_.pane_id -eq $paneFilter })
        if ($hit.Count -eq 1) { return $hit[0] }
        return $null
    }

    $candidates = @($Panes | Where-Object {
            $_.label -ne 'Sidebar' -and -not $_.tokens.'herdr-sidebar-explorer'
        })

    if (Test-Path -LiteralPath $projectsRoot -PathType Container) {
        $under = @($candidates | Where-Object { Test-PathUnderRoot -Child $_.cwd -Root $projectsRoot })
        if ($under.Count -ge 1) { $candidates = $under }
    }

    if ($preferRepo) {
        $exact = @($candidates | Where-Object { (Get-HerdrPathLiteral $_.cwd) -eq $preferRepo })
        if ($exact.Count -ge 1) {
            $pick = @($exact | Where-Object { $_.focused -eq $true } | Select-Object -First 1)
            if ($pick.Count -ge 1) { return $pick[0] }
            return $exact[0]
        }
    }

    $withPi = @($candidates | Where-Object { $_.agent -eq $agentKind })
    if ($withPi.Count -ge 1) {
        $focused = @($withPi | Where-Object { $_.focused -eq $true })
        if ($focused.Count -ge 1) { return $focused[0] }
        return $withPi[0]
    }

    $byTitle = @($candidates | Where-Object {
            $t = [string]$_.terminal_title_stripped
            $t -match 'π|pi|gentle'
        })
    if ($byTitle.Count -ge 1) {
        $focused = @($byTitle | Where-Object { $_.focused -eq $true })
        if ($focused.Count -ge 1) { return $focused[0] }
        return $byTitle[0]
    }

    if ($candidates.Count -eq 1) { return $candidates[0] }
    return $null
}

if (-not (Get-Command herdr -ErrorAction SilentlyContinue)) {
    throw 'herdr is not on PATH'
}

$existing = $null
if (Test-HerdrServerRunning) {
    try { $existing = Get-RegisteredAgent -Name $agentName } catch { }
}

if (-not (Test-HerdrServerRunning)) {
    if (-not $LaunchHerdr) {
        throw 'Herdr server is not running. Open Herdr or re-run with -LaunchHerdr.'
    }
    Write-Info 'Starting Herdr (GUI)...'
    $app = Resolve-HerdrAppPath
    Start-Process -FilePath $app -WindowStyle Normal | Out-Null
}

$deadline = (Get-Date).AddSeconds($waitSec)
$hintShown = $false
while ((Get-Date) -lt $deadline) {
    if (-not (Test-HerdrServerRunning)) {
        Start-Sleep -Seconds 2
        continue
    }

    try {
        $existing = Get-RegisteredAgent -Name $agentName
        if ($existing) {
            if ($paneFilter -and $existing.pane_id -ne $paneFilter) {
                throw "Agent $agentName is on pane $($existing.pane_id), not HERDR_PANE_ID=$paneFilter"
            }
            Write-Info "Agent $agentName on pane $($existing.pane_id) cwd=$($existing.cwd) (status=$($existing.agent_status))."
            exit 0
        }

        $paneList = Invoke-HerdrJson -ArgumentList @('pane', 'list')
        $pane = Select-PiPane -Panes $paneList.result.panes
        if ($pane) {
            Write-Info "Registering agent $agentName on pane $($pane.pane_id)..."
            $startRaw = (& herdr agent start $agentName --kind $agentKind --pane $pane.pane_id 2>&1) | Out-String
            if ($LASTEXITCODE -eq 0) {
                Write-Info "Agent $agentName ready."
                exit 0
            }
            if ($startRaw -match 'agent_name_taken') {
                Write-Info "Agent $agentName was registered concurrently."
                exit 0
            }
            throw $startRaw.Trim()
        }
    } catch {
        $msg = $_.Exception.Message
        if ($msg -notmatch 'connect|socket|server') {
            throw
        }
    }

    if (-not $hintShown) {
        Write-Info 'Waiting for Pi: open your repo in Herdr and run gentle-shell in that pane.'
        $hintShown = $true
    } else {
        Write-Info 'Still waiting for a Pi/gentle-shell pane...'
    }
    Start-Sleep -Seconds 2
}

throw "Timed out after ${waitSec}s. Open gentle-shell in Herdr (pane with Pi) and run this script again."