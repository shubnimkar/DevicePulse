#!/usr/bin/env bash
# =============================================================================
# Build a .pkg installer for DevicePulse Agent (macOS, arm64 + amd64)
#
# Requires: pkgbuild, productbuild (Xcode Command Line Tools)
# Called by: packaging/build.sh
# Usage: build_pkg.sh <version> <dist_dir>
#
# The resulting .pkg:
#   - Installs the binary to /usr/local/bin/devicepulse-agent
#   - Installs a launchd plist to /Library/LaunchDaemons/
#   - Starts the agent at boot via launchd (runs as root, no extra user needed)
# =============================================================================
set -euo pipefail

VERSION="${1:?version required}"
DIST_DIR="${2:?dist_dir required}"
ARCH="${3:-arm64}"   # arm64 | amd64  (default arm64 for Apple Silicon)
BINARY="$DIST_DIR/devicepulse-agent-${VERSION}-darwin-${ARCH}"
PKG_NAME="devicepulse-agent"
STAGING="$DIST_DIR/pkg-staging-${VERSION}-${ARCH}"
IDENTIFIER="io.devicepulse.agent"

if [[ ! -f "$BINARY" ]]; then
  echo "  ✗ Binary not found: $BINARY"
  exit 1
fi

if ! command -v pkgbuild &>/dev/null; then
  echo "  ✗ pkgbuild not found. Install Xcode Command Line Tools: xcode-select --install"
  exit 1
fi

# ── Stage files ───────────────────────────────────────────────────────────────
rm -rf "$STAGING"
mkdir -p "$STAGING/usr/local/bin"
mkdir -p "$STAGING/Library/LaunchDaemons"
mkdir -p "$STAGING/var/lib/devicepulse"

install -m 0755 "$BINARY" "$STAGING/usr/local/bin/devicepulse-agent"

# ── launchd plist ─────────────────────────────────────────────────────────────
# LaunchDaemon runs as root at boot, keeps the agent alive (KeepAlive=true).
cat > "$STAGING/Library/LaunchDaemons/${IDENTIFIER}.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${IDENTIFIER}</string>

  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/devicepulse-agent</string>
  </array>

  <!-- Working directory where registration.json and devicepulse.db are stored -->
  <key>WorkingDirectory</key>
  <string>/var/lib/devicepulse</string>

  <!-- Restart automatically if it crashes -->
  <key>KeepAlive</key>
  <true/>

  <!-- Start at boot without requiring a user login -->
  <key>RunAtLoad</key>
  <true/>

  <key>StandardOutPath</key>
  <string>/var/log/devicepulse-agent.log</string>

  <key>StandardErrorPath</key>
  <string>/var/log/devicepulse-agent.log</string>
</dict>
</plist>
EOF

# ── Pre/post install scripts ──────────────────────────────────────────────────
SCRIPTS_DIR="$DIST_DIR/pkg-scripts-${VERSION}-${ARCH}"
rm -rf "$SCRIPTS_DIR"
mkdir -p "$SCRIPTS_DIR"

cat > "$SCRIPTS_DIR/postinstall" <<'EOF'
#!/bin/bash
set -e

DATA_DIR="/var/lib/devicepulse"
PLIST="/Library/LaunchDaemons/io.devicepulse.agent.plist"

# Ensure data directory exists with correct permissions
mkdir -p "$DATA_DIR"
chmod 750 "$DATA_DIR"

# Unload existing daemon if running (handles upgrades cleanly)
if launchctl list | grep -q "io.devicepulse.agent"; then
  launchctl unload "$PLIST" 2>/dev/null || true
fi

# Load and start the daemon
launchctl load -w "$PLIST"

echo ""
echo "╔══════════════════════════════════════════════════════════════════╗"
echo "║           DevicePulse Agent — Installation Complete             ║"
echo "╠══════════════════════════════════════════════════════════════════╣"
echo "║                                                                  ║"
echo "║  The agent is running as root via launchd.                       ║"
echo "║                                                                  ║"
echo "║  ⚠  TWO macOS privacy permissions are required for full          ║"
echo "║     data collection. Please grant them manually:                ║"
echo "║                                                                  ║"
echo "║  1. FULL DISK ACCESS (for browser history)                       ║"
echo "║     System Settings → Privacy & Security → Full Disk Access      ║"
echo "║     → Add: /usr/local/bin/devicepulse-agent                      ║"
echo "║                                                                  ║"
echo "║  2. AUTOMATION (for active window tracking)                      ║"
echo "║     System Settings → Privacy & Security → Automation            ║"
echo "║     → devicepulse-agent → enable System Events                   ║"
echo "║                                                                  ║"
echo "║  On MDM-managed devices, push a TCC configuration profile        ║"
echo "║  to pre-approve both permissions without user interaction.       ║"
echo "║                                                                  ║"
echo "║  Logs: tail -f /var/log/devicepulse-agent.log                    ║"
echo "╚══════════════════════════════════════════════════════════════════╝"
echo ""
exit 0
EOF
chmod 0755 "$SCRIPTS_DIR/postinstall"

cat > "$SCRIPTS_DIR/preinstall" <<'EOF'
#!/bin/bash
# Stop the existing daemon before upgrading the binary
PLIST="/Library/LaunchDaemons/io.devicepulse.agent.plist"
if launchctl list | grep -q "io.devicepulse.agent"; then
  launchctl unload "$PLIST" 2>/dev/null || true
fi
exit 0
EOF
chmod 0755 "$SCRIPTS_DIR/preinstall"

# ── Build the component .pkg ──────────────────────────────────────────────────
COMPONENT_PKG="$DIST_DIR/${PKG_NAME}-component-${VERSION}-${ARCH}.pkg"

pkgbuild \
  --root "$STAGING" \
  --identifier "$IDENTIFIER" \
  --version "$VERSION" \
  --scripts "$SCRIPTS_DIR" \
  --install-location "/" \
  "$COMPONENT_PKG"

# ── Wrap into a distributable .pkg (productbuild) ────────────────────────────
OUTPUT="$DIST_DIR/${PKG_NAME}-${VERSION}-darwin-${ARCH}.pkg"

productbuild \
  --package "$COMPONENT_PKG" \
  "$OUTPUT"

# Clean up intermediate artifacts
rm -f "$COMPONENT_PKG"
rm -rf "$STAGING" "$SCRIPTS_DIR"

echo "  ✓ $OUTPUT"
echo ""
echo "  To install: sudo installer -pkg $OUTPUT -target /"
echo "  To uninstall:"
echo "    sudo launchctl unload /Library/LaunchDaemons/io.devicepulse.agent.plist"
echo "    sudo rm /usr/local/bin/devicepulse-agent"
echo "    sudo rm /Library/LaunchDaemons/io.devicepulse.agent.plist"
echo "    sudo rm -rf /var/lib/devicepulse"
