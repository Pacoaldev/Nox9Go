# Contributing

Thanks for looking. This is a small, single-maintainer project, so the bar is
"does it make the gateway better for the person running it", not "does it grow
the feature list".

## Before you write code

- **Open an issue first** for anything larger than a bug fix or a typo. A
  refactor or a new provider adapter that arrives as a surprise pull request is
  likely to be declined on scope alone, not on quality.
- **Security problems do not go in an issue.** See [SECURITY.md](SECURITY.md).
- Read [AGENTS.md](AGENTS.md). It is written for an AI assistant working in this
  repository, but it documents the same conventions a person needs: where code
  belongs, what the tests expect, and why several things that look like bugs are
  deliberate divergences from upstream.

## Requirements

| Tool | Version | Why |
| --- | --- | --- |
| Go | 1.27 | `go.mod` pins it; the toolchain line covers patch releases |
| Bun | 1.x | builds the Svelte dashboard that gets embedded in the binary |
| GNU Make | 3.82+ | every build path goes through the Makefile |
| Git | any recent | |

## Build and test

A fresh clone has no `web/dist`, so the Go build cannot embed the SPA until it
exists. `make build` handles that; `go build` alone on a clean tree will fail on
`//go:embed dist/*`.

```bash
make build          # dashboard, then the nox9go binary
make tray           # the Windows tray companion (nox9go-tray)
make test           # go test ./... (+ the integration suite)
make vet            # go vet ./...
make test-race      # the race detector, slower
make cross          # linux/darwin/windows binaries + SHA256SUMS.txt
```

Frontend-only work:

```bash
cd web
bun install --frozen-lockfile
bun run check:svelte
bun run lint
bun run build
```

The suite must pass before a pull request is reviewed. If you add behaviour,
add the test that would fail without it; if you fix a bug, add the test that
reproduces it. A test that only exercises the happy path you just wrote is not
the test that matters.

## Conventions

- **Go style is whatever `go vet` and the surrounding code say.** Follow the
  file you are editing rather than importing a style from elsewhere.
- **Comments explain why, not what.** Several comments in this tree record a
  real incident ("the SPA was served stale for a whole release because …").
  That is the level of detail worth writing; `// increment i` is not.
- **Divergence from upstream is labelled.** If you change behaviour that
  `decolua/9router` implements differently, say so in the comment and in the
  changelog, and say why. Compatibility is a feature; silent drift is not.
- **No new runtime dependency without a reason in the pull request.** The Go
  binary is meant to stay self-contained and `CGO_ENABLED=0`.
- **Do not add a secret, a token or a personal path.** Not in code, not in a
  fixture, not in a doc.

## Changelog

Edit [`CHANGELOG.md`](CHANGELOG.md) directly, under the current version
heading. There is no fragment workflow: it was upstream's multi-contributor
release machinery and it is gone. Write the entry for the person who will read
it after an upgrade — what changed for them, not which package you touched.

## Pull requests

- One change per pull request. A bug fix does not carry a refactor.
- Say what the change does, why, and what you ran. If tests were added, name
  them.
- Keep the diff reviewable. Formatting-only commits mixed with logic make a
  review take twice as long.
- CI must be green. It builds the frontend, runs `go vet`, `go test`,
  `go test -race`, a secret scan, a vulnerability scan and the update-manifest
  check.

## Issues

A useful issue includes the version (`nox9go version`), the platform, how the
gateway was started, the provider and model involved, and the log lines with
credentials removed. "It does not work" cannot be acted on; "this request to
this provider returns 401 through the gateway and 200 if I call it directly"
can.
