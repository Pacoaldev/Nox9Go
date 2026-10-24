### Stable public tunnel URL via the short-URL worker

- The dashboard tunnel now hands out a stable public hostname
  (`https://r<shortId>.abc-tunnel.us`) instead of the raw
  `*.trycloudflare.com` URL. The raw URL changes on every respawn, so a
  client configured with it broke on the next restart; the short hostname
  survives because the worker keeps the `shortId → URL` mapping.
- Ports upstream `decolua/9router` `src/lib/tunnel/cloudflare/manager.js`:
  the quick-tunnel URL is registered with the `abc-tunnel.us` worker and the
  short id is persisted next to the cloudflared PID file, so a respawn reuses
  the same id instead of issuing a new hostname. A disable keeps the id too
  (upstream `shared/state.js` `disableTunnel`).
- A worker outage degrades the public URL, not the tunnel: the direct
  `trycloudflare.com` URL still routes traffic and is offered as a second copy
  button next to the public one.
- The tunnel access gate now also matches the public hostname. It is a
  different host from the raw URL the tunnel serves, so a gate that only knew
  the raw one would have let every request through the public hostname and made
  the "allow dashboard access via tunnel" toggle decorative.
- Verified: `go test ./internal/tunnel/cloudflare/ ./internal/auth/
  ./internal/handlers/dashboard/ -count=1`; the registration was also
  exercised against the live worker.
