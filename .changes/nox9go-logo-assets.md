### NOX9GO logo and tray/favicon icons

- Ship `logo_9nox.svg` / `favicon.svg` from the NOX9GO mark, plus `favicon.ico`
  and `cmd/nox9go-tray/tray.ico` from `assets/branding/NOX9GO.ico`.
- Regenerate PWA `icon-192.png` / `icon-512.png` from that ICO (replacing the
  old “9R” stubs) and drop the duplicate sized SVG icon entries.
- Register `/logo_9nox.svg` on the SPA static routes (same allow-list as
  favicon).
- Rename tray menu title and `manifest.webmanifest` brand strings to NOX9GO.
- Dashboard brand mark: use `icon-192.png` on a white plate (sidebar, login,
  analytics). The path-traced SVG is dark and illegible at ~36px on `surface-2`.
