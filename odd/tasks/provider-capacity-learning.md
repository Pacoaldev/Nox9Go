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

## Tasks

- [x] T1. DB + repo: `providerCapacity` and `providerObservations` tables + reader/writer methods + tests. Route: inline. Commit 2b41fd50. Evidence: `go test ./internal/db/` green.
- [x] T2. Extractor: `internal/providers/observation.go` ClassifyObservation + table tests. Route: inline. Commit (providers observation). Evidence: `go test ./internal/providers/` green.
- [x] T3. Recording hook: record observations for integrated providers in the chat failure path + tests. Route: inline. Commit ef5f6e31. Evidence: `go test ./internal/handlers/chat/` green.
- [x] T4. Enforcement: extend providerLimitsFor/acquire path to read integrated capacity + tests. Route: inline. Commit ef5f6e31.
- [x] T5. Dashboard endpoints: GET/PUT `/api/providers/{id}/capacity` + routes + tests. Route: inline. Commit c332362d. Evidence: `go test ./internal/handlers/dashboard/` green.
- [x] T6. Frontend: capacity editor for integrated providers in ProviderDetailView + component + api client + i18n. Route: inline. Commit 3de9b8ee. Evidence: `bun run ratchet:svelte` (0 unresolved, baseline 76), `cd web; bun run build` OK.

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
