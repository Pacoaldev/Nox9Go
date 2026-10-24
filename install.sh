#!/usr/bin/env bash
# nox9go single-line installer: detects OS/arch, downloads the latest
# release binary from GitHub, installs it, and prints next steps.
#
#   curl -fsSL https://raw.githubusercontent.com/Pacoaldev/Nox9Go/main/install.sh | bash
#
# Env overrides: VERSION (e.g. v1.9.2, default: latest), BINDIR (default:
# /usr/local/bin, falls back to ~/.local/bin).
set -euo pipefail

REPO="${REPO:-Pacoaldev/Nox9Go}"
VERSION="${VERSION:-latest}"
BINDIR="${BINDIR:-/usr/local/bin}"

info()  { printf '\033[1;32m[nox9go]\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33m[nox9go]\033[0m %s\n' "$*" >&2; }
fatal() { printf '\033[1;31m[nox9go]\033[0m %s\n' "$*" >&2; exit 1; }

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$OS" in
  linux|darwin) ;;
  *) fatal "unsupported OS: $OS (Linux and macOS only; Windows: download the .exe from https://github.com/$REPO/releases/latest)" ;;
esac
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) fatal "unsupported architecture: $ARCH (amd64/arm64 only)" ;;
esac

ASSET="nox9go-${OS}-${ARCH}"
if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
else
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
info "downloading $URL ..."
curl -fsSL --retry 3 -o "$TMP/nox9go" "$URL" \
  || fatal "download failed — check https://github.com/$REPO/releases"
chmod +x "$TMP/nox9go"

if [ -w "$BINDIR" ]; then
  mv "$TMP/nox9go" "$BINDIR/nox9go"
else
  warn "$BINDIR not writable, trying sudo (or set BINDIR=\$HOME/.local/bin)"
  sudo mv "$TMP/nox9go" "$BINDIR/nox9go"
fi
trap - EXIT

info "installed to $BINDIR/nox9go"
"$BINDIR/nox9go" version || true
echo
info "start it with:"
echo "       nox9go"
echo "Dashboard: http://localhost:20130 (defaults: port 20130, data ~/.nox9go)"
