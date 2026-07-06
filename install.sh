#!/bin/sh
# Installs the latest skillx release binary. No dependencies beyond
# curl and tar. Usage:
#
#   curl -fsSL https://raw.githubusercontent.com/mbroton/skillx/main/install.sh | sh
#
# Installs to ~/.local/bin (override with SKILLX_INSTALL_DIR).
set -eu

REPO="mbroton/skillx"
INSTALL_DIR="${SKILLX_INSTALL_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux | darwin) ;;
  *) echo "error: unsupported OS: $os (linux and macOS only)" >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "error: unsupported architecture: $arch" >&2; exit 1 ;;
esac

url="https://github.com/$REPO/releases/latest/download/skillx_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading skillx (${os}/${arch}) ..."
curl -fsSL "$url" -o "$tmp/skillx.tar.gz"
tar -xzf "$tmp/skillx.tar.gz" -C "$tmp" skillx

mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmp/skillx" "$INSTALL_DIR/skillx"
echo "Installed $("$INSTALL_DIR/skillx" --version) to $INSTALL_DIR/skillx"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "note: $INSTALL_DIR is not on your PATH; add it to your shell profile" ;;
esac
