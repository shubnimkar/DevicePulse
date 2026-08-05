#!/usr/bin/env bash
# =============================================================================
# Build a .deb package for DevicePulse Agent (amd64)
#
# Requires: dpkg-deb (ships with dpkg on Debian/Ubuntu)
# Called by: packaging/build.sh
# Usage: build_deb.sh <version> <dist_dir>
# =============================================================================
set -euo pipefail

VERSION="${1:?version required}"
DIST_DIR="${2:?dist_dir required}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARCH="amd64"
BINARY="$DIST_DIR/devicepulse-agent-${VERSION}-linux-${ARCH}"
PKG_NAME="devicepulse-agent"
PKG_DIR="$DIST_DIR/deb-staging-${VERSION}"

if [[ ! -f "$BINARY" ]]; then
  echo "  ✗ Binary not found: $BINARY (run build.sh without --package first)"
  exit 1
fi

if ! command -v dpkg-deb &>/dev/null; then
  echo "  ✗ dpkg-deb not found. Install with: sudo apt-get install dpkg"
  exit 1
fi

# ── Directory layout ──────────────────────────────────────────────────────────
rm -rf "$PKG_DIR"
mkdir -p "$PKG_DIR/DEBIAN"
mkdir -p "$PKG_DIR/usr/local/bin"
mkdir -p "$PKG_DIR/usr/lib/systemd/system"
mkdir -p "$PKG_DIR/usr/lib/systemd/user"
mkdir -p "$PKG_DIR/var/lib/devicepulse"

# Binary
install -m 0755 "$BINARY" "$PKG_DIR/usr/local/bin/devicepulse-agent"

# System service (runs as root — all telemetry except active window)
install -m 0644 "$SCRIPT_DIR/devicepulse-agent.service" \
  "$PKG_DIR/usr/lib/systemd/system/devicepulse-agent.service"

# User service (runs as logged-in user — active window tracking only)
install -m 0644 "$SCRIPT_DIR/devicepulse-agent-window.service" \
  "$PKG_DIR/usr/lib/systemd/user/devicepulse-agent-window.service"

# ── DEBIAN/control ────────────────────────────────────────────────────────────
cat > "$PKG_DIR/DEBIAN/control" <<EOF
Package: $PKG_NAME
Version: $VERSION
Section: utils
Priority: optional
Architecture: $ARCH
Maintainer: DevicePulse <support@devicepulse.io>
Description: DevicePulse endpoint telemetry agent
 Lightweight agent that streams hardware stats, processes, browser history,
 active window tracking, installed apps, open ports, services, USB events,
 and OS updates to the DevicePulse API.
 .
 Consists of two services:
  - devicepulse-agent.service (system, runs as root)
  - devicepulse-agent-window.service (user, tracks active window per session)
EOF

# ── DEBIAN/postinst ───────────────────────────────────────────────────────────
cat > "$PKG_DIR/DEBIAN/postinst" <<'POSTINST'
#!/bin/bash
set -e

DATA_DIR="/var/lib/devicepulse"

# Create data directory.
# Mode 0777 so the root system service AND the user session service can both
# write to the shared SQLite queue.
mkdir -p "$DATA_DIR"
chmod 0777 "$DATA_DIR"

# ── System service (root) ─────────────────────────────────────────────────────
systemctl daemon-reload
systemctl enable devicepulse-agent
systemctl start devicepulse-agent

# ── User service (active window tracking) ────────────────────────────────────
# Enable globally so it auto-starts for every user that logs in.
# This requires systemd >= 236 (Ubuntu 18.04+, Debian 10+).
if systemctl --global enable devicepulse-agent-window 2>/dev/null; then
  echo "Active window tracking enabled for all users (starts on next login)."
else
  echo "Note: Could not globally enable user service — users can enable manually:"
  echo "  systemctl --user enable --now devicepulse-agent-window"
fi

echo ""
echo "DevicePulse Agent installed and started."
echo "  System service logs : journalctl -u devicepulse-agent -f"
echo "  Window tracker logs : journalctl --user -u devicepulse-agent-window -f"
POSTINST
chmod 0755 "$PKG_DIR/DEBIAN/postinst"

# ── DEBIAN/prerm ──────────────────────────────────────────────────────────────
cat > "$PKG_DIR/DEBIAN/prerm" <<'PRERM'
#!/bin/bash
set -e

# Stop and disable system service
if systemctl is-active --quiet devicepulse-agent 2>/dev/null; then
  systemctl stop devicepulse-agent
fi
systemctl disable devicepulse-agent 2>/dev/null || true

# Disable user service globally
systemctl --global disable devicepulse-agent-window 2>/dev/null || true
PRERM
chmod 0755 "$PKG_DIR/DEBIAN/prerm"

# ── DEBIAN/postrm ─────────────────────────────────────────────────────────────
cat > "$PKG_DIR/DEBIAN/postrm" <<'POSTRM'
#!/bin/bash
set -e

systemctl daemon-reload || true

# On purge, remove data directory
if [[ "$1" == "purge" ]]; then
  rm -rf /var/lib/devicepulse
fi
POSTRM
chmod 0755 "$PKG_DIR/DEBIAN/postrm"

# ── Build the .deb ────────────────────────────────────────────────────────────
OUTPUT="$DIST_DIR/${PKG_NAME}_${VERSION}_${ARCH}.deb"
dpkg-deb --build --root-owner-group "$PKG_DIR" "$OUTPUT"
rm -rf "$PKG_DIR"

echo "  ✓ $OUTPUT"
