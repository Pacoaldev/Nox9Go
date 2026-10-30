#!/usr/bin/env bash
# Build nox9go reproducibly: web assets first (embedded at compile time),
# then the binary with the version from VERSION.
#
# web/dist is gitignored, so a fresh clone has no SPA to embed and the Go build
# fails on `//go:embed dist/*`. web/scripts/web-build.ts (the same script `make
# web-build` runs) fingerprints the frontend inputs and rebuilds only when they
# changed, so a pulled update never embeds an old dashboard.
set -euo pipefail
cd "$(dirname "$0")"

BUN="${BUN:-$(command -v bun || true)}"
if [[ -z "${BUN}" && -x "$HOME/.bun/bin/bun" ]]; then
  BUN="$HOME/.bun/bin/bun"
fi
if [[ -z "${BUN}" ]]; then
  echo "error: bun not found (install it or set BUN=/path/to/bun)" >&2
  exit 1
fi

echo "==> building web assets"
"$BUN" web/scripts/web-build.ts

VERSION="$(cat VERSION)"
echo "==> building nox9go ${VERSION}"
go build -ldflags="-s -w -X nox9go/proxy/internal/updater.CurrentVersion=${VERSION}" \
  -o ./nox9go ./cmd/nox9go/
echo "==> ./nox9go"
