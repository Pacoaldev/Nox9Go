@echo off
REM Double-click / shortcut: spawn the launcher fully hidden, then exit this console.
REM (A visible .cmd that waits on the .ps1 looks like a stuck Terminal.)
cd /d "%~dp0.."
start "" /b powershell -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "%~dp0start-nox9go.ps1"
exit /b 0
