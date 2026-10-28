# Launch NOX9GO: gateway (if needed) + tray icon + dashboard in the browser.
# No console flash: gateway CLI is CUI; invoke it with CreateNoWindow.
$ErrorActionPreference = 'Stop'
$Root = Split-Path $PSScriptRoot -Parent
$Gateway = Join-Path $Root 'nox9go.exe'
$Tray = Join-Path $Root 'nox9go-tray.exe'

if (-not (Test-Path $Gateway)) { throw "Missing $Gateway - build it first (go build -o nox9go.exe ./cmd/nox9go/)" }
if (-not (Test-Path $Tray)) {
    throw "Missing $Tray - build with: go build -ldflags=`"-H=windowsgui`" -o nox9go-tray.exe ./cmd/nox9go-tray/"
}

# Run the CLI without a console window and capture its output.
function Invoke-GatewayHidden {
    param([Parameter(Mandatory)][string[]]$ArgumentList)
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $Gateway
    $psi.Arguments = ($ArgumentList -join ' ')
    $psi.WorkingDirectory = $Root
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $p = [System.Diagnostics.Process]::Start($psi)
    $stdout = $p.StandardOutput.ReadToEnd()
    $stderr = $p.StandardError.ReadToEnd()
    $p.WaitForExit()
    [pscustomobject]@{
        ExitCode = $p.ExitCode
        Output   = ($stdout + $stderr)
    }
}

# Decide on the structured status, not on human prose, and take the address from
# the CLI so PORT, .env or a profile flag are honoured. This used to poll a
# hard-coded 20130 and match the text "is running".
function Get-GatewayStatus {
    $raw = (Invoke-GatewayHidden -ArgumentList @('status', '--json')).Output
    try {
        return $raw | ConvertFrom-Json
    } catch {
        throw "Could not read the gateway status as JSON. Output was: $raw"
    }
}

$status = Get-GatewayStatus
if (-not $status.running) {
    [void](Invoke-GatewayHidden -ArgumentList @('start'))
    $deadline = (Get-Date).AddSeconds(20)
    do {
        Start-Sleep -Milliseconds 300
        try {
            $r = Invoke-WebRequest -Uri $status.dashboard -UseBasicParsing -TimeoutSec 2
            if ($r.StatusCode -ge 200) { break }
        } catch { }
    } while ((Get-Date) -lt $deadline)
    $status = Get-GatewayStatus
}

# Replace any existing tray (old console build must not stay alive).
Get-Process -Name 'nox9go-tray' -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 400
Start-Process -FilePath $Tray -WorkingDirectory $Root -WindowStyle Hidden

# Open the dashboard on the address the gateway actually answers on.
Start-Process $status.dashboard
