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

# ── Sync manifests from kcp-commands ─────────────────────────────────────────
# kcp-dashboard serve reads command manifests from ~/.kcp/commands/.
# This syncs the full set (291 files) from the kcp-commands repo so the
# Go hook daemon has complete manifest coverage without needing the Java JAR.

MANIFEST_DIR="${HOME}/.kcp/commands"
EXISTING=$(ls "${MANIFEST_DIR}"/*.yaml 2>/dev/null | wc -l || echo 0)

if [ "${EXISTING}" -lt 200 ]; then
  echo ""
  echo "[kcp-dashboard] syncing command manifests from kcp-commands..."
  mkdir -p "${MANIFEST_DIR}"
  curl -fsSL "https://github.com/Cantara/kcp-commands/archive/refs/heads/main.tar.gz" \
    | tar -xz --strip-components=2 -C "${MANIFEST_DIR}" "kcp-commands-main/commands/"
  COUNT=$(ls "${MANIFEST_DIR}"/*.yaml 2>/dev/null | wc -l || echo 0)
  echo "[kcp-dashboard] manifests synced (${COUNT} files → ${MANIFEST_DIR})"
else
  echo ""
  echo "[kcp-dashboard] manifests already present (${EXISTING} files) — skipping sync"
  echo "  To force re-sync: rm ${MANIFEST_DIR}/*.yaml && re-run this installer"
fi

# ── Done ──────────────────────────────────────────────────────────────────────

echo ""
echo "[kcp-dashboard] installed → ${INSTALL_DIR}/kcp-dashboard"
echo ""
echo "  kcp-dashboard          # live TUI"
echo "  kcp-dashboard serve    # hook daemon (no JVM — port 7734)"
