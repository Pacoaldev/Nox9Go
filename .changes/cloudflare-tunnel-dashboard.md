### Cloudflare Tunnel from the dashboard

- Wired `POST /api/tunnel/enable` / `disable` to a real `cloudflared` quick
  tunnel (`trycloudflare.com`), matching upstream `decolua/9router`
  `src/lib/tunnel/cloudflare/` behavior for personal use (no Cloudflare account).
- Downloads `cloudflared` into the data `bin/` dir on first enable; hides the
  Windows console window; persists `tunnelEnabled` / `tunnelUrl` in settings.
- Added public `GET /api/health` with CORS `*` so the dashboard client ping
  through the tunnel URL works (upstream `src/app/api/health/route.js`).
- Verified: `go test ./internal/tunnel/cloudflare/ ./internal/handlers/dashboard/ -count=1`.
