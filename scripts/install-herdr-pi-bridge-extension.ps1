# Install the Pi companion from spancerxing/herdr-telegram-bridge so Herdr
# reports blocked reliably (herdr:blocked emitter). MIT — see repo LICENSE.
# Requires: herdr on PATH, Pi agent extensions dir (~/.pi/agent/extensions).

$ErrorActionPreference = 'Stop'
$repo = 'spancerxing/herdr-telegram-bridge'
$branch = 'main'
$relPath = 'pi-extension/herdr-blocked-emitter.ts'
$rawUrl = "https://raw.githubusercontent.com/$repo/$branch/$relPath"

$piRoot = Join-Path $env:USERPROFILE '.pi\agent\extensions'
if (-not (Test-Path -LiteralPath $piRoot)) {
    New-Item -ItemType Directory -Force -Path $piRoot | Out-Null
}
$dest = Join-Path $piRoot 'herdr-blocked-emitter.ts'

Write-Host "Downloading $relPath ..."
Invoke-WebRequest -Uri $rawUrl -UseBasicParsing -OutFile $dest
Write-Host "Wrote $dest"

Write-Host 'Running: herdr integration install pi'
& herdr integration install pi
if ($LASTEXITCODE -ne 0) {
    Write-Warning 'herdr integration install pi failed; install Herdr integration manually.'
}
Write-Host 'Reload Pi sessions (/reload in pane) so the extension loads.'
Write-Host 'Reference: https://github.com/spancerxing/herdr-telegram-bridge#enabling-blocked-on-pi'