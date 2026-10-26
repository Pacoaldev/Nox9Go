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
- **Perplexity Web speaks its own protocol.** The `perplexity-web` provider is no longer posted an OpenAI Chat Completions body: it answers `422 {"detail":[{"loc":["body","query_str"],"msg":"Field required"}]}` for that. A dedicated executor now sends the site's own contract (`query_str` at the top level and inside `params`), authenticates with the browser session cookie (`__Secure-next-auth.session-token`) instead of a bearer token, and translates the site's `blocks`/`markdown_block` event stream back into OpenAI chunks — streaming and non-streaming. Follow-up turns reuse `params.last_backend_uuid` through a bounded session cache (200 entries, 1 h TTL). *Ported from `decolua/9router` `open-sse/executors/perplexity-web.js`.*

- **The usage chart's daily window follows UTC days.** `dailyChartBuckets` derived its day buckets from the local clock and then keyed them by UTC date, so in a zone ahead of UTC and a local time before that offset — the first hours of a local day — one UTC day was crossed twice and the next was skipped, and the rollup row for the skipped day fell into no bucket at all. The chart showed 0 tokens for a day that had usage for `7d`, `30d`, `60d` and `all`. The window is now built from consecutive UTC days, matching the `dateKey` the usage writer stores. *Found while fixing `TestHandleUsageChart`.*
- **The alert bell now shows which provider and key are parked.** The badge used to answer "how many" and nothing else. `GET /api/dashboard/alerts` returns one row per alert naming the connection, provider, API key, model, the upstream's own error message, HTTP status and when the lock expires; the bell opens a panel with that list instead of a tooltip with a count. The provider display name is resolved from the dashboard's own catalog (`web/src/lib/providers.ts`), so the catalog stays in one place. *Backed by `internal/handlers/dashboard/alerts.go` and `web/src/components/AlertsBadge.svelte`.*

### Providers & proxy
- **A provider+model pair can be parked as a unit, opt-in per provider.** Lockouts used to live only per connection (`modelLock_<model>` on the connection row), so when a combo member's quota was spent the failover walked the provider's remaining keys one at a time. A new fork-local table `providerModelLocks` can park `(provider, model)` as a single state with an absolute deadline: the combo skips the whole member instantly, the selector refuses every connection of that provider for that model, and a served request clears the park. **It is off for every provider by default** — the shape of a key pool is usually independent per-key quotas, and parking the pair there would strand healthy keys. A provider only opts in when its keys share one upstream quota, via `sharedQuotaProviders`. When opted in, only the quota class parks the pair: a 429 carrying a quota/capacity marker, a 402, or a 503 carrying a quota/capacity marker. A generic 429 stays a per-connection cooldown and a 401 stays a per-account park. The dashboard bell reports a pair lockout at pair scope with its deadline when one is recorded. *Deliberate divergence from `decolua/9router`, which has no pair-level park at all.*
- **Replayed reasoning fields are stripped for providers with a strict message validator.** Cerebras answered `400 ... property "messages.N.assistant.reasoning_content" is unsupported` for a conversation replayed from a reasoning model; `groq`, `mistral`, `kiro`, `kilocode` and `kilo-gateway` refuse the same fields (`reasoning_content`, `reasoning_details`, `reasoning`). The strip now runs on every forwarded body and on the OpenAI → Kiro `conversationState` translation, not only inside the opencode / DeepSeek / Kimi injection path. Providers that *require* the field are untouched.
- **CodeBuddy conversation identity follows the caller and the body.** A connection whose stored headers carry its own `X-Conversation-*` ids keeps them instead of being overwritten by a generated set — a differently-cased caller key and the generated header are one header once Go canonicalizes them, so which one survived was decided by map iteration order and the caller's conversation was silently restarted on an unpredictable subset of turns. The conversation key is now also read from the request body (`session_id`, `prompt_cache_key`, `conversation_id`) before falling back to the opening message, so turns whose first line differs stay in one conversation. Code `11128` is reported as the channel-verification failure it is — branched on the message, because the code also carries a body-shape error — instead of surfacing as a bare `400`. *Ported from `decolua/9router` #5217.*

### Version

- Personal fork version pinned to **1.0.0** (`VERSION`, `version.json`, binary `-ldflags`).
- Tracked-upstream baselines are embedded at build time (`upstreamwatch.BaselineUpstreamVersion` / `BaselineForkVersion`). Bump them at each port; unset means the alert stays silent.
