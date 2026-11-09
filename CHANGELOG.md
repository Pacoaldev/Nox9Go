# Changelog

## [1.1.0] - 2026-11-09

### Routing & Executors

- **Qoder identity unmasking.** Injected base AI model identity unmasking directive into the first user turn when requests arrive without a system prompt, preventing Qoder's underlying models from assuming the Qoder persona (`internal/proxy/executor/qoder_request.go`).
- **Antigravity CLI fingerprint 1.5.1 and session scoping.** Bumped `AntigravityCLIVersion` to `1.5.1` and `AntigravityCLIUserAgent` to `cl=1014269460`. Fixed metadata platform enum to `5` (`WINDOWS_AMD64`) matching the Windows amd64 identity in User-Agent. Scoped request IDs and trajectories by session and connection ID to prevent cross-account UUID collisions in pooled setups. Unified Antigravity User-Agents across chat, quota, and probe endpoints. Added `Accept` headers (`text/event-stream` / `application/json`).
- **MiniMax Code Claude-to-OpenAI reply translation.** Routed `req.UpstreamClaude` responses through `handleClaudeMessagesStream`/`handleClaudeMessagesNonStream` when callers use Chat Completions format, ensuring proper `chat.completion.chunk` and `finish_reason: "stop"` emission. Defaulted `X-Mavis-Agent-Id` to `main`.
- **Cloudflare AI image endpoint routing and FLUX.2 multipart support.** Dedicated image lane routing `cloudflare-ai`/`cf` image generation requests to `/accounts/<accountId>/ai/run/<model>`. Handled FLUX.2 models (`flux-2-dev`, `-klein-4b`, `-klein-9b`) as `multipart/form-data`, stripped dimensions for `flux-1-schnell`, and resolved reference images through SSRF guard to OpenAI image response envelopes.
- **GitHub Copilot native `/v1/messages` for Claude models.** Routed Claude models (`claude-*`) on `github` directly to Copilot's `/v1/messages` endpoint using Claude Messages format, falling back to OpenAI format with context-length-exceeded normalization for all other models.
- **Groq reasoning effort clamping and strict reasoning parity.** Clamped `reasoning_effort` on Groq `gpt-oss` models (`gpt-oss-120b`, `gpt-oss-20b`) to valid enum values (`low`, `medium`, `high`) and pinned `reasoning_format="parsed"` for structured JSON modes. Narrowed `groq/gpt-oss` thinking levels in the capability table.
- **Per-request Token Saver bypass.** Added support for `X-9Router-Token-Saver: false|0|off|bypass` request header to skip the prompt compression (RTK / token-saver) pipeline per request across chat completions, messages, and responses handlers.

### Providers & Capacity

- **Seven free-tier LLM providers from `freellmapi`.** Registered `iteracompute` (`itc`, 30M tokens/month), `ainative` (`ain`, ~10M tokens/month), `navyai` (`navy`, 150K tokens/day with explicit `User-Agent`), `anyapi` (`any`, 100K tokens/day on `:free` models), `ovhcloud` (`ovh`, keyless `NoAuth: true` at 2 RPM/IP or 400 RPM with Bearer token), `sealion` (`sl`, AI Singapore 10 RPM), and `longcat` (`lc`, Meituan daily free quota) across Go provider/capability/executor registries and the Svelte dashboard catalog with live `/v1/models` discovery.
- **Integrated providers get the Capacity limits editor, with an auto-learning observation base.** The "Capacity limits" panel — Requests/minute, Prompt tokens/minute, Concurrent requests, Queue wait, Lifetime token quota, Contract ends — existed only for custom provider nodes, whose policy lives on the `providerNodes` row. A catalog provider has no such row, so it had no editor at all even when it returns hard limits (e.g. BazaarLink's "daily limit of 60 free-model requests"). `internal/db/provider_capacity.go` adds two Go-only tables: `providerCapacity` (the configured policy, keyed by provider id) and `providerObservations` (the capacity facts the provider reports at runtime). `internal/handlers/chat/provider_capacity.go` routes the enforcement read — `readProviderPolicy` — to `providerCapacity` for a catalog id and to `providerNodes` for a custom node (the two id spaces cannot collide: a node id is always `<literal>-<suffix>`), so a configured integrated provider is bounded by the existing provider limiter exactly like a node, and an unconfigured one stays unlimited. Every retryable upstream failure is reduced to a fact by `providers.ClassifyObservation` (`internal/providers/observation.go`): a signal (`rate_limit`, `quota_exceeded`, `capacity`, `timeout`, `auth`, `server_error`), a numeric limit and unit parsed from `x-ratelimit-*` headers or the error body (`limit of 60 ... requests`), and a bounded evidence snippet — request-shape errors (model fit, malformed request) are excluded because they say nothing about capacity. Two endpoints back a new `ProviderCapacityPanel.svelte`, embedded in `ProviderDetailView` for catalog providers: `GET /api/providers/{id}/capacity` returns the policy, the learned facts and prefill suggestions, and `PUT` merges an edit exactly like the node editor. Recording is fail-open, and applying an observed value only prefills the editor — nothing is enforced until the operator saves.

- **Capacity limits gain a fixed daily request budget (`Requests / day`).** A provider that publishes "60 requests per day, resets at 00:00 UTC" (BazaarLink's free tier, for one) could not be described by the existing limits: RPM is a sliding window that refills continuously, and TPM/concurrency are unrelated axes, so the only way to honour a daily allowance was to leave it unset and let the upstream refuse each request past the cap. `middleware.Limits` now carries `DailyRequests`, a fixed-window counter that `AcquireProvider` charges first and that resets on the UTC day boundary (`internal/middleware/ratelimit.go`, `dailyWindow`). The budget is not queueable — it does not refill while a request waits — so an exhausted day refuses immediately with axis `rpd` (429, `provider_daily_limit_exhausted`, `Retry-After` until the next UTC midnight) and the failover moves to the next candidate; a reservation is refunded when the attempt never left (a TPM refusal or a queue timeout). The field is stored on both `providerCapacity` (integrated providers) and `providerNodes` (custom nodes) and exposed in both editors and both responses, with `dailyUsedWindow` reporting today's spend. `providers.ClassifyObservation` upgrades a "daily ... requests" figure to unit `rpd` so the observation base prefills this field rather than a per-minute one.
- **Saving the limits editor no longer crashes on an empty number field.** Svelte binds a cleared `<input type="number">` to `null`, and the shared `countOf` conversion called `.trim()` on it, so any save that left a count field untouched died with "Cannot read properties of null (reading 'trim')" before the request left the browser. `web/src/lib/node-limits.ts` now reads a `null`/`undefined` count as unlimited (0), and the draft type and the shared `Input` accept `null` so the shape matches what the binding actually produces.

## [1.0.9] - 2026-11-07

### Dashboard

- **API Key Providers list starts expanded and is collapsible.** The "API Key Providers" section in Providers & Endpoints now renders all providers on first load instead of showing only the first 20 behind a "Show all X providers" button. A toggle button — "Show fewer providers" when expanded, "Show all X providers" when collapsed — appears at the bottom of the section whenever the total exceeds the 20-provider threshold and no search or status filter is active. Searching or filtering auto-expands the list as before.

- **Quota tracker now shows real quotas for 10 more providers.** GitHub Copilot, GLM, GLM CN, Kimi, MiniMax, MiniMax CN, OpenCode Go, CodeBuddy CN, Xiaomi MiMo and Zed were listed in the tracker with no quota fetcher, so every connection read "Account active. No quota limits tracked." Ported their live usage handlers from upstream `open-sse/services/usage/*.js` in `internal/handlers/dashboard/usage_extra_providers.go` and wired them into `fetchProviderUsage`:
  - **GitHub Copilot** (`github`): `GET copilot_internal/user`, paid `quota_snapshots` and free `monthly_quotas`/`limited_user_quotas` formats (chat, completions, premium interactions).
  - **GLM / GLM CN** (`glm`, `glm-cn`): `monitor/usage/quota/limit`, `TOKENS_LIMIT`/`CREDIT_LIMIT` rows labelled by unit (session, weekly).
  - **Kimi** (`kimi`): `coding/v1/usages`, dual auth (`x-api-key` or OAuth Bearer + `X-Msh-*`), weekly + rate-limit rows and plan level.
  - **MiniMax / MiniMax CN** (`minimax`, `minimax-cn`): `coding_plan/remains` (with fallback hosts), 5h/7d windows from count or percent-only M-series buckets.
  - **OpenCode Go** (`opencode-go`): `zen/go/v1/usage`, rolling/weekly/monthly percent rows.
  - **CodeBuddy CN** (`codebuddy-cn`): Tencent billing meter, refill cadence + bonus packs.
  - **Xiaomi MiMo** (`xiaomi-mimo`): `aistudio` weekly percent.
  - **Zed** (`zed`): `cloud.zed.dev/client/users/me`, edit-predictions and hosted-model-request rows with plan label.
- Providers that still expose no metered endpoint keep the honest "no quota limits tracked" state; no percentages are fabricated.
- **The model-health loop is now configurable from the dashboard.** The loop itself already existed (`internal/modelhealth`): it probes the custom models an operator configured, counts consecutive failures and switches a dead model off so it stops being routed to. But nothing ever wrote the settings key it reads — `modelHealth` was read-only — so the feature was unreachable without editing the database by hand. A new "Model Health Loop" card in System Settings backs that key with an enable toggle, a check interval, a failure threshold and an auto-enable toggle. `web/src/lib/modelHealth.ts` mirrors the server's clamp bounds (5–1440 minutes, 2–100 consecutive failures) so the form never shows a value the server would silently rewrite on the next read, and reads an unreadable row as off, the way `ParseConfig` does. The loop re-reads the key every cycle, so switching it on takes effect without a restart.

### Routing

- **Kiro tool-call IDs use UUIDs.** `OpenAIToKiro` maps non-UUID tool-call IDs to per-request UUIDs and reuses that mapping for matching tool results; valid UUIDs remain unchanged. Resolves upstream `400 REQUEST_BODY_INVALID` ("Invalid tool use format.") seen on tool-assisted turns (e.g. `kr/glm-5`).
- **Groq tool history is flattened for model templates.** Requests convert assistant tool calls and tool results into prose before forwarding, avoiding upstream minijinja template failures ("Unexpected message role") when using models that reject the `tool` role (e.g. `groq/qwen/qwen3.8-27b`). Native tool structure is omitted; conversational context is preserved as plain text.

- **Kiro profileArn fallback.** Non-API-key Kiro connections (social, SSO, builder-id, idc) require `x-amzn-codewhisperer-profile-arn` and a top-level `profileArn` in request payloads. `ResolveKiroProfileARN` now supplies the proper fallback profile ARN, and `refreshKiroSocial` and `HandleKiroAutoImport` preserve it in connection data to avoid upstream HTTP 400 `profileArn is required for this request`.
- **GitHub Copilot OAuth token refresh and error handling.** Added `internal/proxy/oauth/github.go` exchanging GitHub OAuth access tokens at `https://api.github.com/copilot_internal/v2/token` for fresh short-lived Copilot bearer tokens (`tid=...`). Added `github`/`gh` to `strictReasoningMessageProviders` to strip unsupported reasoning fields. Extended `ModelFitErrorPatterns` with `model_max_prompt_tokens_exceeded` and prompt token limit patterns so oversized prompts trigger model fallback instead of aborting the request.
- **Cloudflare AI schema sanitization and message content flattening.** `SanitizeOpenAITools` now cleans `response_format.json_schema.schema` via `cleanResponseFormatSchema`, recursively stripping unsupported grammar keys (`propertyNames`, `$schema`, `$comment`) while preserving `additionalProperties` for strict JSON schema mode. Chat completions for `cloudflare-ai` flatten multipart text content arrays to avoid upstream schema validation errors.
- **Combo non-retryable 400 loop termination.** `handleComboFallback` and `handleMessagesComboFallback` now immediately abort retry loops on non-retryable HTTP 400s rather than repeating 10 identical attempts on the same connection. Probe requests now honor `maxConsecutive429PerTurn` to prevent hammering multiple accounts when a provider rate-limits.
- **Groq `max_tokens` over the model cap no longer fails the request.** Groq rejects a `max_tokens` above the model's output limit with a 400 that names the limit ("`max_tokens` must be less than or equal to `16384`"). The gateway forwarded the body verbatim, so every request asking for more — including one whose value came from a client-config placeholder — died at the first attempt. `executor.ForwardOpenAI` now parses the cap the upstream states (`translator.MaxTokensLimitFromError`), clamps `max_tokens`/`max_completion_tokens` to it (`translator.ClampMaxTokens`) and retries once; if the retry fails, its error is returned instead of the stale pre-clamp 400. The upstream's own number is the source of truth: the model catalog describes the model, not each gateway's per-model cap. Any other 400 surfaces unchanged.
- **The Pi client config no longer invents a 64000 output cap.** `internal/clientconfig/pi.go` wrote `contextWindow: 200000, maxTokens: 64000` for every model it registered, which is what made a Pi client request a `max_tokens` Groq rejects. It now derives both numbers from the capability catalog (`providers.GetCapabilitiesDetailForModel`) and omits a field the catalog cannot answer instead of guessing.
- **Gemini's OpenAI-compatible endpoint gets its thought signature backfilled.** Gemini 3 refuses a current-turn `functionCall` with no `thought_signature` (400 INVALID_ARGUMENT "Function call is missing a thought_signature in functionCall parts"). Only the native/Antigravity path ran that fixup, so the `gemini` provider — which talks to `generativelanguage.googleapis.com/v1beta/openai/chat/completions` — reached upstream unsigned whenever a client dropped the field or a combo rebuilt the history. `translator.BackfillOpenAIToolCallSignatures` now injects `extra_content.google.thought_signature` into assistant `tool_calls` that lack one, using the cached signature for that id (session-scoped) or `DefaultThinkingSignature`; `translator.HarvestOpenAIToolCallSignatures` stores the signatures Gemini returns so later turns replay the real one. Applied only for `gemini-openai` providers.
- **A run of consecutive 429s now parks the provider+model pair, not just one walk.** `maxConsecutive429PerTurn` already stopped the failover walk after five 429s across different credentials, but it bounded one turn and nothing else: the next turn dialled the accounts the first one never reached and earned the same answer. A free-tier pool of many keys was therefore spent in a couple of minutes — a combo member like `mistral/mistral-medium-3-5` burned 72 of 78 connections into cooldown while the operator was not even using that provider, and the dashboard read "rate limited" on the whole pool. A single 429 must still leave the pair routable (one 429 names a per-account or per-IP limit the operator's other keys may serve, which is why `providerModelQuotaMarkers` excludes "rate limit"), but five different credentials answering 429 for the same model inside one turn is a fact about the pair. `ChatHandler.recordProviderModelRunLockout` (`internal/handlers/chat/provider_model_lock.go`) parks it through `LockProviderModel` with reason `rate_limit_cascade`, called from `handleAccountFallback` and from both combo paths (`handleComboFallback`, `handleMessagesComboFallback`). The window is the failing attempt's own cooldown — an announced `Retry-After` honoured exactly — floored at 30s so a two-second classifier base cannot put the pair back inside the burst that answered all five. The existing pair check then skips the model whole on the next request, and any account that serves it again clears the lock. Covered by `TestHandleAccountFallback_Consecutive429ParksProviderModel`, which asserts the pair is parked and that the follow-up request makes zero upstream calls.

## [1.0.8] - 2026-11-06

Security fix release: two dependency vulnerabilities closed and a mandatory pre-release gate added so a vulnerable release is never published again.

### Security

- **GO-2026-6888 (brotli).** Bumped `github.com/andybalholm/brotli` from `v1.2.0` to `v1.2.7`. Excessive memory allocation when decompressing crafted brotli streams; reachable from `internal/proxy/stream_peek.go` (brReader.Read) and the windsurf executor. Fixed by upgrading the direct dependency.
- **GO-2026-6865 (klauspost/compress).** Bumped `github.com/klauspost/compress` from `v1.19.1` to `v1.20.2`. Panic on two-block frames (fse.Decompress / huff0.ReadTable) reachable from `internal/proxy/stream_peek.go` (zstdReader.Read). Fixed by upgrading the direct dependency.
- **Release gate formalised.** `AGENTS.md` §7.4 now carries a mandatory public release checklist: `govulncheck` (0 vulns in your code), `go test ./...`, `go vet ./...`, portability (`CGO_ENABLED=0` for every `CROSS_TARGETS` entry when `go.mod` changed), frontend gates when `web/` changed, rebuild with the new `VERSION` embedded, generate and verify `version.json`/`SHA256SUMS.txt`, then tag/push/release and post-release sanity. A release is never published from stale binaries and never skips gates "because it's a small fix".

### Database

- **Static analysis and row iteration cleanup (`sqlrowserr`).** Verified `rows.Err()` across database querying loops in `internal/db/compression_analytics.go`, `internal/db/proxyPools.go`, and test suites. Removed unused test parameter in `usage_retention_test.go`.

## [1.0.7] - 2026-11-06

The failover walk no longer spends a whole account pool on one rate limit, and a
Mistral account stays out of rotation long enough for its burst limit to recover.

### Providers
- **Requesty AI** (id `requesty`, alias `rq`). OpenAI-compatible router at `router.requesty.ai/v1`. Bearer keys; EU traffic can point `providerSpecificData.baseUrl` at `https://router.eu.requesty.ai/v1` to stay in-region. Its live catalogue answers **815** ids unauthenticated, so the static catalog is a curated fallback (`nvidia/muse-glimmer-30b`, `openai/gpt-6.1-sol`, `anthropic/claude-opus-5-5`, ...) and the full list comes from Import/Sync. Ids are vendor-prefixed (`nvidia/...`, `anthropic/...`), so capabilities are declared per id straight from the live payload. The dashboard also wires a `requesty-free` suggested-models filter (13 zero-priced models, top-level `input_price`/`output_price`).

- **AMD Radeon Cloud** (id `amd-radeon`, alias `amd`). OpenAI-compatible Token Factory at `developer.amd.com.cn/radeon/api/v1` (China site). Bearer keys look like `rc-…`; alias is `amd` because `rc` already belongs to RunCrate. Static chat catalog covers the documented wire ids (`DeepSeek-V4-Flash`, `Qwen3.8-27B`, … — card titles like `DeepSeek-V4-Flash-0731` are not accepted upstream). Live `GET /v1/models` is wired for Import/Sync. OCR (`MinerU2.5-Pro`), TTS (`OmniVoice`) and systemone (`Clef`/`d3`) endpoints are not registered in this pass.

### Routing and quota

- **Gemini transport timeout failover.** Classify transient transport faults
  (`http2: timeout awaiting response headers`, `EOF`, `connection reset by peer`) as
  retryable HTTP 502 `UpstreamFailure` in `ForwardGemini` so timeouts trigger combo
  and account failover rather than aborting the turn (`luqman-v1/9router-go#922`).
- **Tool call loop and anomaly detection.** Ported deterministic loop detector in
  `internal/detection` analyzing trailing tool calls for stuck identical calls, cyclic
  patterns, and repeated errors, logging findings to request anomalies (`luqman-v1/9router-go#921`).
- **Provider circuit breaker.** Ported admission control from `luqman-v1/9router-go#858`.
  After 5 consecutive provider-level 5xx failures, outbound traffic to that provider
  trips open for 30s with exponential backoff on repeated probe failures. It rejects
  with HTTP 503 `provider_circuit_open` so combo candidates immediately advance to the
  next provider without penalizing account cooldowns.
- **Model health background scheduler.** Ported opt-in health loop from `luqman-v1/9router-go#921`.
  Monitors imported custom models when configured via `modelHealth` in settings.
  Disables models in `disabledModels` after repeated consecutive probe failures and
  can automatically re-enable them upon recovery.
- **A run of 429s stops the failover walk.** The walk dialled every remaining
  account after a 429, so one client request became one upstream call per
  account and parked each of them on its own cooldown for a limit that answered
  all of them. Five consecutive 429s in a single turn now end the walk and the
  upstream's own response is returned, which is what carries the `Retry-After`
  the client needs. With 45 independently keyed Mistral accounts that turns 45
  complaints per request into at most 5. Any other verdict resets the run, so a
  single failing account is still worth walking past.
- **A Mistral 429 with no announced window waits 10 seconds, not 2.** The
  classifier's two-second base put the account back in rotation before the 1 RPS
  burst that exhausted it had drained, so the same account kept absorbing the
  next request and earning another 429. The floor applies only when the upstream
  names no wait of its own; an announced `Retry-After` is still honoured exactly,
  and every other provider keeps the classifier's own reading.

### Dashboard

- **Periodic provider catalogue sweep.** A background loop re-reads the upstream
  catalogue of every provider that publishes one (`modelSweep` settings key,
  default on, `intervalMin` 720, clamped 15..10080) and records per provider what
  the catalogue listed, how many recorded deprecations it disproved, and which of
  the operator's configured models (custom models plus combo members) it no
  longer lists. The provider detail view shows the last sweep with a `Check now`
  action, and `GET`/`POST /api/models/sweep` expose the same state. A model the
  catalogue omits is reported as *no longer listed* and never marked deprecated:
  catalogue absence stays weak evidence, and only an explicit upstream 410 files
  a deprecation. Providers without a queryable catalogue are reported as skipped.
  `POST /api/models/sync` now runs the same sweep, so there is one implementation
  of the catalogue pass. Its counters are now per provider rather than per
  connection, and a catalogue read that listed no models counts as synced
  instead of being dropped silently.

### Providers

- **xKiro provider gateway.** Added xKiro (`xkiro`, alias `xk`) OpenAI-compatible gateway
  (`https://api.xkiro.com`) with 106 cataloged models across LLM, image generation, and TTS,
  live `/v1/models` discovery, per-model pricing and capabilities, and `/v1/usage` key
  validation and quota tracking.
- **Live catalog tool capabilities.** Added explicit `tools` capability reporting for live
  catalog models, including Zed's `supports_tools` signal and Kiro's agentic variants.
- **HCNSec provider gateway.** Added HCNSec (`hcnsec`, alias `hc`) OpenAI-compatible gateway
  (`https://api.hcnsec.cn`) relaying GLM, DeepSeek, Qwen, Step, MiMo, MiniCPM, and SenseNova
  models with speech, embeddings, and dynamic catalog discovery (`luqman-v1/9router-go#854`).

### Wire protocols and translation

- **Groq rejects unsupported prompt cache metadata.** Remove top-level `prompt_cache_key`
  before forwarding OpenAI-compatible requests to Groq; retain it for providers such as
  CodeBuddy that use it for conversation identity.
- **Gemini embeddings dimension mapping.** Ported `dimensions` forwarding to Gemini's
  `outputDimensionality` (upstream `decolua/9router#1366`, `luqman-v1/9router-go#895`).
- **Tool schema regex lookaround stripping.** Widened regex pattern sanitization to
  strip lookarounds (`(?=...)`, `(?<!...)`, etc.) across all OpenAI-compatible targets
  instead of only OpenRouter (`luqman-v1/9router-go#886`).
- **SSE delta reasoning recognition.** Recognized `delta.reasoning` chunks alongside
  `delta.reasoning_content` in SSE streams and empty success probes (`luqman-v1/9router-go#938`).

### Repository

- **The session review report is out of the tree.** `docs/report.md` was a
  point-in-time AI code review (dated 2026-10-31, written against v1.0.4) whose
  findings were superseded by the changelog; it also quoted the MiniMax Code
  signing constant as if it were a private key. It is removed, gitignored like
  its sibling session docs, and the constant is now what it actually is: an
  allowlisted entry in `.gitleaks.toml` naming its upstream origin (public in
  `decolua/9router`, shipped inside MiniMax's own web bundle), with the runtime
  fallback in `usage_minimax_code.go` documented as public knowledge rather
  than a secret.

## [1.0.6] - 2026-11-02

Token Saver upstream updates and audit across RTK, Headroom, Caveman, and ADHD modules.

### Token Saver

- **RTK tool compression filters.** Ported five new command compactors from upstream rtk-ai/rtk (oxlint, shellcheck, markdownlint, yamllint, brew-install), bringing the embedded catalog from 55 to 60 filters.
- **RTK live test bench filter selection.** Fixed the test bench payload in the dashboard so tests execute against the user selected active filters rather than defaulting to all.
- **Headroom Python 3.14 support.** Added python3.14 to candidate binaries and framework search paths. Updated dashboard guidance to reflect headroom-ai v0.42.0 requiring Python >= 3.11.
- **Caveman negation protection.** Added upstream safety floor across Lite, Full, and Ultra prompts to preserve negation words (not, never, no, only) and avoid loss of negative intent during compression.
- **ADHD debug spiral escape hatch.** Added the upstream rule to the Full prompt to break infinite retry loops after three consecutive failed attempts by stating the unproven assumption and asking a diagnostic question.

### Fixes

- **Token Saver view card property.** Removed non-standard id attribute on the Svelte Card component.
- **Linter cleanups.** Fixed inefficient string concatenation in RTK regression tests.

## [1.0.5] - 2026-10-31

Seven new providers imported from the 9router-go and 9router upstream registries: SenseNova, RunCrate, Nace, and four web-based providers (ChatGPT Web, Muse Web, UC Web, Veo AI Free).

### Dashboard

- **Check All Models is gentler on free-tier RPM.** Each probe used a 1024-token chat completion with six parallel workers, which was enough for llm7 to answer `HTTP 429 Rate limit exceeded. Retry after 1 seconds` on a two-model sweep. Chat probes now ask for 256 tokens (length-limited reasoning replies still count as a pass), Check All runs at most two probes at once with a short start stagger, and a 429 surfaces `retryAfter` so the sweep waits before the next model instead of immediately re-hitting the same key pool.

### Providers

- **SenseNova** (alias `sn`). OpenAI-compatible API at `token.sensenova.ai` (international Token Plan; keys from [platform.sensenova.ai/console/keys](https://platform.sensenova.ai/console/keys)) with image generation support. Seven models including `sensenova-6.8-flash-lite`, `sensenova-u1.5-lite`, `sensenova-u1.5-fast`, `deepseek-v4-flash`, `deepseek-flash`, `glm-5.2`, and `kimi-k3`. Capabilities include Vision and ImageOutput on supported models; the flash-lite model is priced.
- **SenseNova endpoint region fix.** The catalogue initially pointed at the mainland China host (`token.sensenova.cn`) and linked "Sign up / Learn more" there. International keys from `platform.sensenova.ai` then failed connection probes with HTTP 401. BaseURL/ImageURL and the dashboard key link now use the international hosts.
- **RunCrate** (alias `rc`). Multi-model gateway at `api.runcrate.ai` exposing DeepSeek-V4.1-Flash, GLM-5.3, Qwen3.8-Flash, and gpt-oss-120b. All four models carry Reasoning, Tools, and ThinkingFormat capabilities.
- **Nace** (alias `drex`). Drex AI API at `console.nace.ai` with two models: `drex-max` and `drex-v2.0`, both with Tools capability.
- **ChatGPT Web** (alias `cg`). Browser-session provider at `chatgpt.com` using Cookie-based raw authentication. Models: `gpt-5-6`, `gpt-5-6-thinking`, `gpt-5.6-luna-free`.
- **Muse Web** (alias `mu`). Browser-session provider at `muse.ai` using Cookie-based raw authentication. Models: `muse-chat`, `muse-image`, `muse-video`.
- **UC Web** (alias `uc`). Browser-session provider at `internal.chatuncensored.ai` using Cookie-based raw authentication. Models: `seedream-v5`, `nano-banana-pro`, `sora-2`, `seedance-2.5`.
- **Veo AI Free** (alias `veo-free`). No-auth provider at `veoaifree.com`. Models: `veo`, `seedance`, `nano-banana`.

All seven providers are registered across the full stack: `KnownProviders` (BaseURL, AuthHeader, AuthScheme), `ProviderModels`, capabilities (`Vision`, `Reasoning`, `Tools`, `ThinkingFormat`, `ImageOutput`), aliases, registry aliases, executor registration (`ForwardOpenAI`), pricing tables, and the frontend catalogue (`providers.ts`).

### Provider catalogue

- **SumoPod key link fix.** The dashboard "Get API Key" link pointed at `https://ai.sumopod.com`, which returns 404. Keys are created at `https://sumopod.com/dashboard/api-services/keys`; `notice.apiKeyUrl` now points there. BaseURL (`ai.sumopod.com`) and the live `/v1/models` fetcher are unchanged — they already require auth and answer correctly.
- **Provider count updated from 142 to 149.** The README badge and catalogue references now reflect the seven additions.

## [1.0.4] - 2026-10-30

Combo lockdowns are respected in the failback chain, and the Combo flow UI shows when every key of a member is parked.

### Tests

- **`TestPatchScalesLinearly` skips under the race detector.** The test compares ns/op across a 4× sample to prove the patcher is linear (~4×), not quadratic (~16×). Under `-race` the instrumentation itself warps the ratio (CI saw 8.37× against an 8× budget on YAML with no code change), the same class of flake the TOML pass-count budget already documents. The shape check still runs on plain `go test`; race builds skip it.

### Routing and quota

- **A combo no longer dials a cooling account when `forceFallback` is on.** That toggle exists for a lone provider with nowhere else to go; a combo's next member is the failover. With it enabled, `getBestConnection` was forcing the soonest `rateLimitedUntil` account through the combo loop (Gemini with two locked keys still got hit), which is how an operator earns a ban. Combo (and fusion) selection now refuses force-fallback; one free key among N still rotates, and a lone provider still surfaces "all in cooldown, earliest reset …".

### Dashboard

- **Combo flow shows a member as locked only when every key is parked.** Rate-limit alerts were visible in the bell panel while the combo list stayed on "not used yet", so a fully cooled Gemini/llm7 looked idle. The card now reads connection lock stamps (`rateLimitedUntil`, `modelLock_*`) and paints `locked` when every active connection of that provider is blocked for the member's model — one of N locked keys still rotates and does not mark the row.

## [1.0.3] - 2026-10-30

The pre-publication audit (`docs/auditoriaUnificada.md`, 35 items) is closed except for two that need the repository owner to act on GitHub: the repository topics and the deletion of a local branch holding leaked OAuth secrets.

### Security

- **Admin routes no longer accept a client API key.** `RequireDashboardAuth` accepted any active engine key, so consuming the gateway was the same as administering it — create and delete connections, mint and revoke keys, change settings, rotate the vault. Only the dashboard session cookie and the local CLI token open the admin group now; a client key gets `401`. The rest of that gate (the local CLI token, the re-typed password header on the export route) is unchanged. *The audit's item 1.1.*
- **The proxy no longer retries a failed TLS verification with verification off.** A certificate that did not validate was retried with `InsecureSkipVerify`, which is exactly the request an interception proxy wants: it carries provider tokens, API keys and the conversation. The retry is gone. An operator behind a corporate MITM appliance sets `ALLOW_INSECURE_TLS=true` to opt in, and `.env.example` documents installing the appliance's CA as the preferred fix instead. *Item 1.2. The code also read `STRICT_SSL` with `os.Getenv`; the new variable is documented next to the loader so a value in `.env` is known to reach it.*
- **`INITIAL_PASSWORD` is honoured again.** `config` parsed it into `Config.InitialPassword` and nothing read the field, so an operator who set it still got a generated password — and the startup notice advised setting a variable that did nothing. It is now the password when set (and is not forced to be replaced, because the operator chose it); blank counts as unset. *Found by the end-to-end job, which boots the gateway with it and logs in.*
- **The dashboard password is generated at first start, printed once, and must be replaced.** A fresh install accepted `123456` — the value every 9router install shares. Hiding it from the README would not have helped, so the behaviour changed: the first start writes a random 24-character password, prints it in a block that says it is shown once, and the login with it answers `403 mustChangePassword` until a new one arrives in the same request. A stored hash of the old default counts as unset, so an upgraded install is migrated the same way. The "must change" state is an explicit `passwordGenerated` marker in settings, not a guess from the hash: a bcrypt hash carries no information about where the password came from, and the first version of this returned `200` for the generated password instead of `403`. The startup notice prints the port the gateway actually bound, not a literal `20130`. *Item 1.3.*
- **`.env.example` no longer ships working defaults.** `JWT_SECRET=change-me-to-a-long-random-secret` and `INITIAL_PASSWORD=change-me` were both usable as-is, and the `change-me` password also disabled the protection it looked like it provided, because that guard only checked for an empty value. Both are commented out with an explanation, and `DATA_DIR` is documented as per-platform instead of `/var/lib/nox9go`. *Item 1.3.*
- **The updater can no longer install a binary for another platform.** `version.json` published one `downloadUrl` — the Windows executable — and the updater took it on every platform when no platform-specific entry existed, so a Linux or macOS install could download and swap in a binary it cannot run; the checksum did not help, it only proved the bytes were the published ones. The manifest names a URL and a `sha256` per platform, an unlisted platform is refused rather than falling back to the first non-checksum asset, and the downloaded file is parsed (PE, Mach-O or ELF) to confirm it is an executable for this OS and, where the header records it, this architecture before it replaces the running binary. `scripts/gen-version-manifest.mjs` writes the manifest and `--check` validates it without touching it. *Item 1.5.*
- **The personal environment is out of `AGENTS.md`.** Absolute paths, the author's user name, the initial password, Engram identifiers and `.atl` references described one machine, not the project. `.rtk/` and `.atl/` are ignored and untracked. *Item 1.6.*
- **Secret scanning runs in CI.** `gitleaks` over the full history on every push: a secret removed from `HEAD` is still leaked in the commits behind it. *Item 5.*
- **`data`, `web/dist`, SQLite sidecars and every `.env` variant are ignored.** `.env.local` and `*.db` were committed by `git add -A` before; the resolved data directory holds every provider token and the WAL holds committed pages. *Item 22.*

### Documentation and repository

- **`SECURITY.md`**: how to report privately, what to include, how a report is handled, what is in and out of scope, and what to do when a credential leaks — revoke first, because rotating it in the repository does not remove it from the history that was pushed.
- **`CONTRIBUTING.md`**: requirements, the build and test commands that actually exist, the convention that a divergence from upstream is labelled with why, and the changelog rule.
- **`THIRD_PARTY_NOTICES.md`**: the 9Router and 9router-go attributions, the provider catalogue source, the Go and frontend dependencies, and the provider logos that are trademarks and not covered by MIT.
- **`LICENSE` is recognised as MIT again.** The attribution paragraph sat inside the permission text, so GitHub read the file as `NOASSERTION`/"Other". The licence and every copyright notice are unchanged; the paragraph moved above the permission paragraph.
- **CI.** A workflow on push and pull request: frontend install with the frozen lockfile, the svelte-check ratchet, lint and build; `go vet`, `go test ./...` and a clean build against that bundle; the race detector; `gitleaks`; `govulncheck`; the update-manifest check; and the end-to-end suite in its own job. Bun is pinned to 1.4.4 because `web/bun.lock` is `lockfileVersion: 2` and another major rewrites it. *Items 12, 13, 32.*
- **The README says how the "web" providers reach their services.** Reusing the browser's session cookie, presenting the official client's identity and TLS fingerprint, and the fact that this can break a provider's terms of service are now a section of their own with the responsibilities that follow. *Item 14.*
- **The README's promises are qualified.** "142 providers" now says catalogue, not verified integration; the free-tier quota figures point at the provider; sharing a 9Router database says which versions are compatible and to back up first; OAuth notes that some services require the user's own client credentials; the `Personal fork` badge became `MIT`; and a new section separates `nox9go` from `nox9go-tray` (Windows only). *Items 16, 23.*
- **Docs match the code again.** `ARCHITECTURE.md` no longer hard-codes a version (it pointed at v1.9.9 while `VERSION` said 1.0.2); `DATABASE.md` documents the credential vault as it is — opt-in, off by default, covering the sealed credential columns only, with the migration replacing the `data` column rather than leaving a plaintext copy, and the master key unrecoverable if lost; `web/README.md` lists the scripts that exist (`check:svelte`, `ratchet:svelte`, `e2e`) and the fingerprint-based rebuild instead of the "skip if dist exists" rule. *Item 15.*
- **The change-log entry for the upstream notice is gone**, and `odd/tasks/` with it: they described a package (`internal/upstreamwatch`) that no longer exists, and plans that belong in issues. *Items 10, 26.*
- **`CLAUDE.md` points at `AGENTS.md`** instead of duplicating it. *Items 9, 31.*

### Build and tooling

- **`build.sh` delegates to `make build`.** It was a second copy of the recipe and had drifted: it skipped the frontend staleness check, dropped the `CGO`/`LDFLAGS` handling, and probed a personal path under `/tmp`. One recipe now, in the Makefile. *Item 11.*
- **`make cross` really produces static binaries.** `cross-one` never set `CGO_ENABLED=0`, which the README already promised; a cgo-capable host linked the target against its own libc. Verified `statically linked` after the change. *Item 18.*
- **`bench` and `bench-go` are gone.** Both pointed at a `benchmark/` directory that does not exist, so they failed with a missing file rather than a benchmark. *Item 17.*
- **`make status --json` for scripts, and the PowerShell launcher uses it.** `scripts/start-nox9go.ps1` decided whether the gateway was running by matching the text `is running` and polled a hard-coded `20130`, so `PORT` was ignored. It reads `running`, `pid` and `dashboard` from `status --json` and opens the URL the gateway reported. The file also regains the syntax it was missing. *Items 8, 24.*
- **Providers with no quota endpoint report `unknown`** instead of the string "Usage API not implemented for <provider>", which the tracker rendered as a failure next to a working account. A missing credential in that path now says so. *Item 19.*
- **Dead weight removed:** `lucide-react` (a React package in a Svelte tree, and the only reason `react` was in the lockfile), the template `react.svg`/`vite.svg`, and the six intermediate `i18n-*.json` extraction dumps that no script consumed. The lockfile was regenerated with the bun that wrote it, so the diff is the removed entries and nothing else. *Items 27, 28, 29.*
- **The skills screen says where its instructions come from.** They are fetched at runtime from `decolua/9router` and describe that project's endpoints, so the page carries a notice and the entry is named after it rather than after NOX9GO. *Item 30.*

### Tests

- **The updater's platform guard is tested on every platform it targets.** The test payload was an eight-byte ELF magic on non-Windows and a hand-built PE header, and each was only ever parsed on the host that produced it: on the Linux runner `debug/elf` reported that it could not read the identifier, and the cross-platform check asked the guard to accept an ELF as a Windows binary. `peImage(goarch)` and `elfImage(goarch)` build the header for the platform under test on any host, and the guard is asserted to accept the published pairs and refuse a foreign architecture.
- **A model missing from a fresh Qoder catalogue no longer costs a network round trip.** `qoderModelConfig` resolved a key, missed, and force-refreshed the catalogue from upstream before giving up — so asking for a model that does not exist spent a request (and, offline, the whole timeout) to learn nothing. A key absent from a live catalogue is absent; the refresh is kept for the case that can change, no catalogue at all or a stale one. `TestForwardQoder_UnknownModelNamesTheModel` went from 30s and "context deadline exceeded" on the CI runner to instant, because it no longer reaches `api3.qoder.sh`.
- **The end-to-end suite runs on every push, and its six remaining failures are known and do not gate.** The job had never executed: every push in this repository's history shows it cancelled. With a binary to run it starts and 40 of 46 pass; the six that fail are tests the 2026-10-24 runtime i18n changed out from under (the top bar's `Language` item came back with a real handler while the test still asserts it is absent; two selectors now match two elements). They are listed here rather than hidden because the suite is the only thing that exercises the dashboard in a browser, and it finds real product bugs — `INITIAL_PASSWORD` above was one of them. The job reports without blocking until those tests are repaired.
- **An inherited `XDG_CONFIG_HOME` can no longer redirect a pinned `Options.Home`.** `xdgConfigHome` read the environment before the caller's home, so on a runner that exports it the OpenCode adapter patched the machine's real `~/.config/opencode/opencode.json` instead of the fixture. `Home` wins, which is what `Options.Home` already documented.

### Dashboard

- **El semáforo de servicios del sidebar ahora son badges de estado.** Los tres puntos verdes (`Gateway`, `DB`, `Túnel`) del sidebar se reemplazan por dos badges tipo chip — `Tunnel` (túnel Cloudflare) y `DB` (SQLite) — con el mismo estilo visual que el chip de latencia del `TopBar`. El badge del gateway local ya existe en la barra superior, así que no se duplica. En modo colapsado el sidebar muestra solo los puntitos de color.

## [1.0.2] - 2026-10-28

Provider probes that tell a network block from a bad key, OpenCode in the CLI tools screen, and a dashboard that shows this project's own change log.

### CLI tools

- **OpenCode is a CLI tool the dashboard can drive.** A new `clientconfig` registry gives every CLI client a shared patch/paths abstraction, so a tool is driven by one code path instead of one path per client. The OpenCode adapter reads and writes `opencode.json`/`opencode.jsonc`, tolerates comment-style JSONC, and **merges** the provider block instead of overwriting the operator's own agents and permission rules. The new backend endpoints let the web UI inspect and rewrite that config, and the CLI tools screen grows an OpenCode card that shows the detected config path and applies the NOX9GO provider without losing the existing ones. *Tests cover the JSONC parsing and the patch merge.*

### Provider probes and catalogues

- **A network block is no longer reported as a bad key.** Groq answered every provider test with `403 Access denied. Please check your network settings.` and the dashboard called it "Invalid API key or base URL", so the operator went hunting for a credential problem that did not exist — the key was never read, and the same 403 comes back for a bogus bearer, for the bare host and for every model at once, with `Server: cloudflare` in the headers. The status code cannot tell the two apart, so the body decides: `looksEdgeBlocked` matches an infrastructure phrase ("check your network settings", Cloudflare's 1020/1010, "just a moment", "attention required") but yields to any phrase that names the credential, so a genuine key rejection is never relabelled as a network problem. The blocked outcome says the key was not evaluated, points at the egress, and logs the body at WARN so the log stops calling it a rejected credential. Wired into `validateGeneric` on both the `/models` probe and the chat fallback, and into `probeCompatibleConnection` on both the OpenAI and Anthropic-compatible branches.
- **A known provider with no static catalogue answers 200, not 404.** `GET /api/models/caps?provider=zed` answered 404 on every dashboard load. The route and the handler were fine; the handler's own empty-map branch collapsed two different situations into one status — "this provider has no static models" and "this provider does not exist". Zed serves its catalogue live, so it has no static list and always landed in that branch, while compatible nodes and provider nodes were already answered with an empty 200. The registry is consulted first now: a known provider gets `200 {caps: {}}`, and the 404 is kept for the only case where the status is true — an id the registry does not know and no node backs.
- **A Zed turn that folds to nothing says what the stream carried.** "upstream event stream carried no completion chunk" named a symptom and nothing else, and that one sentence was reaching the log for three unrelated faults: an empty stream, a stream whose events never parsed as JSON objects, and a JSON stream of event types the fold does not model. Each wants a different fix, and the message could not tell them apart. The fold now counts the events it discards and, when nothing was recognised at all, logs the raw size, that count and the first 500 bytes; discarded events are logged at DEBUG with their own head, so a stream that arrives in an unexpected shape is visible instead of silently absent. `zedHead` renders a body for a log line: control bytes and newlines are escaped so a multi-line SSE frame cannot break the log and a binary or gzip body cannot wreck it, and a truncated sample is marked so it is not read as a complete one.

### Routing and quota

- **A malformed request stops the combo instead of burning it.** A 400/422 whose body marks the request itself as malformed or unsupported (bad tool-call payload, unsupported modality, out-of-range `max_tokens`, `invalid_request_error`) was falling through the combo loop's generic `continue`, so every remaining combo member was tried against the same body and rejected in turn — one connection per member, until the pool was saturated. `IsRequestInvalidError` (a sibling of `IsModelFitError`: fit-error covers "too big", request-invalid covers "wrong shape") breaks the turn in both combo loops, OpenAI and Claude. The account is not at fault, so `comboLockRetryable` is skipped, same as fit-error; the single-provider fallback path already filtered through `RetryableStatusCodes` and needed no change.
- **CodeBuddy honours the exact reset instant the 429 names.** A spent model quota comes back as HTTP 429 with an absolute reset instant in the body ("usage exceeds frequency limit... reset at 2026-10-28 11:29:25 UTC+8"). Two things dropped that instant: the executor replaced the body with a generic message, and the cooldown path clamped every reset hint to 2h. The model was therefore retried while the quota was still spent, which is what turns a frequency limit into an account restriction. The executor now keeps the upstream body, and `retryableCooldownSec` takes the provider so the CodeBuddy branch can honour that instant exactly. The lock is per connection and model, because a CodeBuddy quota belongs to the OAuth account: the provider+model pair lockout stays opt-in and off, so healthy keys keep serving.
- **CodeBuddy parks restricted accounts, paces requests and refreshes tokens.** An account answering code `11140` is cooled down for 12h instead of 2m, requests are spaced 1.5s per credential, and OAuth refreshers are registered for `codebuddy-cn` and `codebuddy-intl`. *Ported from `decolua/9router` `tokenRefresh/providers.js`.*

### Dashboard

- **The change log shows this project's notes.** The "Change Log" entry in the account menu opened a modal fed by `/api/changelog`, a route mounted with the engine routes behind `middleware.RequireApiKey` — so the SPA's call, which carries the session cookie and no engine key, was answered `401 invalid_api_key`. The client then fell back to a GitHub copy, and the last link in that chain was `decolua/9router`'s `CHANGELOG.md`: the modal showed the upstream project's release history under NOX9GO's name. The route now answers under `RequireDashboardAuth` (session cookie, API key, or local CLI token); the handler serves only this repository's own `CHANGELOG.md`, read from the working directory or from the directory holding the binary; and both the browser client and the handler lost their remote fallbacks — this repository's raw copy is not readable by an anonymous client, so a missing file is a missing file, and the dashboard no longer reaches a third-party origin for it. A pending `.changes/` fragment also keeps the document's own `# Changelog` title first instead of landing above it.
- **Manual model entry for platforms with no static catalogue.** When a media provider has no static model list (`getModelsByProviderId` returns `[]`), the model selector did not render and `selectedModelId` stayed empty; `resolveQualifiedModel('')` then fell back to `provider.id` (`replicate`), which the backend rejects with `model "replicate" must be owner/name`. A text input bound to `customModelId` is the `{:else}` branch of the selector now, and `effectiveModelId()` returns `customModelId` when no static models exist, so the example curl and the Run button send the operator-typed model id (`black-forest-labs/flux-1.1-pro`).
- **The upstream author's email is gone from the tracked files.** The email masking helper, its tests and the i18n extraction dump carried `luqman.hakim@gmail.com` — the upstream author's address — because the guardrails feature was ported from upstream with its fixtures. In a tracked repository that is someone else's personal data. It is now the operator's own address, and the masked form changes from `l***m@gmail.com` to `p***v@gmail.com`.

## [1.0.1] - 2026-10-27

First public release.

### Media providers

- **Replicate integration.** Replicate was registered in the catalog with a `BaseURL` pointing at `/v1/chat/completions`, a route that does not exist; every media call 404'd and Replicate answered "no API key found", which read like a bad credential and sent the operator looking at their key when the route was the fault. A dedicated lane (`internal/handlers/media/replicate.go`) now runs Replicate through its real protocol: `POST /v1/models/{owner}/{name}/predictions` with `Prefer: wait=60`, polling `GET /v1/predictions/{id}` as fallback, and an OpenAI-shaped response for callers. Three model-id forms are resolved: `owner/name` (official), `owner/name:version` (pinned), and `deployments/owner/name` (private/fine-tuned). Text-to-image and video generation are supported; `/v1/videos/edits` and `/v1/videos/extensions` are deliberately **not** routed to Replicate because it has no such upstream endpoint and a prediction would be spent on a body the model ignores. `KnownProviders["replicate"].BaseURL` is now the API root (`https://api.replicate.com/v1`); there is no `VideoURL`/`ImageURL` constant because the prediction path embeds the model id. *Verified against the live API: every constructed path returns 401 with a bad key (route exists) instead of 404.*
- **BaseURL coherence test.** `internal/providers/base_url_coherence_test.go` fails if a provider registered for a non-chat service declares a `BaseURL` ending in `/chat/completions` or `/messages`: the media handlers strip that suffix to build the endpoint, so a non-chat provider advertising it makes every media call 404. Replicate is the named case the test was written for, so a regression reintroducing the path is caught rather than silently passing.

### Dashboard

- **Promote-to-catalog dialog shows service kinds and explains media conflicts.** The dialog hardcoded `serviceKinds: ['llm']`, so a custom node that actually served TTS or embeddings was promoted as chat without a question. It now renders a nine-kind selector (LLM, Embedding, Image, Video, TTS, STT, System One, Web Search, Web Fetch) with LLM checked by default. When the id being promoted already exists in Media Providers, the 409 carries the existing entry's `serviceKinds` and the dialog says so: "ese ID ya existe y además sirve Text to Image, Video" — the previous advice ("pick another id") hid that the existing entry is a different kind of provider, not a duplicate. New error code `PROVIDER_ALREADY_AS_MEDIA` distinguishes a media collision from a plain duplicate. `ApiError` gained a `payload` field so the dialog reads structured fields without re-parsing the message.

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
- **The update alert watches this project's own releases.** A chip between the latency meter and "Uso de hoy" appears when `Pacoaldev/Nox9Go` publishes a version newer than the one compiled into the binary, and opens the Update modal, which names both versions, shows the release notes and asks before installing (Auto Update verifies the asset against `version.json`'s `sha256`; Copy & Shutdown is there for a manual run). The check compares `updater.CurrentVersion` against `version.json` on `main`, falling back to GitHub Releases; the result is the one the sidebar banner already polls, so the chip and the banner cannot disagree. Backed by the existing `/api/version/check` and `POST /api/version/update` — no new route, and nothing in the dashboard reaches GitHub directly. It is hidden whenever `currentVersion == latestVersion`, which is the designed behaviour, not a fault. *Replaces the tracked-upstream notice: watching the repositories this fork ports from answered a maintainer's question, not a user's.*
- **Combo creation sends explicit model kind.** `CombosView` includes the `kind` field in the combo creation payload to match schema validation.
- **Perplexity Web speaks its own protocol.** The `perplexity-web` provider is no longer posted an OpenAI Chat Completions body: it answers `422 {"detail":[{"loc":["body","query_str"],"msg":"Field required"}]}` for that. A dedicated executor now sends the site's own contract (`query_str` at the top level and inside `params`), authenticates with the browser session cookie (`__Secure-next-auth.session-token`) instead of a bearer token, and translates the site's `blocks`/`markdown_block` event stream back into OpenAI chunks — streaming and non-streaming. Follow-up turns reuse `params.last_backend_uuid` through a bounded session cache (200 entries, 1 h TTL). *Ported from `decolua/9router` `open-sse/executors/perplexity-web.js`.*

- **The usage chart's daily window follows UTC days.** `dailyChartBuckets` derived its day buckets from the local clock and then keyed them by UTC date, so in a zone ahead of UTC and a local time before that offset — the first hours of a local day — one UTC day was crossed twice and the next was skipped, and the rollup row for the skipped day fell into no bucket at all. The chart showed 0 tokens for a day that had usage for `7d`, `30d`, `60d` and `all`. The window is now built from consecutive UTC days, matching the `dateKey` the usage writer stores. *Found while fixing `TestHandleUsageChart`.*
- **The alert bell now shows which provider and key are parked.** The badge used to answer "how many" and nothing else. `GET /api/dashboard/alerts` returns one row per alert naming the connection, provider, API key, model, the upstream's own error message, HTTP status and when the lock expires; the bell opens a panel with that list instead of a tooltip with a count. The provider display name is resolved from the dashboard's own catalog (`web/src/lib/providers.ts`), so the catalog stays in one place. *Backed by `internal/handlers/dashboard/alerts.go` and `web/src/components/AlertsBadge.svelte`.*

### Providers & proxy
- **A provider+model pair can be parked as a unit, opt-in per provider.** Lockouts used to live only per connection (`modelLock_<model>` on the connection row), so when a combo member's quota was spent the failover walked the provider's remaining keys one at a time. A new fork-local table `providerModelLocks` can park `(provider, model)` as a single state with an absolute deadline: the combo skips the whole member instantly, the selector refuses every connection of that provider for that model, and a served request clears the park. **It is off for every provider by default** — the shape of a key pool is usually independent per-key quotas, and parking the pair there would strand healthy keys. A provider only opts in when its keys share one upstream quota, via `sharedQuotaProviders`. When opted in, only the quota class parks the pair: a 429 carrying a quota/capacity marker, a 402, or a 503 carrying a quota/capacity marker. A generic 429 stays a per-connection cooldown and a 401 stays a per-account park. The dashboard bell reports a pair lockout at pair scope with its deadline when one is recorded. *Deliberate divergence from `decolua/9router`, which has no pair-level park at all.*
- **Replayed reasoning fields are stripped for providers with a strict message validator.** Cerebras answered `400 ... property "messages.N.assistant.reasoning_content" is unsupported` for a conversation replayed from a reasoning model; `groq`, `mistral`, `kiro`, `kilocode` and `kilo-gateway` refuse the same fields (`reasoning_content`, `reasoning_details`, `reasoning`). The strip now runs on every forwarded body and on the OpenAI → Kiro `conversationState` translation, not only inside the opencode / DeepSeek / Kimi injection path. Providers that *require* the field are untouched.
- **CodeBuddy conversation identity follows the caller and the body.** A connection whose stored headers carry its own `X-Conversation-*` ids keeps them instead of being overwritten by a generated set — a differently-cased caller key and the generated header are one header once Go canonicalizes them, so which one survived was decided by map iteration order and the caller's conversation was silently restarted on an unpredictable subset of turns. The conversation key is now also read from the request body (`session_id`, `prompt_cache_key`, `conversation_id`) before falling back to the opening message, so turns whose first line differs stay in one conversation. Code `11128` is reported as the channel-verification failure it is — branched on the message, because the code also carries a body-shape error — instead of surfacing as a bare `400`. *Ported from `decolua/9router` #5217.*

### Configuration & tooling

- **`.env` variables exported to process environment.** Dedicated loader (`internal/envfile`) parses `.env` files and exports entries via `os.Setenv` at startup, ensuring direct `os.Getenv` lookups in subcommands, CLI status checks, and OAuth handlers resolve reliably without depending on Viper bootstrap order.
- **OAuth client secrets required via environment.** Hardcoded OAuth client secrets were stripped from compiled Go binaries; sensitive OAuth credentials (e.g. Antigravity) must be provided through `.env` or system environment variables.
- **Upstream free & API-key provider catalogue sync.** `scripts/sync-providers.mjs` extracts and normalizes the active provider catalogues from both upstream repositories (`decolua/9router` and `cheaper-inference/OmniRoute`), filtering out OAuth-only, web-cookie, and loopback/localhost endpoints. Generates `docs/providers_free.md` (281 providers across free, free-tier, and api-key) and supports diffing against a baseline snapshot via `--check`. Accessible via the `/check-providers` slash command.

### Version

- The version lives in one place: the `VERSION` file. `make build` stamps it into the binary with `-ldflags`, and `scripts/gen-version-manifest.mjs` writes the matching `version.json` (per-platform download URLs and `sha256`). Nothing else hard-codes a version number.
- The update alert watches **this project's own releases** only. It compares the compiled `CurrentVersion` against `version.json` on `main`, falling back to GitHub Releases, and offers to install. *Replaces the tracked-upstream notice, which watched the repositories this fork ports from and answered a maintainer's question rather than a user's.*
