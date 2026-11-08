# Provider capacity learning (integrated providers)

Objective: expose the same "Capacity limits" editor that custom provider nodes have, for INTEGRATED (catalog) providers, and auto-fill it from the limits/refusals those providers report at runtime, building a small per-provider knowledge base.
Branch: feat/provider-capacity-learning
Delivery strategy: ask-on-risk (feature branch, work-unit commits)
Test runner: go test ./... (unit), go test -tags=integration ./internal/integration/... (feature), cd web; bun run build; make vet-svelte (frontend)

## Specs

S1. A "Capacity limits" editor is reachable from the dashboard for an integrated provider (a catalog provider such as `bazaarlink`, `antigravity`, `opencode`, `claude`), not only for custom provider nodes. Fields match the node editor: Requests / minute, Prompt tokens / minute, Concurrent requests, Queue wait (ms), Lifetime token quota, Contract ends (optional). 0 means unlimited; empty date means no expiry.

S2. Configured capacity for an integrated provider is enforced on the request path through the existing provider limiter, exactly like a custom node: a request that cannot get a slot fails over to another candidate, and 429 appears only once no candidate is left.

S3. The gateway records, per integrated provider (and model), the capacity facts an upstream reports: a classification signal (rate_limit, quota_exceeded, capacity, timeout, auth, server_error, model_fit, request_invalid), the HTTP status, a numeric limit when one is parseable, its unit (rpm, tpm, requests, tokens, concurrent), a bounded evidence snippet, a sample count, and first/last seen timestamps.

S4. Recording is conservative: only recognized signals are stored; a number is stored only when a known pattern matches (structured headers `x-ratelimit-*`/`retry-after`, or error-body phrases such as "limit of <n>..." / "<n> requests"). Unknown errors are classified as `server_error`/`auth` by status with no invented number. Evidence is bounded and never contains credentials.

S5. `GET /api/providers/{id}/capacity` returns the configured limits (S1), the learned observations (S3), and suggested limit values derived from those observations. `PUT /api/providers/{id}/capacity` saves the configured limits (same tri-state merge as nodes: omitted keeps, explicit 0 clears, explicit number sets).

S6. Suggestions prefill the editor but are never applied automatically: an observation never changes an active stored limit on its own. The operator saves to apply.

S7. Recording must not affect request latency materially and must never fail the request: a storage error during recording is logged and ignored (fail-open), same rule as the existing provider-limit policy read.

S8. The capacity editor offers a fixed daily request budget (`Requests / day`), distinct from the per-minute window: it resets at 00:00 UTC (the boundary a provider that publishes a daily allowance names), it is not queueable, and an exhausted day refuses the attempt (429, `provider_daily_limit_exhausted`) so the failover moves on. A request the provider never saw (a TPM refusal or a queue timeout) does not spend it. The field is available for integrated providers and custom nodes, and an observed "daily ... requests" figure prefills it.

S9. Saving the editor must not crash on an untouched or cleared count field: an empty `<input type="number">` binds to `null`, and a `null` count reads as unlimited (0), never as a client-side exception.

## Tasks

- [x] T1. DB + repo: `providerCapacity` and `providerObservations` tables + reader/writer methods + tests. Route: inline. Commit 2b41fd50. Evidence: `go test ./internal/db/` green.
- [x] T2. Extractor: `internal/providers/observation.go` ClassifyObservation + table tests. Route: inline. Commit (providers observation). Evidence: `go test ./internal/providers/` green.
- [x] T3. Recording hook: record observations for integrated providers in the chat failure path + tests. Route: inline. Commit ef5f6e31. Evidence: `go test ./internal/handlers/chat/` green.
- [x] T4. Enforcement: extend providerLimitsFor/acquire path to read integrated capacity + tests. Route: inline. Commit ef5f6e31.
- [x] T5. Dashboard endpoints: GET/PUT `/api/providers/{id}/capacity` + routes + tests. Route: inline. Commit c332362d. Evidence: `go test ./internal/handlers/dashboard/` green.
- [x] T6. Frontend: capacity editor for integrated providers in ProviderDetailView + component + api client + i18n. Route: inline. Commit 3de9b8ee. Evidence: `bun run ratchet:svelte` (0 unresolved, baseline 76), `cd web; bun run build` OK.
- [x] T7. Daily request budget (`Requests / day`): limiter fixed UTC-day window + DB columns on both tables + chat/dashboard wiring + extractor `rpd` + both editors + tests. Route: inline. Evidence: `go test ./...` green; `internal/middleware/ratelimit_provider_daily_test.go`; live GET returns `dailyRequests`/`dailyUsedWindow`.
- [x] T8. Fix: saving the editor crashed on a `null` count (empty number input) — `countOf` null-safe, draft type + `Input` accept `null`, regression test. Route: inline. Evidence: `bun test src/lib/node-limits.test.ts` 22 pass; `bun run ratchet:svelte` baseline 76.

## Verification (observed)

- `go vet ./...` → clean.
- `go test ./...` → all packages ok (chat 35s, dashboard 16s, db 12s).
- `go test -tags=integration ./internal/integration/...` → ok (87s), provider limits included.
- `cd web; bun run ratchet:svelte` → 0 unresolved identifiers, 76 errors (baseline 76).
- `cd web; bun run build` → built; precompressed 2683 KB → 567 KB brotli.
- `go build ./...` → clean.

## Log

L1 (user, verbatim): "Me refiero a tener acceso desde el dashboard a como cuando configuramos un "custom provider" que sale esto de "Capacity limits" [Image 2] Ademas estaria genial que se fueran "autorellenando" esos valores segun los "mensajes que vayan devolviendo de "error" o "fallo" los mismos providers y asi ir haciendo una pequeña DB de cada uno de ellos."

L2 (user, verbatim): "No lo quiero para los custom, lo quiero para LOS INTEGRADOS. Adelante con la implementacion."

L3 (decision): scope is INTEGRATED (catalog) providers only. Custom nodes already have this. Auto-fill prefills the editor; it does not silently auto-apply/enforce (accepted from the earlier design: "Autorrellenar sugerencias, nunca cambiar límites activos automáticamente").

L4 (user, verbatim): "Aparece asi, pero como añado por ejemplo: 60 request al DIA?" — the editor had no daily field; a per-minute window cannot express a provider's fixed daily allowance.

L5 (feature change, from L4): added `Requests / day`, a fixed UTC-day budget. `middleware.Limits.DailyRequests` + `dailyWindow` (resets at 00:00 UTC, not queueable, refunded when the attempt never left); stored on `providerCapacity` and `providerNodes`; exposed in both editors and responses with `dailyUsedWindow`; the extractor upgrades a "daily ... requests" figure to unit `rpd`. Spec S8 below. Tests: `internal/middleware/ratelimit_provider_daily_test.go`, `TestAcquireProviderAttempt_EnforcesIntegratedDailyBudget`, `TestProviderCapacity_PutThenGet` (dailyRequests), `node-limits.test.ts`.

L6 (user, verbatim): "ahora ya aparece lo de request/day pero sale este error al salvar [Image 1]" — "Cannot read properties of null (reading 'trim')". Root cause: Svelte binds a cleared `<input type="number">` to `null`, and `countOf` called `.trim()` on it, so the save died client-side before the request left. Fix: `countOf` treats null/undefined as 0; draft type and `Input` accept `null`. Regression test added in `node-limits.test.ts`.

