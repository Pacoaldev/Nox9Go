### Tray Start/Stop on Windows

- Fix tray **Start Gateway**: it called `nox9go start --background`, but
  `--background` is a root flag, so the subcommand rejected it and the click
  failed silently (no console on the tray process).
- Fix tray **Stop Gateway** on Windows/macOS: with no socket-owner proof (L2),
  DecideStop refused the item. Stop now falls back to shelling out to the
  sibling `nox9go stop`, matching how Start already shells out to `nox9go
  start`, so the tray never puts the CLI token on the wire itself.
- Verified with `go test ./internal/tray/` and a rebuilt `nox9go-tray.exe`.
- Build the tray with `-H=windowsgui` on Windows (`make tray`) so launching it
  does not open an empty console / Windows Terminal window.
