# Upstream version notice

Objective: show a dashboard button when either tracked upstream repository publishes a version newer than the one this build was ported from, and open that version's changelog when pressed.
Branch: `main` (private fork; no feature branches). Delivery: `single-pr` equivalent = one work unit on `main`.
Test runner: `go test ./...` plus `-tags=integration`; frontend `cd web && bun run build` and `bun run ratchet:svelte` (`.atl/skill-registry.md` → `go-testing`, `ponytail`, `svelte`).

## Specs

S1. A button appears in the top bar **between the latency meter and "Uso de hoy"**, and only when at least one tracked source has a version newer than this build's baseline. The voice of the request: *"entre el 'medidor de latencia' y 'Uso de hoy' añada un boton que muestre cuando alguno de los dos tiene alguna version nueva"*.

S2. Pressing that button shows the **changelog of the newly discovered version**: *"al apretar 'este boton' ... me saque el 'CHANGELOG' con lo nuevo que han implementado en esa version nueva que acaba de descubrir"*.

S3. Both sources are tracked:
   - `decolua/9router` — the official upstream (the behavioural specification).
   - `luqman-v1/9router-go` — the sibling fork cited by the operator.

S4. The comparison baseline is **baked into the build** via `-ldflags` (operator's explicit choice). No persisted "last seen" state, no DB write. A fresh install therefore reports "nothing new" until a source actually publishes past the baseline.

S5. Discovery and changelog retrieval happen **server-side**. `AGENTS.md` §6.G.1 forbids any asset or call from a third-party origin in the dashboard, and proxying keeps the operator's IP off GitHub.

S6. Quota-tracker work already agreed and now implemented: every visible connection is listed, providers without a metered endpoint report account state (`available` / `exhausted` / `unknown`) from an on-demand probe, and the probe never runs on the 60-second auto cycle. This is scope already delivered, recorded here so the two changes are not confused.

## Tasks

| ID | Spec | Route | Status | Evidence |
| :-- | :-- | :-- | :-- | :-- |
| T1 | S3, S4 | inline | done | `internal/upstreamwatch/` — baselines, tag/release listing, semver compare, bounded caches; `go test ./internal/upstreamwatch/` PASS |
| T2 | S3, S5 | inline | done | `internal/handlers/dashboard/upstream_watch.go` + `/api/upstream-versions`, `/api/upstream-changelog` in `router.go`; `go test ./internal/handlers/dashboard/ -run TestUpstream` PASS |
| T3 | S1, S2 | inline | done | `ChangelogModal.svelte` parameterized, `TopBar.svelte` button, `client.ts` methods, `es.json`; `bun run build` + ratchet 77 ≤ 78 |
| T4 | S4 | inline | done | `-ldflags` documented in `AGENTS.md` §7.2 and wired into the build command; verified via live API response |
| T5 | S1-S5 | inline | done | gates: `go test ./internal/...` (except baseline browserlogin failure), `bun run build`, svelte ratchet, binary rebuilt and restarted, live `/api/health` 200, both endpoints verified |

Route note: the delegation trigger fired (2+ non-trivial files), but this runtime exposes no `gentle-ai-worker`/`gentle-ai-verify` agent — only `explore`, `general` and review lenses. Degraded to inline execution, as the rule allows.

## Log

L1 (verbatim): "Quiero que mandes un agente para que vaya definiendo un plan para implementar esto: Tiene que ir al repo oficial 9Router (https://github.com/decolua/9router) y al fork 9router-go (https://github.com/luqman-v1/9router-go) y la implementacion es que cuando se detecte una version nueva en alguno de los dos quiero que en nuestro dashboard, entre el "medidor de latencia" y "Uso de hoy" añada un boton que muestre cuando alguno de los dos tiene alguna version nueva, y que al apretar "este boton" nuevo que te comento me saque el "CHANGELOG" con lo nuevo que han implementado en esa version nueva que acaba de descubrir. Entendido?"

L2 (verbatim correction): "No, solo revisa ambos repos oficiales para ir detectando cuando sacan nuevas versiones, entonces hace lo que te he explicado en el mensaje de antes."

L3: Read-only research of both official repositories established the facts the design rests on:
- `decolua/9router` publishes **no GitHub Release** for current versions (`/releases/tags/v0.5.109` is a 404; Releases stop at v0.5.35). Its versions live as **tags**, ordered unpredictably in the API (page one returned `v0.5.109, v0.5.104, v0.5.95`), so sorting must be by semver client-side, never by page order.
- `luqman-v1/9router-go` publishes **Releases**, including `prerelease: true` churn (`v1.9.15-exp.9`). Prereleases are ignored by default.
- Both repositories are public, so no credential is involved.
- Unauthenticated GitHub API limit is 60 req/hour per IP (documented, not measured here). Two sources refreshed on a half-hour cache spends ~4 calls an hour.
- A fork release body is large (100+ KB), so the changelog is fetched per version on click, size-capped server-side, never the whole file.

L4: Baseline is a build-time variable, so nothing is written to the database and a restart cannot lose it. Bumping it is a deliberate act at each port.

L5: Distinguishing feature of the two rows on screen: a metered provider renders quota bars from `fetchProviderUsage`; an unmetered one renders the account-state line from the probe. Both now appear in the same list.

L6: Live verification found a design bug the unit tests could not: the fork's release **bodies** run past 100 KB each, so paging thirty releases overflowed the 1 MB read cap and the JSON arrived truncated (`jsontext: unexpected EOF ... after offset 1048576`). The source now asks for `/releases/latest`, which GitHub documents as skipping drafts and prereleases — one small body, and the selection rule is GitHub's rather than ours. The prerelease guard stays as a defensive check on the answer.

L7: Live reads, against the running gateway, with the CLI token:
- `GET /api/upstream-versions` → `200`; both sources present, `hasNew:false` for each because the baselines equal the latest. Without the token the same route answers `401`.
- `GET /api/upstream-changelog?source=fork&version=v1.9.14` → `200`, `text/markdown`, 125 480 bytes, no raw `<script>`.
- `GET /api/upstream-changelog?source=upstream&version=v0.5.109` → `200`, 5 525 bytes, and the body opens with the version's own heading (`# v0.5.109 (2026-10-23)`).
- `…&version=v9.9.9` → `404 that version is not published`; a path-walk version → `400`; an untracked source → `400`.

L8: The notes are escaped (`<` → `&lt;`) before they leave the handler. The modal renders them with `{@html}` and marked passes raw tags through by design, so an unescaped `<script>` in a release body written by a repository the operator does not control would run in the dashboard's own origin, which holds the session cookie and the CLI token.

L9: Pre-existing failure, verified against HEAD with this change stashed and the new files moved aside: `internal/integration TestChangelogShowsPendingFragments` fails on `main` alone. It asserts the served changelog contains a `## [v` heading, and this fork's `CHANGELOG.md` uses `## [1.0.0]`; the same test also expects the `.changes/` fragment workflow that `AGENTS.md` §7.4 says was removed from this fork. Untouched by this work and out of scope. The other baseline failure is `internal/browserlogin TestBrowserNamesCoverTheBrowsersTheFlowCanDrive`.