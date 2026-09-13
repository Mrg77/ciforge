#!/bin/sh
# ciforge installer — https://github.com/Mrg77/ciforge
# Works on Linux (Debian, Ubuntu, Alpine…) and macOS.
# Usage: curl -fsSL https://raw.githubusercontent.com/Mrg77/ciforge/main/install.sh | sh
set -eu

REPO="Mrg77/ciforge"
INSTALL_DIR="${CIFORGE_INSTALL_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  *) echo "error: unsupported OS '$os' (darwin and linux only — use WSL on Windows)" >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "error: unsupported architecture '$arch'" >&2; exit 1 ;;
esac

version="${CIFORGE_VERSION:-}"
if [ -z "$version" ]; then
  version=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | head -1 | cut -d '"' -f 4)
fi
[ -n "$version" ] || { echo "error: could not resolve latest release" >&2; exit 1; }

archive="ciforge_${version#v}_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/$version/$archive"

echo "Downloading ciforge $version ($os/$arch)..."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$url" -o "$tmp/$archive"
tar -xzf "$tmp/$archive" -C "$tmp" ciforge

mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmp/ciforge" "$INSTALL_DIR/ciforge"
echo "Installed to $INSTALL_DIR/ciforge"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "note: add $INSTALL_DIR to your PATH, e.g.:"
     echo "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.bashrc" ;;
esac

echo
echo "Get started:"
echo "  ciforge scan ./infra --fail-on high     # deterministic security scan (no API key)"
echo "  export ANTHROPIC_API_KEY=...            # then run the agent:"
echo "  ciforge \"build a private, encrypted S3 bucket in ./out, scan it, fix findings\""
