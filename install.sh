#!/bin/sh
# Install the latest code-mem release for macOS or Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/tshubham2/code-mem/main/install.sh | sh
#
# Options (environment variables):
#   CODE_MEM_VERSION      release tag to install, e.g. v0.2.0 (default: latest)
#   CODE_MEM_INSTALL_DIR  where to put the binary (default: ~/.local/bin)
set -eu

REPO="tshubham2/code-mem"
VERSION="${CODE_MEM_VERSION:-latest}"
INSTALL_DIR="${CODE_MEM_INSTALL_DIR:-$HOME/.local/bin}"

fail() { echo "code-mem install: $*" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "unsupported OS $(uname -s). On Windows, download the .zip from https://github.com/$REPO/releases" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported CPU $(uname -m)" ;;
esac

if [ -n "${CODE_MEM_BASE_URL:-}" ]; then
  base="$CODE_MEM_BASE_URL" # for testing against a local build
elif [ "$VERSION" = latest ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi

archive="code-mem_${os}_${arch}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $archive ($VERSION)..."
curl -fsSL "$base/$archive" -o "$tmp/$archive" || fail "download failed: $base/$archive"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" || fail "download failed: $base/checksums.txt"

expected="$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
[ -n "$expected" ] || fail "no checksum listed for $archive"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$archive" | cut -d' ' -f1)"
else
  actual="$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)"
fi
[ "$expected" = "$actual" ] || fail "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp" code-mem
mkdir -p "$INSTALL_DIR"
mv "$tmp/code-mem" "$INSTALL_DIR/code-mem"
chmod +x "$INSTALL_DIR/code-mem"

echo "Installed $("$INSTALL_DIR/code-mem" version) to $INSTALL_DIR/code-mem"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "Note: $INSTALL_DIR is not on your PATH. Add it, or use the full path above." ;;
esac
echo
echo "Next: register it with your MCP client as the command"
echo "  $INSTALL_DIR/code-mem serve"
