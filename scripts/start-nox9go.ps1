# Launch NOX9GO: gateway (if needed) + tray icon + dashboard in the browser.
# No console flash: gateway CLI is CUI; invoke it with CreateNoWindow.
$ErrorActionPreference = 'Stop'
$Root = Split-Path $PSScriptRoot -Parent
$Gateway = Join-Path $Root 'nox9go.exe'
$Tray = Join-Path $Root 'nox9go-tray.exe'
$Dashboard = 'http://127.0.0.1:20130/dashboard'

if (-not (Test-Path $Gateway)) { throw "Missing $Gateway — build it first (go build -o nox9go.exe ./cmd/nox9go/)" }
if (-not (Test-Path $Tray)) {
	throw "Missing $Tray — build with: go build -ldflags=`"-H=windowsgui`" -o nox9go-tray.exe ./cmd/nox9go-tray/"
}

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

$status = (Invoke-GatewayHidden -ArgumentList @('status')).Output
if ($status -notmatch 'is running') {
	[void](Invoke-GatewayHidden -ArgumentList @('start'))
	$deadline = (Get-Date).AddSeconds(20)
	do {
		Start-Sleep -Milliseconds 300
		try {
			$r = Invoke-WebRequest -Uri 'http://127.0.0.1:20130/' -UseBasicParsing -TimeoutSec 2
			if ($r.StatusCode -ge 200) { break }
		} catch { }
	} while ((Get-Date) -lt $deadline)
}

# Replace any existing tray (old console build must not stay alive).
Get-Process -Name 'nox9go-tray' -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 400
Start-Process -FilePath $Tray -WorkingDirectory $Root -WindowStyle Hidden

Start-Process $Dashboard
