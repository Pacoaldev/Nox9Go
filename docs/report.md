# NOX9GO Code Review Report

**Project:** NOX9GO (fork of 9Router) – IA gateway written in Go, embedded Svelte 5 dashboard.
**Review scope:** Security, code quality, bugs, vulnerabilities.
**Generated:** 2026‑10‑31 (session review).
**Author:** Gentleman (Pi coding-agent harness).

---

## 1. Overview

- **Current version**: v1.0.4 (VERSION file); Go module `nox9go/proxy` Go 1.27.
- **Package manager**: Bun 1.4.x for the Svelte 5 SPA (`web/`), npm for Pi extensions (separate).
- **Testing**: `go test ./...` – all unit tests pass (`ok` for every package). `go vet ./...` – clean.
- **Dependencies**: Go modules listed in `go.mod`/`go.sum`; frontend deps in `web/node_modules`/`bun.lock`.
- **Security policy**: `SECURITY.md` defines in‑scope scope and reporting procedure (GitHub private advisories).

---

## 2. Vulnerability / Security Findings

### 2.1 Hardcoded MiniMax Code signing secret

- **Location**: `internal/handlers/dashboard/usage_minimax_code.go:30`
  ```go
  miniMaxCodeSignSecret = "I*7Cf%WZ#S&%1RlZJ&C2"
  ```
- **How it's used**: The secret is concatenated with a timestamp and the request payload, then MD5‑hashed to produce the `x-signature` header for the MiniMax Code account API (`miniMaxCodeAccountCall` → `signedMiniMaxCodeRequest` line 603).
- **Risk**: A hard‑coded signing secret means anyone who obtains the binary (the typical deployment scenario) can compute valid signatures and forge account‑API calls to MiniMax Code. This effectively compromises the confidentiality/integrity of the MiniMax Code quota/account data that NOX9GO manages on the operator's behalf.
- **Remediation**: Promote the secret to an environment variable (or per‑installation config) and read it at runtime. Example:
  ```go
  miniMaxCodeSignSecret = os.Getenv("MINIMAX_CODE_SIGN_SECRET")
  ```
  and document the variable in `.env.example`.
- **Status**: Not currently exposed via `.env` (the `.env` file is git‑ignored); operators must edit source or add the env var manually.

### 2.2 Unused constant

- **Location**: `internal/handlers/dashboard/connection_probe.go:56`
  ```go
  museProbeAPIKey = "muse-spark-1.3"
  ```
- **Nature**: Constant defined but never referenced elsewhere in the Go source (grep confirms only the definition). Dead‑code / lint concern; may be a leftover from porting.
- **Remediation**: Remove the constant if truly unused, or add a comment explaining its purpose (e.g., test fixture).

### 2.3 No code‑level Go vulnerabilities

- **`govulncheck`** (v1.9.0) run against the whole module: **0 vulnerabilities in project code**. The tool reported 2 vulnerabilities in imported packages and 1 in a required module, but those are not invoked by the project’s own code paths.
- **`go vet`** – clean (no output).

### 2.4 Front‑end audit

- `bun audit` in `web/` fails with “UnknownLockfileVersion” because the project uses `lockfileVersion: 2` (Bun 1.4.4). This is a build‑environment configuration issue, not a code vulnerability. The lockfile is valid for the installed Bun version.

### 2.5 Documentation & policy

- `SECURITY.md` is present and describes the reporting procedure, in‑scope items (gateway / admin API, credential handling, updater, dashboard SPA), and out‑of‑scope items.
- The changelog (v1.0.4) records several security‑related fixes already merged (e.g., admin route auth tightening, TLS‑verification retry removal, password generation hardening, updater platform checks).

---

## 3. Code Quality

| Aspect | Result |
|---|---|
| **Unit tests** | All packages pass (`go test ./...`). |
| **Static analysis** | `go vet ./...` clean. |
| **Go supply‑chain** | `govulncheck` clean for project code. |
| **Lint / formatting** | No custom lint gate enforced here; standard `gofmt` applies. |
| **Dead code** | One unused constant (`museProbeAPIKey`). |
| **Hard‑coded credentials** | MiniMax signing secret is hard‑coded (see 2.1). No other API keys or secrets are embedded in source (the `.env` file is git‑ignored; operators supply them at runtime). |
| **Test coverage** | Broad coverage across handlers, providers, executors; integration test suite exists (`-tags=integration`) but requires a temporary SQLite and fake upstreams. |

---

## 4. Recommendations

1. **Move MiniMax Code signing secret to an environment variable**  
   Add `MINIMAX_CODE_SIGN_SECRET` (and possibly `MINIMAX_CODE_SIGN_SALT`) to `.env.example` and read it at runtime in `signedMiniMaxCodeRequest`. This prevents the secret from being baked into the binary and eases rotation.

2. **Remove or document the unused `museProbeAPIKey` constant**  
   Either delete it or add a comment explaining why it exists (e.g., future probe, test fixture).

3. **Consider externalising other per‑provider secrets**  
   Review all `const` strings that look like credentials (e.g., `miniMaxCodeSignSalt = "ooui"`). If any of them are meant to be operator‑configurable, promote them to env vars.

4. **Keep the security policy up‑to‑date**  
   As new features (e.g., new provider integrations) are added, update `SECURITY.md` accordingly. The changelog already documents security‑related changes; continue that practice.

5. **Run the integration test suite before releases**  
   `go test -tags=integration ./internal/integration/...` exercises the full router + real DB + fake upstreams. It catches wiring and quota‑related regressions that unit tests miss.

6. **Front‑end weight & compression**  
   The dashboard bundle is ~4.6 MB (compressed ~232 KB brotli). No third‑party origins are loaded; fonts and icons are self‑hosted. Keep an eye on bundle size when adding new pages or icons.

---

## 5. Closing

The NOX9GO codebase is generally well‑structured, with passing tests, clean static analysis, and a documented security policy. The two items highlighted above (hardcoded MiniMax signing secret and unused constant) are the only concrete findings. Addressing them will raise the security posture and code hygiene with minimal effort.

*End of report.*