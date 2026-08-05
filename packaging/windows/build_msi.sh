#!/usr/bin/env bash
# =============================================================================
# Build a .msi installer for DevicePulse Agent (Windows amd64)
#
# Requires: WiX Toolset v3 (candle + light) installed and on PATH.
# Download: https://wixtoolset.org/releases/
#
# This script is designed to run on Linux/macOS with Wine, or natively on
# Windows under Git Bash / WSL. On a native Windows machine use build_msi.bat.
#
# Called by: packaging/build.sh
# Usage: build_msi.sh <version> <dist_dir>
# =============================================================================
set -euo pipefail

VERSION="${1:?version required}"
DIST_DIR="${2:?dist_dir required}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY="$DIST_DIR/devicepulse-agent-${VERSION}-windows-amd64.exe"
WXS="$SCRIPT_DIR/devicepulse-agent.wxs"
BUILD_DIR="$DIST_DIR/msi-build-${VERSION}"
OUTPUT="$DIST_DIR/devicepulse-agent-${VERSION}-windows-amd64.msi"

if [[ ! -f "$BINARY" ]]; then
  echo "  ✗ Binary not found: $BINARY"
  exit 1
fi

# Detect candle/light — support both native and wine-wrapped invocations
if command -v candle &>/dev/null && command -v light &>/dev/null; then
  CANDLE="candle"
  LIGHT="light"
elif command -v candle.exe &>/dev/null && command -v light.exe &>/dev/null; then
  CANDLE="candle.exe"
  LIGHT="light.exe"
else
  echo "  ✗ WiX Toolset (candle/light) not found."
  echo "    Download from https://wixtoolset.org/releases/ and add to PATH."
  echo "    On Linux/macOS you can run it via Wine."
  exit 1
fi

mkdir -p "$BUILD_DIR"

echo "  Compiling WiX source..."
"$CANDLE" \
  -arch x64 \
  -dVersion="$VERSION" \
  -dBinaryPath="$BINARY" \
  -ext WixUtilExtension \
  -out "$BUILD_DIR/devicepulse-agent.wixobj" \
  "$WXS"

echo "  Linking MSI..."
"$LIGHT" \
  -ext WixUIExtension \
  -ext WixUtilExtension \
  -out "$OUTPUT" \
  "$BUILD_DIR/devicepulse-agent.wixobj"

rm -rf "$BUILD_DIR"

echo "  ✓ $OUTPUT"
echo ""
echo "  To install silently:  msiexec /i devicepulse-agent-${VERSION}-windows-amd64.msi /quiet"
echo "  To uninstall silently: msiexec /x devicepulse-agent-${VERSION}-windows-amd64.msi /quiet"
