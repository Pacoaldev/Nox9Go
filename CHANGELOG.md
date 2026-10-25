# Changelog

## [1.0.0] - 2026-10-24

Personal NOX9GO fork (`Pacoaldev/Nox9Go`). Nothing from upstream release history.

### Project

- Renamed the project to **NOX9GO** (module, binary, branding, license copyright).
- Stripped public release / CI / distribution machinery for private personal use.
- Root layout cleanup: docs under `docs/`, branding under `assets/branding/`.
- Ship NOX9GO mark (logo, favicon, tray ICO, PWA icons); dashboard brand uses the PNG on a light plate.

### Windows tray & launcher

- Tray Start/Stop works on Windows (`nox9go start` / CLI fallback for stop).
- Tray built with `-H=windowsgui` so it does not flash a console / Terminal window.
- Tray menu order: Open Dashboard above Start/Stop.

### Cloudflare tunnel

- Enable/disable Cloudflare quick tunnel from the dashboard (`cloudflared` → `trycloudflare.com`).
- Stable public hostname via `abc-tunnel.us` short URL (`https://r<shortId>.abc-tunnel.us`).
- `GET /api/health` with CORS for tunnel client pings; access gate matches the public hostname.

### Dashboard

- Runtime i18n: English + Español (España), including `title` / `placeholder` / `aria-label` / `alt`.
- **Combo flow** moved out of Usage › Overview into its own sidebar page (`/dashboard/combo-flow`), always open (no auto-collapse).
- **Quota tracker lists every connection.** `GET /api/providers/client` no longer filters the list by upstream's `features.usage` / `features.usageApikey` flags, so the accounts the tracker used to hide are visible and manageable from the same page. *Deliberate divergence from `decolua/9router` and `luqman-v1/9router-go`, which return usage-eligible connections only.*
- **Account state for providers with no metered endpoint.** Those rows report `available` / `exhausted` / `unknown` from the existing connection probe instead of an invented percentage. The probe runs **only on a manual row refresh** — the 60-second auto cycle would otherwise spend one live upstream request per unmetered account per tick.
- Quota filters and ordering persist for the browser session.
- **Upstream update alert**: a button between the latency meter and "Uso de hoy" appears when `decolua/9router` or `luqman-v1/9router-go` publishes a version newer than the one this build was ported from, and opens that version's changelog. Backed by `GET /api/upstream-versions` and `GET /api/upstream-changelog`; both are served by the gateway, never called from the browser.

### Version

- Personal fork version pinned to **1.0.0** (`VERSION`, `version.json`, binary `-ldflags`).
- Tracked-upstream baselines are embedded at build time (`upstreamwatch.BaselineUpstreamVersion` / `BaselineForkVersion`). Bump them at each port; unset means the alert stays silent.
