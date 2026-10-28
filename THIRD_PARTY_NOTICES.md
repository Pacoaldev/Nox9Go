# Third-party notices

NOX9GO is MIT licensed (see [LICENSE](LICENSE)). This file records the other
projects whose code, assets or data it carries, and under what terms.

## Ported from 9Router

[9Router](https://github.com/decolua/9router) — MIT, Copyright (c) 2024-2026
decolua and contributors. NOX9GO is a Go + Svelte port of that project. The
copyright notice is retained at the top of [LICENSE](LICENSE). Portions that
remain recognisably derived from it are labelled as such where they live; the
comment usually names the upstream file (for example
`Ported from decolua/9router open-sse/executors/perplexity-web.js`).

## Ported from Luqmanul Hakim's 9router-go

[9router-go](https://github.com/luqman-v1/9router-go) — MIT, Copyright (c) 2026
Luqmanul Hakim. Notice retained in [LICENSE](LICENSE).

## Provider catalogue

[OmniRoute](https://github.com/cheaper-inference/OmniRoute) — the provider
catalogue synced by `scripts/sync-providers.mjs` is extracted from this project
and from 9Router. The generated file is not committed.

## Go modules

The `require` block in [go.mod](go.mod) lists every Go module this binary
links. Their licences are recorded in each module's own repository and in
`go.sum`'s module cache; the notable ones are:

| Module | Licence |
| --- | --- |
| `github.com/urfave/cli/v2` | MIT |
| `github.com/spf13/viper` | MIT |
| `go.uber.org/fx` | MIT |
| `golang.org/x/crypto` | BSD-3-Clause |
| `golang.org/x/net` | BSD-3-Clause |
| `github.com/bogdanfinn/utls` | BSD-3-Clause |

Run `go list -m -json all` for the authoritative list with versions.

## Frontend packages

See `web/package.json` for the dependency list and `web/bun.lock` for the
resolved tree with integrity hashes. The dashboard bundles Svelte (MIT), Vite
(MIT), Tailwind CSS (MIT), `marked` (MIT), `lucide-svelte` (ISC) and the fonts
vendored under `web/src/assets/fonts/` (see the licence file in that directory).

## Branding

`assets/branding/` and `web/public/` carry the NOX9GO logo and icon set, which
are part of this project. The provider logos under `web/public/providers/` are
the trademarks of their respective owners, used to identify the provider a
connection points at. They are not covered by this project's MIT licence, and
no endorsement is implied.
