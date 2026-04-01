#!/usr/bin/env bash
# kcp-dashboard installer
#
# Usage (curl | bash — no clone needed):
#   curl -fsSL https://raw.githubusercontent.com/Cantara/kcp-dashboard/main/bin/install.sh | bash
#
# Or from a cloned repo:
#   ./bin/install.sh
#
# Installs the kcp-dashboard binary to ~/.local/bin/kcp-dashboard.
# Re-running upgrades an existing installation.

set -euo pipefail

REPO="Cantara/kcp-dashboard"
INSTALL_DIR="${HOME}/.local/bin"

# ── Detect OS and architecture ────────────────────────────────────────────────

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "[kcp-dashboard] error: unsupported architecture: $ARCH"
    exit 1
    ;;
esac

case "$OS" in
  linux|darwin) ;;
  *)
    echo "[kcp-dashboard] error: unsupported OS: $OS"
    echo "  Supported: linux, darwin"
    exit 1
    ;;
esac

BINARY="kcp-dashboard-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/latest/download/${BINARY}"

# ── Download ──────────────────────────────────────────────────────────────────

echo "[kcp-dashboard] installing for ${OS}/${ARCH}..."
mkdir -p "$INSTALL_DIR"
curl -fsSL -o "${INSTALL_DIR}/kcp-dashboard" "$URL"
chmod +x "${INSTALL_DIR}/kcp-dashboard"

# ── PATH hint ─────────────────────────────────────────────────────────────────

if ! echo ":${PATH}:" | grep -q ":${INSTALL_DIR}:"; then
  echo ""
  echo "[kcp-dashboard] note: add ~/.local/bin to your PATH:"
  echo "  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.bashrc  # or ~/.zshrc"
fi

# ── Done ──────────────────────────────────────────────────────────────────────

echo ""
echo "[kcp-dashboard] installed → ${INSTALL_DIR}/kcp-dashboard"
echo "[kcp-dashboard] run: kcp-dashboard"
