# Provider+model lockout

Objective: a (provider, model) pair that is in quota lockout must not be used again until the lockout expires; the combo must jump to the next member instantly instead of burning the provider's remaining API keys on the same dead pair.
Branch: `main` (private fork; no feature branches). Delivery: one work unit on `main`, no C&P (the operator withheld commit/push for this consultation).
Test runner: `go test ./internal/handlers/chat/ ./internal/db/` plus `go test -tags=integration ./internal/integration/...`; `go vet ./...`.

## Specs

S1. A **provider+model lockout** is a state that blocks one `(provider, model)` pair, not one connection. While it is active, **no** connection of that provider may serve that model, and the lockout expires on its own.

S2. A lockout is recorded when the upstream reports the **quota of that model exhausted**, not when an individual credential misbehaves. The verbatim request: *"Si un modelo de un provider esta con lockout, obligatoriamente NO SE PUEDE USAR hasta que pase el lockout que marca. Entendido? Se tiene que saltar al siguiente/otro INSTANTANEAMENTE."*

S2b. **The pair lockout is OFF for every provider by default.** Only a provider explicitly listed as sharing one upstream quota may be parked as a pair. Verbatim correction: *"No, para nada, deberia estar en 'OFF' el problema ahi, por ejemplo con Mistral es que 'bloquea' a las otras sanas porque las bombardea y entonces activa el 'lock' de REQUESTS POR MINUTO..."*. For every other provider each key keeps its own quota, so a failure on one key parks only that key and the healthy siblings keep serving the turn.

S3. The trigger set is the quota class only, because the damage the operator reported is a shared quota spent key by key:
   - 429 whose body carries a quota/capacity marker (`QUOTA_EXHAUSTED`, `RESOURCE_EXHAUSTED`, `MODEL_CAPACITY_EXHAUSTED`, "Individual quota reached", "quota exceeded", "capacity", "overloaded", "resource has been exhausted").
   - 402 (insufficient funds for the model tier).
   - 503 with a quota/capacity marker.
   A **generic 429 without a quota marker is NOT a provider+model lockout**: it is a per-account or per-IP rate limit, and the operator's other keys may still serve. It keeps today's per-connection cooldown.

S4. A credential failure (**401 invalid OAuth / revoked grant**) is **not** a provider+model lockout. It parks that one account; the operator's other keys of the same provider must keep serving.

S5. The combo **skips the whole entry** `(provider, model)` when the pair is locked — no connection of that provider is dialled at all, and the next combo member is tried in the same turn. Verbatim: *"no se siga BOMBARDEANDO ese mismo provider-modelo ... el resto de las API Keys añadidas a modo de failback de ese provider las satura y las mete a todas en 'Rate limit exceeded'"*.

S6. The single-model path (a direct request to one provider+model) honours the same lockout: the selector does not hand out a connection for a locked pair.

S7. The lockout is **self-expiring**: it stores an absolute deadline and is treated as absent once it passes. A served request clears it, the way a served request clears the per-connection model lock.

S8. The lockout is **visible in the dashboard** as an alert, so the operator can see which pair is parked and until when, and can clear it by hand.

S9. The lockout never touches the per-connection `modelLock_*` / `rateLimitedUntil` fields. Those keep their current per-connection meaning, and the shared Next.js dashboard keeps reading them.

## Tasks

| ID | Spec | Route | Status | Evidence |
| :-- | :-- | :-- | :-- | :-- |
| T1 | S1, S7 | delegated | done | `internal/db/provider_model_lock.go` — `providerModelLocks` tableDef, atomic upsert that never shortens a longer park, expired-reads-as-absent, scope matrix clear, list for the dashboard |
| T2 | S1 | delegated | done | `internal/db/schema.go` — the table joins `coreSchema` as a fork-local entry |
| T3 | S2, S3, S4 | delegated | done | `internal/handlers/chat/provider_model_lock.go` — 8 quota markers, 402/429/503 gate, deadline via `retryableCooldownSec` + `GetQuotaCooldown`, probe no-op |
| T4 | S5, S6 | delegated | done | `combo.go` both loops skip the parked member and break out of the connection walk; `connections.go` `connectionModelBlocked` + `pinnedConnectionIneligible` refuse the pair; `fallback.go` parks on the failure and clears on success |
| T5 | S1-S9 | delegated | done | `internal/handlers/chat/provider_model_lock_test.go` (9 tests), `internal/db/provider_model_lock_test.go` (6 tests) — all green |
| T6 | S8 | inline | done | `internal/handlers/dashboard/alerts.go` pair-scope alert + `ProviderLock` count; `status.go` bell count; `AlertsBadge.svelte` renders model instead of key; `client.ts` types |
| T7 | S1-S9 | inline | done | gates below + `CHANGELOG.md` entry naming the divergence |

Route note: the Writer trigger fired (four non-trivial files across two packages, with disjoint edit surfaces), so T1-T5 went to one bounded writer. The parent kept the design, the review disposition and the git actions. The parent then re-scoped two pre-existing failover subtests whose contract the spec deliberately reverses, and fixed a route that only existed in the test-only table (see L7).

## Log

L1 (verbatim): "Quiero me revises o implementes algun sistema para que cuando uno de los "providers-modelos" de DENTRO DE LOS SELECCIONADOS en un "combo" tiene "lockout" no se siga BOMBARDEANDO ese mismo provider-modelo, porque asi lo que pasa es que el resto de las API Keys añadidas a modo de failback de ese provider las satura y las mete a todas en "Rate limit exceeded"... Si un modelo de un provider esta con lockout, obligatoriamente NO SE PUEDE USAR hasta que pase el lockout que marca. Entendido? Se tiene que saltar al siguiente/otro INSTANTANEAMENTE. Me tengo que ausentar ahora, y no voy a estar para contestarte, tienes mi permiso para lo aplicar tu recomendacion hasta que consigas tener esto que te pido al 100% pero NO puedes hacer C&P, eso para esta consulta tendre que darte permiso, SOLO para esta peticion."

L2: The operator is away and cannot answer questions, so the design decisions the request leaves open were made by the parent and are recorded here:

- **S3, the trigger set.** A generic 429 stays a per-connection cooldown; only a quota-marked 429 / 402 / 503-capacity parks the whole pair. Reason: a generic 429 is frequently a per-IP or per-account limit where the operator's other keys still work, and promoting it would strand working keys. A quota-marked failure is a fact about the model at that provider, which is exactly the case that burns the failback pool.
- **S4, credential failures.** A 401 invalid OAuth stays a per-account park. Promoting it would take a whole provider out of rotation because one grant expired.
- **S7, expiry.** The lockout stores an absolute deadline rather than a backoff counter, so "hasta que pase el lockout que marca" is literal and a served request can clear it early.
- **S9, storage shape.** A dedicated table, not a new field on the per-connection JSON blob: the state belongs to the pair, not to a credential, and the Next.js dashboard must keep reading the per-connection fields unchanged.

L2b (correction, verbatim): *"No, para nada, deberia estar en 'OFF' el problema ahi, por ejemplo con Mistral es que 'bloquea' a las otras sanas porque las bombardea y entonces activa el 'lock' de REQUESTS POR MINUTO..."*

That correction reverses the default. The first delivery parked any provider whose keys answered a quota marker, which is wrong for the ordinary shape of a key pool: independent keys, each with its own quota. The pair lockout is now **opt-in per provider** through `sharedQuotaProviders`, empty by default, so no provider is parked as a pair unless the operator names it. Every other provider keeps the pre-existing behaviour: the failing key is cooled down on its own and the healthy siblings serve the turn.

The operator's own diagnosis of the Mistral damage also rules the trigger out: what Mistral returns is `{"message":"Rate limit exceeded","type":"rate_limited","code":"1300"}` — no quota marker — so it never reached the pair classifier at all. The harm was the walk over the pool triggering the provider's per-minute request lock, which is a per-connection cooldown matter, not a pair matter.

L3: The map of the existing behaviour, read before designing:

- `internal/handlers/chat/combo.go:568` — the combo loop over members. `combo.go:576` skips a member whose every connection is model-locked (`IsProviderAvailable`). `combo.go:586` — the inner loop over up to ten connections of that member. `combo.go:621-633` — the pre-flight per-connection lock check. `combo.go:753-755` — `comboLockRetryable` writes the per-connection lock and excludes the connection. `combo.go:1081` — `comboLockRetryable` itself: it locks the connection for the model and appends it to `excludeIDs`, which is what makes the loop walk to the **next key of the same provider**.
- `internal/handlers/chat/connections.go:383` — `connectionModelBlocked` in the selector, the per-connection half of the same rule. `connections.go:470` — that helper.
- `internal/handlers/chat/fallback.go:206-266` — the single-model path locks the connection the same way.
- `internal/db/accounts.go:165` — `IsConnectionModelLocked` reads `modelLock_<model>` out of the connection's `data` JSON. `accounts.go:270` — `IsProviderAvailable` answers "is at least one connection of this provider free for this model", which is why one locked key does not stop the walk.
- `internal/providers/errorclassify.go:40-64` — the error rules; `errorclassify.go:112` — `GetQuotaCooldown`, the 2s-to-5min backoff ladder. `internal/handlers/chat/retry_after.go:59` — `retryableCooldownSec`, which honours Retry-After and reset hints.
- `internal/handlers/chat/fallback.go:1499` — `isModelQuotaText`, the existing quota markers, and `fallback.go:1544` — `isModelScopedError`, the existing model-vs-account split.

L4: Measured on the operator's own database (`%APPDATA%\9router\db\data.sqlite`, 83 MB): 52 `nvidia` connections (5 active), 26 `mistral` (26 active), 25 `openrouter` (5 active), 12 `minimax-cn` (4 active). The two live combos list 6 and 15 members. A pair of provider and model is therefore backed by a pool of keys, and the current walk spends that pool one key at a time — the saturation the operator reports.

L5: The upstream counterpart for this behaviour could not be read: `https://raw.githubusercontent.com/decolua/9router/main/open-sse/services/accountFallback.js` answers 404, and the path of the file that carries `isModelLockActive` / `filterAvailableAccounts` is not known. This is therefore a **fork-local behaviour**, not an upstream port, and it is recorded as such in `CHANGELOG.md`.

L6: Two pre-existing subtests of `TestComboRateLimitFailover` failed after the change and were re-scoped, not silenced. Their body is `retryInfoBody("120s")` = `{"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED", ...}`, which carries a quota marker, so the pair is now parked and the second account is never dialled. That is the contract S5 asks for, so the two cases were rewritten to pin the new outcome (`wantHits: 1`, client 429 with `Retry-After: 120`) and renamed to say "quota-marked". The five sibling subtests that use a body **without** a quota marker still pass unchanged, which proves the marker is the only variable and that a generic 429 keeps its old per-account walk.

L7: A live check against the running gateway found a routing bug that no unit test could see: `GET /api/dashboard/alerts` answered **404 on the real server**. The route was registered only in `dashboard.RegisterRoutes`, and that table is mounted by tests only — the same trap the file already documents for `/api/provider-nodes/{id}/limits` and the smart-combo routes. The route now also lives in `SetupDashboardRoutes` next to `/api/dashboard/status`. It answers 200 with the CLI token after the rebuild.

L8: End-to-end proof against a fake quota-429 upstream (`httptest`-shaped Python server on loopback, two `deepseek` keys pointed at it, gateway started with `ALLOW_PRIVATE_NETWORKING=true` because the SSRF guard refuses loopback by default):

- Request 1 → client `429`; the fake upstream logged **1** hit, not 2; a `providerModelLocks` row appeared: `deepseek | deepseek-chat | quota_exhausted | 429`.
- Request 2, immediately after → client answered in **1 ms** and the upstream hit counter stayed at **1**. The pair was skipped without dialling any key, which is the "saltar al siguiente INSTANTANEAMENTE" the request asks for.

One earlier attempt looked like a failure and was not: the second request arrived 6 s after the first, past the 2 s cooldown the upstream had earned, so the pair was legitimately free again. The wall clock was verified independently (18:03 local = 17:03 UTC) before concluding the code was right.

L9: Test data removed afterwards. The probe connections (`zz-probe-%`) and every `providerModelLocks` row are gone; the operator's `providerConnections` count is back to its original 184, and the gateway was restarted without the private-networking override.

L10: Gates observed after the change:

- `go test ./internal/... ./web/...` — no `FAIL` line.
- `go test -tags=integration ./internal/integration/...` — only `TestChangelogShowsPendingFragments` fails, which is the pre-existing failure this repo's own agent notes record: it asserts the `.changes/` fragment workflow that `AGENTS.md` §7.4 says was removed from this fork.
- `go vet ./...` — exit 0.
- `cd web && bun run ratchet:svelte` — 0 unresolved identifiers, 77 errors against a baseline of 77.
- `cd web && bun run build` — built; 2524 KB → 522 KB brotli. The JS chunk moved 1,264.60 → 1,264.95 kB (+0.35 kB) for the alert-panel branch, well inside the noise budget of §6.F.
- `go build -ldflags ... -o nox9go.exe ./cmd/nox9go/` — OK; binary rebuilt and the service restarted, `/api/health` 200.

L11: Nothing was committed and nothing was pushed. The operator withheld C&P for this consultation, so every change above is in the working tree only.

L12: Observed in the operator's own gateway log while the correction was being verified, on the real `api.mistral.ai` traffic:

```
conn=Key 88  provider=mistral model=mistral-medium-3-5 status=429 "Rate limit exceeded" → locked cooldown_s=32
conn=Key 89  ... → locked cooldown_s=32
conn=Key 104 ... → locked cooldown_s=32
conn=Key 105 ... → locked cooldown_s=32
conn=Key 145 → 200 (served the turn)
```

No `pair parked` line appears. Each failing key was cooled on its own and a healthy sibling served the turn — the behaviour the correction asks for. It also confirms the diagnostic: Mistral's body is `{"message":"Rate limit exceeded","type":"rate_limited","code":"1300"}`, carrying no quota marker, so it never reached the pair classifier.

L13: Gates re-run after the correction: `go test ./internal/... ./web/...` no `FAIL`; `go vet ./...` exit 0; `TestComboRateLimitFailover` all seven subtests pass, including the two restored to their original contract (`wantHits: 2`, second account dialled) because the pair is no longer parked by default; binary rebuilt, service restarted, `/api/health` 200. Probe rows removed: `providerConnections` back to its original 184, `providerModelLocks` empty.
