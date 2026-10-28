# NOX9GO — Team Engineering & AI Collaboration Guidelines

Personal Go reimplementation of [**decolua/9router**](https://github.com/decolua/9router), shipped as a single static binary with an embedded Svelte 5 dashboard.

This is a **public** fork (`github.com/Pacoaldev/Nox9Go`): CI runs on GitHub Actions, releases are tagged manually. Everything in this document describes how *this* repo is worked on, and every step below is meant to work on a clean clone on any supported platform.

---

## 0. Read this first — the environment

| Fact | Value |
| :--- | :--- |
| Remote | `https://github.com/Pacoaldev/Nox9Go.git` (public) |
| Branches | `main` only. No `dev`, no feature branches, no PR flow. |
| Supported platforms | Windows amd64, Linux amd64/arm64, macOS amd64/arm64 (`make cross`) |
| Toolchain | `go 1.27`, `bun` 1.4.x, `git`, GNU Make 3.82+ (CI pins a version that has `$(file …)`) |
| `golangci-lint` | Optional. The lint gate in CI is `go vet` plus `oxlint` on the frontend. |
| Upstream clone | None needed. `decolua/9router` is read over the network (§1). |
| Data dir | Per platform: `%APPDATA%\nox9go` on Windows, `~/.nox9go` elsewhere. Override with `DATA_DIR`. See `internal/config` → `ResolveDataDir`. |
| Dashboard | `http://localhost:<PORT>/dashboard` (20130 by default; the startup notice prints the real port) — the first start generates the dashboard password and prints it; the first login with it forces a new one. |
| bun version | `web/bun.lock` is `lockfileVersion: 2`, written by bun 1.4.4. `bun install --frozen-lockfile` with an older or newer major rejects the file and rewrites it, so pin 1.4.x; CI sets `BUN_VERSION` to match. |
| Version | `VERSION` is the single source of truth. `version.json` is generated from it (see `scripts/gen-version-manifest.mjs`). |
| CI | `.github/workflows/ci.yml` — build, vet, tests, race detector, frontend check, secret scan. |

### 0.1 How the project is worked on (read this before you touch anything)

- **Language.** Code, identifiers, comments, commit messages, docs and UI copy are in English. The README is in Spanish because that is the product's audience.
- **Minimal change by default.** Smallest change that fixes the real problem. No speculative abstractions, no "while we are here" refactors, no option menus where one path works. Over-thinking has been pushed back on before — a 15-minute detour is a defect, not diligence.
- **Reuse before you add.** Shared state (token cache, model list, connection row, quota counter) goes through the existing helper, never a second implementation with a different shape.
- **One question at a time, then stop.** Ask only when there is a real product decision to make; then wait for the answer instead of choosing.
- **Never commit without an explicit ask.** `C&P` means *commit and push*, autonomously: inspect `git status`/`git diff`, stage only what was intended, Conventional Commit message, no AI attribution, no `--no-verify`. If there is nothing to commit, say so and stop.
- **Measure, don't assume.** "This library can't be used here" costs one command to check. Measure bundle size, RSS, latency, module count before calling anything heavy.
- **Verify with observed output.** A green build is not proof. Run the test, hit the endpoint, quote what came back.
- **Never hand-edit what a generator owns.** `web/dist` is build output. `web/public/i18n/literals/es.json` is the translation source of truth. `internal/providers/registry_models.go` is the model catalog.
- **Report honestly.** A failed, skipped, or unavailable check is reported as such — never dressed up as a pass.

### 0.2 Things that look true but are not

- **The dashboard is embedded in the binary** (`web/embed.go` → `//go:embed all:dist`). A frontend change is not live until the SPA is rebuilt, the Go binary is rebuilt, and the service restarts (§7.2). A FE-only build changes nothing the browser sees.
- **A running `nox9go.exe` cannot be overwritten** on Windows. The order is always: stop → build → start.
- **`nox9go.exe` with no arguments opens an interactive menu**; it does not start the gateway. Use `nox9go start`.
- **The dashboard API requires auth.** The CLI token travels in the `x-9r-cli-token` header (`sha256(machine-id + "9r-cli-auth" + cli-secret).hex[:16]`, files in the data dir). Without it: `401`. An unknown route: `404`. That pair is the cheap way to prove the wiring is alive.
- **CI lives in `.github/workflows/ci.yml`.** It builds the frontend from the lockfile, runs `svelte-check`, `go vet`, `go test ./...`, the race detector, the secret scanner and the release-asset check. The same gates run locally in §8, so a green local run is the same work CI does.
- **`make` must be 3.82 or newer.** The Makefile reads `VERSION` with `$(file <VERSION)`, and under 3.81 that expands to empty and embeds an empty version. Every target below works under `sh` and `cmd.exe`, but this one needs the newer Make.

---

## 1. Upstream Source of Truth (fetch, don't look for a clone)

Upstream remains the **behavioral specification**. A local clone is optional; the commands below read it over the network.

- **Upstream repository**: `https://github.com/decolua/9router`
- **Read a file**: `https://raw.githubusercontent.com/decolua/9router/main/<path>`
- **Search upstream**: `https://api.github.com/search/code?q=<term>+repo:decolua/9router` (or `gh api search/code -f q='...' --jq '.items[].path'`)
- **History / PRs / issues**: `gh api repos/decolua/9router/commits?per_page=20`, `gh pr list -R decolua/9router`, `gh issue list -R decolua/9router`
- Upstream tests live in `tests/` (Vitest) — they are the fastest way to learn an edge case, a header, or a protocol nuance.

### Parity Protocol
1. **Inspect upstream before writing.** Find the counterpart of what you are about to change, read it, and read its upstream test. Do not port from memory.
2. **Behavioral compatibility (100% contract match)**:
   - Request/response schemas for `/v1/*` (Chat Completions, Messages, Embeddings, Audio, Models).
   - Dashboard endpoints `/api/*` (Connections, Combos, ProxyPools, Settings, ProviderNodes, Usage, Auth).
   - Combo expansion, model fallback, account rotation, strike-breaker quota handling.
   - SQLite database compatibility (same schema, so both apps can share one data dir).
3. **Record the upstream reference.** When porting a feature or fix, note the upstream commit/issue/PR in [`CHANGELOG.md`](CHANGELOG.md) under the current personal version (§7.4).

### Secondary reference: OmniRoute (source of many feature requests)

Some requests in this repo are **not** parity requests against `decolua/9router` — they are **port requests sourced from OmniRoute**, cited by a file path inside that repo (e.g. issue #38 cites `src/lib/usage/codexResetCredits.ts`).

- **Reference repository**: `https://github.com/diegosouzapw/OmniRoute` — TypeScript/Next.js, MIT, a *sibling* gateway project (not this repo's upstream). It vendors its own `open-sse/` tree under the `@omniroute/open-sse` package alias, so its provider/executor layout is recognizable but **not** the same as upstream's.
- **When a request cites OmniRoute**, treat the cited file as the *feature specification*. Do not guess its contents and do not port from memory.

**OmniRoute Porting Protocol**
1. **Fetch the cited file first**, from raw: `https://raw.githubusercontent.com/diegosouzapw/OmniRoute/main/<path>`. Fetch the related UI component too (dashboard modals live under its `src/app/` tree) — the port must match the dashboard *behavior*, not just the API shape.
2. **Port the contract, not the code**: endpoints, request/response payloads, error status codes, and filter/sort rules. Never transliterate TypeScript into Go (§4.A).
3. **Map by responsibility** — OmniRoute `src/lib/usage/*.ts` → `internal/handlers/dashboard/` + `web/src/api/client.ts`; `open-sse/executors/` → `internal/proxy/executor/`; `src/app/` components → `web/src/`.
4. **OmniRoute is secondary.** Where it conflicts with `decolua/9router`, the §1 parity rules win; note the deliberate divergence in `CHANGELOG.md`.
5. **Provider IDs are not portable verbatim.** OmniRoute has its own catalog. Map to `nox9go` provider IDs and obey §3 (strict provider isolation — no cross-provider aliasing or model hijacking).
6. **Preserve upstream error semantics.** OmniRoute surfaces typed error classes with explicit status codes (e.g. `409 no_credit`); port that distinction rather than collapsing every upstream failure into one generic error.

---

## 2. Architecture & Codebase Mapping

Local docs first — they are the fork's own map and stay accurate:
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) and [`docs/DATABASE.md`](docs/DATABASE.md).

| Upstream path (in `decolua/9router`) | NOX9GO path | Role |
| :--- | :--- | :--- |
| `open-sse/translator/` | `internal/translator/` | Protocol converters (OpenAI ↔ Claude Messages ↔ Gemini ↔ Antigravity). |
| `open-sse/executors/` | `internal/proxy/executor/`, `internal/proxy/` | Provider callers, SSE adapters, tool-ID repair, cloaking. |
| `open-sse/providers/registry/`, `open-sse/config/providerModels.js` | `internal/providers/` | Provider catalog (`providers.go` → `KnownProviders`, 140+ entries), model catalog (`registry_models.go`), aliases (`aliases.go`), OAuth. |
| `open-sse/handlers/chatCore.js`, `src/sse/handlers/chat.js` | `internal/handlers/chat/` | Chat routing, combo fallback, fusion, resolution, quota handling. |
| `open-sse/rtk/` | `internal/tokensaver/` | Prompt compression (RTK), Caveman, Ponytail, ADHD system prompts. |
| `src/app/api/` (Next.js API routes) | `internal/handlers/dashboard/`, `internal/handlers/` | Dashboard REST & SSE APIs (connections, combos, proxypools, usage, settings, `status.go`). |
| `src/lib/db/` | `internal/db/` | SQLite layer (pure-Go `modernc.org/sqlite`). |
| `src/app/` (Next.js React 19) | `web/src/` | Dashboard UI: **Svelte 5 + Vite 8**, embedded via `web/embed.go`. |
| `cli/` (npm launcher) | `cmd/nox9go/` (gateway), `cmd/nox9go-tray/` (Windows tray) | Single-binary launcher, daemon and system tray. |
| `tests/` (Vitest) | `*_test.go`, `internal/integration/` | Go unit tests and the tagged integration suite. |
| — (fork-only) | `internal/tunnel/` | Cloudflare quick tunnel: enable/disable/status, state in the data dir. |
| — (fork-only) | `internal/app/` | Uber Fx wiring (`DatabaseModule`, `ServerModule`, `ProvideRouter`). |
| — (fork-only) | `web/src/lib/i18n/`, `web/public/i18n/literals/es.json` | Runtime i18n (en/es). |
| — (fork-only) | `internal/integration/` | Feature-level suite on the real router + real DB + fake upstreams. |

---

## 3. Provider Independence & Anti-Hardcoding Principles (STRICTLY MANDATORY)

Every provider in this repository (`antigravity`, `opencode`, `claude`, `codex`, `cline`, `freebuff`, `xai`, `gemini-cli`, `qoder`, …) is **strictly distinct, independent, and isolated**. You **MUST NEVER** merge, conflate, cross-alias, or hijack one provider into another.

### A. Strict Provider Isolation (No Cross-Hijacking)
1. **Never confuse or conflate providers**:
   - `antigravity` is **NOT** `opencode`. Separate upstream APIs, separate authentication (Google Cloud Code OAuth tokens vs OpenCode public tokens), separate model catalogs.
   - A request targeted at `ag/<model>` or `antigravity/<model>` must **always and only** reach the Antigravity executor.
   - An OpenCode model (`space-bunny-free`, `muse-spark-*`, `big-pickle`, `*-free`) belongs exclusively to `opencode` (`oc/`). It must **never** be injected into, aliased to, or redirected from Antigravity.
2. **Zero cross-provider aliasing**:
   - `ResolveProviderProxyPoolID` and connection lookups must only resolve genuine aliases (`ag` ↔ `antigravity`, `oc` ↔ `opencode`, `cline` ↔ `clinepass`). Never fall through from `antigravity` to `opencode` or vice versa.

### B. Ban on Hardcoded Model Rewrites
- **No substring-based provider switching**:
  - ❌ `if (provider == "antigravity" || provider == "ag") && strings.Contains(model, "muse-spark") { provider = "opencode" }` (strictly forbidden).
  - ❌ `if isOpenCodeModel(model) { provider = "opencode" }` (strictly forbidden).
- **Uniform provider handling**:
  - Every provider handles all of its models uniformly.
  - Routing is driven by explicit catalog registration, database model aliases, custom provider-node prefixes, or the standard `provider/model` wire syntax — **never** by ad-hoc string inspection.

### C. General vs Custom Abstractions
- **Shared / general abstractions** — if a mechanism is general across providers (streaming SSE adapters, HTTP retry with direct fallback on proxy errors, token estimation, client session resolution), encapsulate it in an agnostic, generally named package or helper:
  - ✅ `proxy.NewFallbackTransport(...)`
  - ✅ `proxy.ForwardOpenAI(...)`
  - ✅ `handlerutil.ExtractSessionID(r)`
  - ✅ `translator.ConcealFingerprintTools(...)`
- **Provider-specific custom logic** — if a provider has unique protocol requirements, custom headers, or quirky payload envelopes, build a dedicated, self-contained executor or handler file:
  - ✅ `internal/translator/antigravity.go` (Google Cloud Code assist envelope)
  - ✅ `internal/proxy/executor/freebuff.go` (multi-session & client_id cloaking)
  - ✅ `internal/proxy/executor/opencode.go` (fingerprinting & Responses API transformation)
  - ✅ `internal/handlers/media/antigravity_image.go` (image generation)
  - **Rule**: provider-specific logic stays strictly inside that provider's execution path. It must **never** leak into generic routing, sibling provider paths, or universal middleware.

### D. Dashboard & UI Parity Discipline
- **Respect registry flags (`modelsFetcher`)**:
  - The "Suggested free models" section must strictly depend on the provider's `modelsFetcher` flag in the catalog.
  - Never add `else if (providerId === '...')` blocks in `ProviderDetailView.svelte` to inject one provider's models into another.
  - "Import from /models" only for providers that genuinely support dynamic catalog discovery upstream (`cline`, `clinepass`, `qoder`, `qoder-cn`).

---

## 4. Golang Engineering & Optimization Principles

> **CORE MANDATE: Do NOT blindly transliterate JavaScript / TypeScript into Go.**
> The observable behavior and API contract must match upstream 100%; the internal implementation must exploit Go's performance, strong typing, low memory footprint and idiomatic concurrency.

### A. Performance & Zero-Allocation Streaming
- **Stream, don't buffer**:
  - Upstream JS often buffers whole response bodies. In Go, stream SSE chunks through `io.Reader`, `io.Writer`, `bufio.Reader`, `bufio.Scanner`.
  - Do not buffer entire LLM streaming responses in memory unless modification or repair is strictly required.
- **Minimize allocations**:
  - Reuse buffers with `bytes.Buffer` or `sync.Pool` in hot forwarding paths.
  - Avoid `[]byte(str)` / `string(bytes)` conversions in hot loops.
  - Use `json.RawMessage` to forward opaque passthrough segments instead of unmarshaling into `map[string]any`.
- **HTTP transport hygiene**:
  - Reuse pooled `*http.Client` instances (`MaxIdleConnsPerHost`, keep-alives, header timeouts). Never create an unconfigured `&http.Client{}` per request.

### B. Concurrency & Goroutine Safety
- **Context propagation**: always accept and propagate `context.Context` into DB queries, HTTP requests and background workers. Respect client disconnects (`r.Context()`) to abort in-flight upstream calls.
- **No goroutine leaks**: every spawned goroutine has a deterministic lifecycle tied to a context or channel exit.
- **Synchronization**: guard mutable shared state (token caches, strike counters, rate limiters, proxy pools) with unexported `sync.RWMutex` / `sync.Mutex` + `defer mu.Unlock()`. Never read/write a Go map concurrently.
- **Collapse duplicates**: use `golang.org/x/sync/singleflight` for identical concurrent external work (OAuth refresh, remote catalog sync).

### C. Type Safety vs Dynamic JSON
- **Strong typing over `map[string]any`**: typed structs for known wire protocols (OpenAI Chat Completions, Anthropic Messages, Gemini GenerateContent, Antigravity envelopes).
- Reserve `any` / `map[string]any` for genuinely dynamic, provider-specific or open-ended passthrough payloads.
- **Clean serialization**: accurate struct tags (`json:"fieldName,omitempty"`); omit zero values where the upstream spec omits them.

### D. Code Style, Modularity & Cognitive Complexity
- **Cognitive complexity**: keep SonarQube cognitive complexity ≤ 15 per function/method.
- **Guard clauses**: happy path left-aligned with early returns; no nested `if/else` ladders 3+ levels deep; branch into focused private helpers.
- **File size**: target ≤ 300 LoC; modularize anything > 400 LoC into focused files or subpackages.
- **Dependency injection**: Uber Fx (`go.uber.org/fx`) in `internal/app/`. Handlers and usecases never `nil`-check injected dependencies — fail fast at container startup.
- **Functional helpers**: prefer `github.com/samber/lo` (`lo.Map`, `lo.Filter`, `lo.Contains`, `lo.Ternary`, `lo.ToPtr`) over handwritten boilerplate loops.

### E. Error Handling
- **Wrap with `%w`**: always give the caller context — `fmt.Errorf("receiver.MethodName: %w", err)`.
- **Inspect with `errors.Is` / `errors.As`**: never `strings.Contains(err.Error(), ...)`.
- **Zero panic policy**: no `panic()` in handlers, services or proxy execution. Recover gracefully and emit proper HTTP/SSE error payloads.

### F. Dependencies & Portability

The build ships one binary per target from pure Go source. That guarantee is the only thing constraining a dependency — not its size, not how heavy it looks.

- **A new dependency is allowed.** Do not refuse one because it "looks heavy". Judge it by the two conditions below. Heavyweight is a reason to measure and state the cost, never a reason to declare the approach impossible.
- **The two conditions are mandatory, and both are cheap. Run them before proposing the dependency, not after:**
  1. **Portability** — every pair in `CROSS_TARGETS` still builds with cgo disabled. Read the list from the `Makefile` so a target added there cannot be forgotten:
     ```powershell
     # PowerShell
     $targets = (Select-String -Path Makefile -Pattern '^CROSS_TARGETS *:=').Line -replace '.*:= *',''
     foreach ($pair in $targets -split '\s+' | Where-Object { $_ }) {
       $os, $arch = $pair -split '-'
       $env:CGO_ENABLED = '0'; $env:GOOS = $os; $env:GOARCH = $arch
       go build -o "$env:TEMP\nox9go-$os-$arch" ./cmd/nox9go/ ; Remove-Item Env:GOOS,Env:GOARCH
       if ($LASTEXITCODE -ne 0) { "FAIL $pair" }
     }
     ```
     ```bash
     # bash (Git Bash / WSL / Linux)
     for pair in $(grep -E '^CROSS_TARGETS *:=' Makefile | sed 's/.*:=[[:space:]]*//'); do
       CGO_ENABLED=0 GOOS=${pair%-*} GOARCH=${pair#*-} \
         go build -o /dev/null ./cmd/nox9go/ || echo "FAIL $pair"
     done
     ```
     This needs `web/dist` to exist first (`web/embed.go` is a `//go:embed all:dist` directive), because a fresh clone fails there and not for portability. `make cross` runs the same loop plus `web-build`, so it needs Bun.
  2. **A coherent module graph** — `go mod tidy` leaves `go.mod` unchanged, and any module the repo imports is a **direct** require, not `// indirect`.
- **Never claim a library cannot be used without building it.** A build-behaviour claim costs one command; the expensive mistake is discarding a working approach and paying for the redesign later.
- **State the measured cost, not the assumed one.** When an approach has a heavier and a lighter variant, name both and their measured difference (binary size, module count). Prefer the lighter one when it meets the requirement; take the heavier one when the requirement genuinely needs it, and say which.
- **Forbidden regardless of size**:
  - Any library requiring cgo — it ends the `CGO_ENABLED=0` guarantee for every target at once.
  - Build tags or platform-specific files that break any target in `CROSS_TARGETS`. They turn a single portable binary into a per-platform build, which is the one property this pipeline exists to keep.

### G. Deployment Target: Small Hardware

The gateway is expected to serve from a **1 vCPU box with a small memory ceiling** — an operator's router, a Raspberry Pi, the cheapest VPS available. That is why §4.A–§4.F read the way they do: stream instead of buffering, bound every cache and goroutine, reuse the pooled `http.Client`. Code can be correct and still be a regression here.

- **A change that measurably raises steady RSS or per-request CPU on that box is a regression**, even when every test passes and the wire contract is unchanged. Measure the real binary on a real request path before and after, and put the number in the commit message or PR body.
- **Bound everything that grows.** A cache needs a size or a TTL; per-connection state needs a lifetime; a goroutine per request needs a reason to exist. An unbounded map keyed by a client-supplied value is a memory leak with a schedule.
- **Take the smaller mechanism when both meet the requirement** — a slice over a map, a counter over a histogram, `json.RawMessage` over `map[string]any` on a hot path. When the heavier mechanism is genuinely needed, say which requirement needs it.

---

## 5. Idiomatic Go Unit Testing Convention (`testing.T`)

All unit tests **MUST** use the standard library `testing` package. Do **NOT** introduce heavy external BDD frameworks (Ginkgo/Gomega, testify suite) to keep `go.mod` clean, compilation fast and the suite uniform across packages.

This rule is about test *style*, not about dependencies in general: it forbids adopting a BDD framework as the way tests are written, and says nothing against a new runtime dependency that satisfies §4.F. §4.F governs what may enter `go.mod`; this section governs how tests are shaped once it is.

### A. Table-Driven Tests Pattern
For functions with multiple inputs, edge cases or status transformations, always structure tests as a table:

```go
func TestResolveModelAlias(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{name: "resolves known alias", input: "cbcn", expected: "codebuddy-cn", wantErr: false},
		{name: "canonical id passes through", input: "openai", expected: "openai", wantErr: false},
		{name: "unknown alias returns error", input: "unknown-xyz", expected: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveModelAlias(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveModelAlias() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.expected {
				t.Errorf("ResolveModelAlias() = %q, want %q", got, tt.expected)
			}
		})
	}
}
```

### B. HTTP Handler Testing (`httptest`)
Use `net/http/httptest` (`NewRecorder`, `NewRequest`, `NewServer`) for endpoints, SSE streams, middleware and reverse-proxy forwarding:
- Verify status codes: `if rec.Code != http.StatusOK { t.Fatalf(...) }`.
- Verify headers (e.g. `Content-Type: text/event-stream`).
- Inspect the body or unmarshal into typed structs, not `map[string]any`.

### C. Testing Best Practices
1. **Meaningful subtest names**: `t.Run("describes behavior/condition", ...)`.
2. **Fail fast vs accumulate**:
   - `t.Fatalf()` when the failure invalidates the remaining checks (nil pointer, unexpected error, failed setup).
   - `t.Errorf()` when verifying independent fields of a struct.
3. **No network in unit tests**: use `httptest.NewServer` or mock clients for upstream providers and OAuth endpoints. Tag live tests clearly (e.g. `live_e2e_test.go` or an env guard) so the standard suite passes fully offline.
4. **Deterministic & isolated**: clean up temp DB files or use in-memory SQLite (`:memory:`).
5. **A new test must fail before the fix.** Write the RED case first, watch it fail for the right reason, then implement. A test that passes before and after the change proves nothing.

### D. Feature Integration Tests (`internal/integration`, `integration` build tag)

Unit tests call handlers directly or mount a hand-built router, so they cannot see a regression in the wiring production actually uses. The suite in `internal/integration/` closes that gap: it boots `app.ProvideRouter` (the real middleware stack and route table) on a real HTTP listener against a temporary SQLite database, with every provider call intercepted by a fake `httptest` upstream seeded through `providerConnections.data.baseUrl`. `internal/integration/bootfx/` boots the full fx graph — `DatabaseModule` and `ServerModule` included — once, in its own test binary.

1. **Run them with `make test-integration`** (or `go test -tags=integration ./internal/integration/...`). The tag keeps `go test ./...` fast. There is no CI here, so run it locally before calling a routing, auth, account-rotation or usage-accounting change done.
2. **Use the harness helpers, never raw setup.** `newProviderEnv(t)` is the common fixture: an `Env` with one DeepSeek connection aimed at a fake upstream. `newEnv(t)` is the bare gateway. Both open the database, apply the real schema bootstrap and seed a client API key; `newEnv` also pins the token savers off so upstream payloads stay comparable byte for byte. Add connections with `env.AddConnection(t, ...)` and combos with `env.AddCombo(t, ...)`.
3. **Every helper takes the running test as its first argument.** `Env` holds no `*testing.T`: capturing the parent would make a failing `t.Run` call `FailNow` on the parent from the subtest's goroutine, which the `testing` package reports as "subtest may have called FailNow on a parent test" and attributes to the wrong line.
4. **Never let a test reach the network.** A provider must be an `env.NewUpstream(t, ...)` fake, and the fake must assert what the gateway sent (`upstream.Last(t).Model(t)`, `.Header`). `AddConnection` reads the stored row back and fails when the fake URL did not persist, because an empty `data.baseUrl` falls through to the real provider URL from `providers.KnownProviders`. No real provider credential may be required for the suite to pass.
5. **Do not boot `app.DatabaseModule` outside `bootfx/`, and boot it only once there.** `db.InitGlobalDatabase` is a process-wide `sync.Once` and `fxApp.Stop()` closes that handle for good, so a second boot in the same binary would reuse a closed database and an already-cancelled shutdown context.
6. **Assert observable behavior, not wiring.** A useful case pins a contract a client depends on (the status a client sees, the model the provider receives, the row written to `usageHistory`). Testing that a route exists, or that a handler forwards to itself, proves nothing. When a subtest looks for one row in a listed collection, select it by id and fail when it is absent — a loop that only errors on a match passes vacuously when the list comes back empty.

### E. Other test targets
- `go test -race -count=1 ./...` — same as the upstream race job. `-race` needs cgo, so it needs a C toolchain on Windows; if none is installed, skip it and say so rather than failing with a confusing build error.
- `go test -count=1 -v ./internal/handlers/chat/ -run 'Live|MuseSpark'` with `NOX9GO_LIVE_TESTS=1` — live upstream tests. Opt-in, local only, and rate-limited upstream: a skip carrying the provider's body is normal, a 400/500 is not.

---

## 6. Frontend Engineering & Svelte 5 Standards (`web/`)

The dashboard is built with **Svelte 5 + Vite 8 + TypeScript + Tailwind CSS**, compiled to `web/dist` and embedded into the Go binary via `web/embed.go` (`//go:embed all:dist`).

> **Upstream parity notice**: upstream `9router` uses Next.js 16 + React 19 (`useState`, `useEffect`, JSX); this repo uses **Svelte 5**. Every upstream UI feature, view and modal is ported to idiomatic Svelte 5.

### A. Svelte 5 Runes (STRICTLY MANDATORY)
Never use legacy Svelte 3/4 syntax.

1. **Component props (`$props`)**:
   ```svelte
   <script lang="ts">
     interface Props {
       title: string
       isOpen?: boolean
       count?: number
       onClose?: () => void
     }

     let { title, isOpen = false, count = 0, onClose = () => {} }: Props = $props()
   </script>
   ```
   - Use `$bindable()` for two-way bound props: `let { activeTab = $bindable('endpoint') } = $props()`.
   - ❌ **Forbidden**: `export let title = ''`.
2. **Reactivity (`$state`, `$derived`, `$effect`)**:
   - State: `let loading = $state(false)`.
   - Derived: `let filteredItems = $derived(items.filter(...))` — never legacy `$:`.
   - Effects: `$effect(() => { ... })` — never legacy `$:` reactive statements.
3. **Snippets over slots**:
   ```svelte
   <script lang="ts">
     import type { Snippet } from 'svelte'
     let { children, footer }: { children?: Snippet; footer?: Snippet } = $props()
   </script>

   <div class="content">{@render children?.()}</div>
   {#if footer}<div class="footer">{@render footer()}</div>{/if}
   ```
   - ❌ **Forbidden**: `<slot />`, `<slot name="footer" />`.
4. **Modern event handlers**: `onclick={handleClick}`, `onkeydown={handleKeyDown}`, `onchange={handleChange}`.
   - ❌ **Forbidden**: `on:click={handleClick}`.

### B. Upstream React 19 → Svelte 5 Porting Matrix

| Upstream Next.js / React 19 | NOX9GO Svelte 5 Equivalent |
| :--- | :--- |
| `useState(initial)` | `let val = $state(initial)` |
| `useMemo(() => compute(x), [x])` | `let computed = $derived(compute(x))` |
| `useEffect(() => { ... }, [deps])` | `$effect(() => { ... })` |
| `const ref = useRef(null)` | `let el = $state<HTMLElement \| null>(null); <div bind:this={el}>` |
| `{condition && <Component />}` | `{#if condition}<Component />{/if}` |
| `{items.map(item => <Row key={item.id} />)}` | `{#each items as item (item.id)}<Row {item} />{/each}` (always key) |
| `value={val} onChange={e => setVal(e.target.value)}` | `bind:value={val}` |
| `props.children` | `{@render children?.()}` |

### C. Design Tokens & Styling (Tailwind CSS)
Use the tokens defined in `web/src/index.css` for consistent light/dark appearance:

- **Brand (Terracotta)**: `bg-brand-500`, `text-brand-500`, `bg-primary`, `text-primary`, `hover:bg-primary-hover` (`#e56a4a`).
- **Surfaces**: `bg-surface`, `bg-surface-2`, `bg-surface-3`, `bg-sidebar`.
- **Borders**: `border-border`, `border-border-subtle`.
- **Text**: `text-text-main`, `text-text-muted`, `text-text-subtle`.
- **Radii**: `rounded-[10px]`, `rounded-[14px]`, `rounded-2xl`.
- **Shadows**: `shadow-[var(--shadow-warm)]`, `shadow-[var(--shadow-elev)]`.
- **Icons**:
  - Material Symbols: `<span class="material-symbols-outlined text-[18px]">icon_name</span>`
  - Lucide Svelte: `import { IconName } from 'lucide-svelte'; <IconName size={18} />`

### D. API Integration & Routing
1. **Centralized API client**: all backend calls go through `web/src/api/client.ts` (`api.getConnections()`, `api.getCombos()`, `api.getSystemVersion()`, …). Typed interfaces live in `client.ts` or a view-specific `types.ts`.
2. **Single-page routing**: navigation tabs are managed by `web/src/lib/router.ts` (`ActiveTab`, `TAB_ROUTES`).
3. **Modals** handle `Escape` and click-outside dismissal.
4. **Never call the gateway from a component.** Go through the client so auth headers and error handling stay in one place.

### E. Svelte Type-Checking (`make vet-svelte`)

`tsconfig.app.json` includes `src`, but **`tsc` cannot parse `.svelte` files at all** — every Svelte `<script lang="ts">` block in this repo went untyped. The consequence is concrete: `bun run build`, `oxlint` and `vite build` all report success for a component that calls a function it never imported, and the failure only appears at runtime — a `ReferenceError` in the browser, after the button was clicked. That shipped in `QuotaTrackerView.svelte:300` (commit `3525284c` renamed the mint to `newResetCreditIdempotencyKey` and updated only the import) and was found by a user, not by a tool.

`svelte-check` closes the hole but reports ~92 pre-existing type errors, so it runs as a **ratchet** (`web/scripts/svelte-check-ratchet.ts`) rather than a hard gate:

1. **Unresolved identifiers fail the build outright** — `Cannot find name`, `Cannot find module`, missing exports. This is the fatal class: each one is a `ReferenceError` waiting for a user click.
2. **The total error count is pinned** in `web/scripts/svelte-check-baseline.json` and may only shrink. Every new type error fails the build.

Run it on any frontend change. When the count legitimately drops:

```bash
cd web && bun run ratchet:svelte -- --update
```

Never widen the baseline to make a failure disappear — fix the error, or land the debt reduction on its own. The `--tsconfig tsconfig.app.json` flag is mandatory: the default resolves `tsconfig.json`, which has `files: []`, and silently checks **zero** components.

### F. Client Weight Budget

The dashboard is embedded in the binary and served by the same small box, usually to a phone. Weight is a review criterion here, not a matter of taste. Note the axis: §4.F governs what may enter `go.mod`; this governs what ships to the browser.

Measured on `main` + the first-paint work, 2026-10-21 (`cd web && bun run build`, then `du -sh dist`):

- `dist/` total: **4.6 MB**
- JS bundle: 1.2 MB raw / **232 KB brotli** · CSS: 115 KB raw / **15 KB brotli** · Material Symbols woff2: **349 KB** · `dist/providers` artwork: 2.2 MB (only the views that render it)
- The `.br`/`.gz` siblings are pre-compressed build output, not extra weight on the wire — see §6.G

- **Report the numbers for a UI change**: `bun run build`, then `du -sh dist` and the largest files. A few percent of drift is noise; a larger jump needs a reason.
- **A new runtime dependency has to pay for itself.** Icon packs, chart libraries, date/utility bundles and animation frameworks are the usual offenders. State what it buys and what it adds.
- **Costs that never appear in the bundle still count**: an animation on a list that can hold hundreds of rows, a poll faster than the data changes, an unbounded client-side array, one DOM node per data point, `backdrop-filter` inside a scrolling container, and per-frame JS on a view that stays open.
- **A visual addition that costs measurably more than it gives may be refused, with the measured reason attached** — name the asset or the per-frame cost, and what the user gets for it. "It is only a small card" is not a measurement.

### G. First-Paint Budget: Origin, Fonts, Compression

§6.F is about what the bundle weighs. This is about what the browser must do to *show* it, and it exists because a dashboard that took 21 seconds to appear on a first visit was shipped and nobody measured it.

The symptom is the `Connecting to NOX9GO Localhost Gateway (:20130)...` screen hanging on the first dashboard open. **Before blaming the gateway, measure the client side.** The backend is almost never the cause: every boot endpoint answers in under 5 ms. The delay is in the bytes and round trips before the app mounts.

#### G.1 No third-party origins

**The dashboard may not load any asset from a host other than the gateway.** Not fonts, not a CDN, not a script tag, not an analytics beacon. Every such origin adds a DNS lookup, a TLS handshake and a round trip on a connection the operator did not choose.

This is not hypothetical: the dashboard pulled Inter, JetBrains Mono and Material Symbols from `fonts.googleapis.com` and `fonts.gstatic.com` until 2026-10-21. Two of those were render-blocking stylesheets. With the CDN blocked or slow, the app did not mount for **21 seconds**. With the CDN reachable, the load event for the same build varied between 3.2 s and 12.3 s across runs, while the self-hosted build stayed within 45 ms of itself.

- Fonts are vendored into `web/src/assets` and served from the gateway's own origin. `web/src/lib/self-hosted-fonts.test.ts` fails the build if a font-CDN URL comes back.
- `font-display: swap` for text fonts, so a slow font never hides content. `font-display: block` for the icon font, because an icon drawn in the fallback face is a *wrong* glyph, and a row of wrong glyphs is worse than a row of blanks.

#### G.2 Instance the icon font, do not ship it raw

The Material Symbols variable font ships every glyph the family defines on every axis, live: **3.9 MB**. The dashboard draws a few hundred of them.

`bun run vendor:fonts` (`web/scripts/vendor-fonts.ts`) writes an instanced copy to `web/src/assets/material-symbols-outlined.woff2`: the axes the stylesheet pins anyway are pinned (`wght` 400, `GRAD` 0, `opsz` 24), `FILL` keeps its full 0..1 range because the sidebar flips it, and **every glyph is kept** — 3.9 MB → **349 KB**. An icon name added to the catalog tomorrow still renders instead of showing as tofu.

- The file is committed and the generator is deterministic.
- `web/src/lib/icon-font-budget.test.ts` pins the size budget.
- **Keep every glyph.** Subsetting to the icons in use today is smaller and breaks silently the first time someone adds an icon — a blank square, and nothing in the build catches it.

#### G.3 Serve the bundle compressed

The embedded asset handler serves what it embeds. `web/scripts/precompress.ts` writes a `.br` and a `.gz` sibling next to every compressible file, and `web/embed.go` picks the best variant the client's `Accept-Encoding` allows.

- **Compress at build time, not at request time.** The gateway runs on a 1 vCPU box and already spends its CPU streaming LLM responses; the bundle never changes between releases, so per-request compression buys nothing the build-time variant did not.
- **Pre-compression runs as part of `bun run build`**, not as a separate step someone has to remember. `vite build` clears `dist`, so a build that skips it leaves the gateway embedding an uncompressed bundle — the handler still works (it falls back to the original), which is exactly why the omission stays invisible until the transfer is measured. `web/embed_compression_test.go` pins the variants' existence so the omission fails the build instead.
- Brotli wins over gzip **on a quality tie** — Chrome sends `gzip, deflate, br` with no `q` values, and header order says nothing about preference. On the current bundle: 1200 KB → 232 KB (brotli) / 306 KB (gzip).
- **`Vary: Accept-Encoding` belongs on every negotiated response**, not only the compressed one. The identity body and the compressed body are different bytes for the same URL.
- **A compressed variant must never be larger than the original.** The handler prefers the compressed variant whenever the client accepts it, so a variant that grew makes the client download *more* bytes. The compressor skips a file whose variant is not smaller.
- The response must carry a `Content-Length` — `http.ServeContent` refuses to set one on a response with a `Content-Encoding`, which leaves the response chunked.
- A missing compression variant must fall back to the original file, never fail the request.
- **Replacing a stdlib handler means auditing what it did for you.** Swapping `http.FileServer` for a plain write silently dropped range support: a client asking for part of a file got the whole file with a 200, and one asking past the end got a 200 instead of a 416. The body goes through `http.ServeContent`, handed the variant's own size so a range resolves against the bytes on the wire.
- **Never write a stub over a built file.** The lint recipe (`mkdir -p web/dist && printf '<!doctype html>' > web/dist/index.html`) exists because `//go:embed` needs *something* to embed. Run it only when there is no build — overwriting a built `index.html` leaves its `.br` variant describing the old document, and the gateway prefers that variant.

#### G.4 Measure the first paint, not just the build

`bun run build` and `du -sh dist` say nothing about what the browser experiences. Measure the open in a real browser against a running gateway, with a **fresh context per run** (no service worker, no HTTP cache, no storage) — otherwise the second run measures the cache, not the fix.

Report three numbers: **bytes transferred**, **request count**, **load event**. The spread across runs matters more than the median — the CDN-backed build varied between 3.2 s and 12.3 s, while the self-hosted build stayed within 45 ms of itself.

#### G.5 The boot screen must not be the only thing the user sees

The `Connecting to NOX9GO Localhost Gateway (:20130)...` spinner is a **full-screen gate**: it replaces the whole dashboard until every boot request resolves.

- It is the first thing an operator sees when something is wrong, so it must not become a *slower* way to find out what is wrong.
- It is a symptom, not a diagnosis. The message names the gateway, so an operator blames the gateway when the cause was the browser's download. Do not "fix" a slow dashboard by making the spinner more informative; measure where the time goes first.

The screen stays as long as the boot requests take, and those requests are `Promise.all`-ed, so it is bounded by the slowest one — but only once the *bundle* has loaded. The bundle is the long pole.

### H. Runtime i18n (en / es)

Translations are applied **at runtime**, not baked at build time.

- `web/src/lib/i18n/config.ts` — locales (`en`, `es`), default locale, cookie name (`locale`), locale names.
- `web/src/lib/i18n/runtime.ts` — `translate(text)`, `setLocale(next)`, `reloadTranslations()`, `initRuntimeI18n()`.
- `web/public/i18n/literals/es.json` — the English→Spanish literal map, loaded over HTTP at runtime.

Rules:
- **New user-facing strings must be added to `es.json`.** The operator uses the dashboard in Spanish; a new English-only string is an unfinished feature.
- The map is keyed by the **exact English literal** — the key must match the string in the component character for character.
- Strings that are dynamic (model names, provider names, error bodies from upstream) are intentionally not translated.
- `bun run build` does not need to re-run for a translation change alone, but the **binary still must be rebuilt**, because `web/public` is embedded too.

### I. The SPA is embedded — the rebuild chain is not optional

Any change under `web/` requires, in order:

```bash
cd web && bun run build     # 1. rebuild the SPA (also pre-compresses dist)
cd ..                        # 2. rebuild the Go binary (embeds web/dist)
# 3. restart the service (the running .exe cannot be overwritten)
```

Skipping step 2 or 3 leaves the old dashboard served, no matter what the browser cache does. `web/scripts/web-build.ts` fingerprints the frontend inputs and only rebuilds when they change; `FORCE=1` forces it.

---

## 7. Daily Workflow on Windows (build, run, ship)

### 7.1 Service management

```powershell
# Stop (a running .exe blocks its own overwrite)
Stop-Process -Name "nox9go","nox9go-tray" -Force -ErrorAction SilentlyContinue

# Start gateway + tray + dashboard, no console flash
powershell -ExecutionPolicy Bypass -File .\scripts\start-nox9go.ps1
# (or the NOX9GO.lnk shortcut on the Desktop)
```

`scripts/start-nox9go.ps1` reads `nox9go status --json` (not the human prose), starts the gateway only when `running` is false, polls the `dashboard` field of that same JSON for readiness, replaces any stale tray, and opens that URL. The address comes from the CLI, so `PORT`, `.env` and profile flags are honoured.

### 7.2 Build

```powershell
# Frontend (required after any change under web/)
cd web; bun run build; cd ..

# Gateway — version + tracked-upstream baselines are embedded through ldflags
go build -ldflags "-X nox9go/proxy/internal/updater.CurrentVersion=1.0.2" -o nox9go.exe ./cmd/nox9go/

# Tray — -H=windowsgui is mandatory, otherwise a console window flashes
go build -ldflags "-H=windowsgui" -o nox9go-tray.exe ./cmd/nox9go-tray/
```

The version string in the binary is `updater.CurrentVersion`. A plain `go build` leaves the source default;
`VERSION` is the value to embed; `make build` reads it from the file. If your Make predates 3.82, pass it explicitly with `-ldflags "-X nox9go/proxy/internal/updater.CurrentVersion=$(cat VERSION)"`.

The top-bar update alert watches this app's own release channel only: it compares `CurrentVersion` against `version.json` on `main` (falling back to GitHub Releases for `Pacoaldev/Nox9Go`) and offers to install. `version.json` names one asset and one digest per platform; the updater refuses a platform the manifest does not list instead of falling back to another one's binary.

### 7.3 Verify a change in the live service

```powershell
Invoke-WebRequest -Uri http://127.0.0.1:20130/api/health -UseBasicParsing | Select-Object StatusCode
# 200 + {"ok":true} == the gateway is up
```

For authenticated dashboard endpoints, send the CLI token header `x-9r-cli-token` (see §0.2). A `401` without it and a `404` on an unknown route are the quick proof that routing and auth are wired.

### 7.4 Changelog & version (personal fork)

- This is a **public** repository. Edit [`CHANGELOG.md`](CHANGELOG.md) directly under the current version   heading (`## [1.0.2]`). Do **not** reintroduce a `.changes/` fragment workflow, `make changelog-merge`, or `scripts/bump-version.sh` — those were upstream's multi-contributor release machinery and were removed.
- Bump `VERSION` (and `version.json` `latestVersion`) by hand when cutting a new personal version, then rebuild with the ldflags above.
- `GET /api/changelog` serves that file.
- **Publish the release so the dashboard's update button can install it.** The gateway reads the manifest at `https://raw.githubusercontent.com/Pacoaldev/Nox9Go/main/version.json` and, when that manifest carries no `sha256`, falls back to the GitHub Releases API. Both channels are anonymous, so the repository has to be public. The installer refuses to replace the running binary without a digest (`internal/updater/updater.go`), so a release needs the binary **and** a `SHA256SUMS.txt` listing it, and `version.json` has to carry the asset's `downloadUrl` and `sha256` for the manifest channel to answer on its own:

  ```powershell
  # 1. release build — the asset name must carry OS and arch, the updater selects by name
  go build -ldflags "-X nox9go/proxy/internal/updater.CurrentVersion=1.0.3 ..." -o nox9go-windows-amd64.exe ./cmd/nox9go/
  go build -ldflags "-H=windowsgui" -o nox9go-tray.exe ./cmd/nox9go-tray/

  # 2. digest, in sha256sum format
  Get-ChildItem nox9go*.exe | Get-FileHash -Algorithm SHA256 |
    ForEach-Object { "$($_.Hash.ToLower())  $(Split-Path $_.Path -Leaf)" } |
    Set-Content SHA256SUMS.txt -Encoding ascii

  # 3. the release (reuses the tag if it already exists)
  gh release create v1.0.3 nox9go-windows-amd64.exe nox9go-tray.exe SHA256SUMS.txt --title "v1.0.3 — ..." --notes-file notes.md

  # 4. the manifest: downloadUrl + sha256 of the asset, committed to main
  ```

  The tray is never picked as the gateway: `matchReleaseAsset` selects the asset whose name carries the OS and the arch, and `nox9go-tray.exe` carries neither. `raw.githubusercontent.com` caches for five minutes (`Cache-Control: max-age=300`), so a manifest change is not visible to the gateway immediately.

### 7.5 Git policy

- **Everything lands on `main`.** There is no `dev` branch and no PR flow; changes are committed to `main` directly.
- **Never commit unless asked.** When asked, inspect `git status` / `git diff`, stage only the intended files, write a Conventional Commit message, no AI attribution, no `--no-verify`. Never blanket `git add -A` when unrelated or sensitive files are present.
- **Tags**: `v1.0.0`, `v1.0.1` and `v1.0.2`. The 80 upstream tags were purged on purpose.

### 7.6 Syncing a feature from upstream

1. **Read upstream** over the network (§1) — the counterpart file and its upstream test.
2. **Locate the NOX9GO counterpart** using the §2 mapping table and `docs/ARCHITECTURE.md`.
3. **Design for Go and Svelte 5** — streaming buffers, strong types, mutexes, singleflight on the backend; `$state` / `$derived` / `$effect` on the frontend.
4. **Implement & test** — `internal/...` + `*_test.go` (table-driven, `testing.T`); `web/src/...` + `bun run build` + `make vet-svelte`.
5. **Verify** — the gate list in §8.
6. **Record the upstream reference** in `CHANGELOG.md`.

---

## 8. Verification Gates (local — there is no CI)

Run what the change touches. Quote the observed result.

```powershell
# Unit tests
go test ./...
go test ./internal/providers/... -v

# Feature integration suite (real router, real DB, fake upstreams)
go test -tags=integration ./internal/integration/...
# or: make test-integration   (adds -race, needs a C toolchain on Windows)

# Race detector (needs cgo → a C compiler; skip and say so if absent)
go test -race -count=1 ./...

# Static analysis
go vet ./...
go vet -tags=integration ./internal/integration/...

# Svelte type-check ratchet (blocks unresolved identifiers, pins type debt)
make vet-svelte

# Linters (not installed as binaries here; run them on demand)
bunx oxlint --deny-warnings --cwd web
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

# Embedded asset contract (compression variants, font origin, icon budget)
go test ./web/...

# Frontend weight budget after a UI change
cd web; bun run build; cd ..
Get-ChildItem web/dist -Recurse -File | Sort-Object Length -Descending | Select-Object -First 8 Name,Length

# Portability gate — required after any go.mod change (§4.F)
# (see the PowerShell and bash loops in §4.F)

# Cross-compile check
make cross
```

`golangci-lint` needs `web/dist` to exist or it reports `pattern dist/*: no matching files found` as a typecheck failure. Write the stub **only when there is no build**:
```powershell
if (-not (Test-Path web/dist/index.html)) { New-Item -ItemType Directory -Force web/dist | Out-Null; Set-Content web/dist/index.html '<!doctype html>' }
```

---

## 9. Working Agreement for AI Agents

- **Read before you write.** `AGENTS.md` (this file), then `docs/ARCHITECTURE.md` / `docs/DATABASE.md` when the change touches structure. Windows runbook.
- **Map with the real code.** Use the code graph / `codegraph_explore` before grepping around a structural question.
- **Delegate the exploration, keep the decision.** A read-only explorer maps the area and returns `path:line` evidence; the parent decides and writes.
- **One writer per area.** Parallel writers only with disjoint edit surfaces; the parent owns git.
- **Report with evidence.** Every claim about behavior cites a command that was run and its output.
- **Leave the tree clean.** No scratch files, no stray `dist` stubs, no leftover build artifacts outside `.gitignore`.

---

## 10. Quick Reference

| I want to… | Command |
| :--- | :--- |
| Run the unit tests | `go test ./...` |
| Run the integration suite | `go test -tags=integration ./internal/integration/...` |
| Build the dashboard SPA | `cd web; bun run build` |
| Build the gateway | `go build -ldflags "-X nox9go/proxy/internal/updater.CurrentVersion=1.0.2" -o nox9go.exe ./cmd/nox9go/` |
| Build the tray | `go build -ldflags "-H=windowsgui" -o nox9go-tray.exe ./cmd/nox9go-tray/` |
| Start everything | `powershell -ExecutionPolicy Bypass -File .\scripts\start-nox9go.ps1` |
| Stop everything | `Stop-Process -Name "nox9go","nox9go-tray" -Force -ErrorAction SilentlyContinue` |
| Check the service | `Invoke-WebRequest http://127.0.0.1:20130/api/health -UseBasicParsing` |
| Type-check Svelte | `make vet-svelte` |
| Cross-compile | `make cross` |
| See what changed | `git status` · `git diff` · `git log --oneline -10` |
| Commit and push | `C&P` (chat shorthand) or explicit request |
| Regenerate the icon font | `cd web; bun run vendor:fonts` |
