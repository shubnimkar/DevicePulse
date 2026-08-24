#!/usr/bin/env bash
# =============================================================================
# DevicePulse Agent — Master Build Script
#
# Compiles the agent for all supported OS/arch combinations and (optionally)
# produces installer packages.
#
# Usage:
#   ./packaging/build.sh [options]
#
# Options:
#   --version   VERSION     Agent version string  (default: 0.0.1-dev)
#   --api-url   URL         API endpoint to bake in (default: http://localhost:8000)
#   --package               Also build OS-specific installers (.pkg/.deb/.rpm)
#   --platform  PLATFORM    Only build one platform: darwin|linux|windows
#   --help                  Show this help
#
# Examples:
#   # Quick local build, all platforms, no installers
#   ./packaging/build.sh --version 1.0.0 --api-url https://api.example.com
#
#   # Full release build with installers
#   ./packaging/build.sh --version 1.0.0 --api-url https://api.example.com --package
#
#   # macOS only
#   ./packaging/build.sh --version 1.0.0 --platform darwin --package
# =============================================================================

set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
VERSION="0.0.1-dev"
API_URL="http://localhost:8000"
DO_PACKAGE=false
PLATFORM_FILTER=""

# ── Parse arguments ───────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)   VERSION="$2";         shift 2 ;;
    --api-url)   API_URL="$2";         shift 2 ;;
    --package)   DO_PACKAGE=true;      shift   ;;
    --platform)  PLATFORM_FILTER="$2"; shift 2 ;;
    --help)
      sed -n '/^# Usage/,/^# ====/p' "$0" | sed 's/^# \?//'
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# ── Paths ─────────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
AGENT_DIR="$REPO_ROOT/agent"
DIST_DIR="$REPO_ROOT/dist"
BASE_LDFLAGS="-X main.defaultAPIURL=${API_URL} -X main.agentVersion=${VERSION} -s -w"

mkdir -p "$DIST_DIR"

echo "======================================"
echo " DevicePulse Agent Build"
echo " Version : $VERSION"
echo " API URL : $API_URL"
echo " Packages: $DO_PACKAGE"
echo "======================================"

# ── Build targets ─────────────────────────────────────────────────────────────
# Format: "GOOS GOARCH output_suffix"
declare -a TARGETS=(
  "darwin  amd64  darwin-amd64"
  "darwin  arm64  darwin-arm64"
  "linux   amd64  linux-amd64"
  "linux   arm64  linux-arm64"
  "windows amd64  windows-amd64.exe"
)

for target in "${TARGETS[@]}"; do
  read -r GOOS GOARCH SUFFIX <<< "$target"

  # Apply platform filter if set
  if [[ -n "$PLATFORM_FILTER" && "$GOOS" != "$PLATFORM_FILTER" ]]; then
    continue
  fi

  OUTPUT="$DIST_DIR/devicepulse-agent-${VERSION}-${SUFFIX}"
  LDFLAGS="$BASE_LDFLAGS"
  if [[ "$GOOS" == "windows" ]]; then
    LDFLAGS="$LDFLAGS -H=windowsgui"
  fi
  echo ""
  echo "▶ Building $GOOS/$GOARCH → $(basename "$OUTPUT")"

  (cd "$AGENT_DIR" && GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 \
    go build \
      -ldflags "$LDFLAGS" \
      -trimpath \
      -o "$OUTPUT" \
      .)

  # Compute and store SHA-256 checksum alongside the binary
  if command -v shasum &>/dev/null; then
    shasum -a 256 "$OUTPUT" > "${OUTPUT}.sha256"
  elif command -v sha256sum &>/dev/null; then
    sha256sum "$OUTPUT" > "${OUTPUT}.sha256"
  fi

  echo "  ✓ $(du -sh "$OUTPUT" | cut -f1)  ${OUTPUT}.sha256"
done

echo ""
echo "Binaries written to: $DIST_DIR"

# ── Packages ──────────────────────────────────────────────────────────────────
if [[ "$DO_PACKAGE" == true ]]; then
  echo ""
  echo "======================================"
  echo " Building installer packages"
  echo "======================================"

  if [[ -z "$PLATFORM_FILTER" || "$PLATFORM_FILTER" == "darwin" ]]; then
    echo ""
    echo "▶ macOS .pkg"
    bash "$SCRIPT_DIR/macos/build_pkg.sh" "$VERSION" "$DIST_DIR"
  fi

  if [[ -z "$PLATFORM_FILTER" || "$PLATFORM_FILTER" == "linux" ]]; then
    echo ""
    echo "▶ Linux .deb"
    bash "$SCRIPT_DIR/linux/build_deb.sh" "$VERSION" "$DIST_DIR"
    echo ""
    echo "▶ Linux .rpm"
    bash "$SCRIPT_DIR/linux/build_rpm.sh" "$VERSION" "$DIST_DIR"
  fi

  if [[ -z "$PLATFORM_FILTER" || "$PLATFORM_FILTER" == "windows" ]]; then
    echo ""
    echo "▶ Windows .msi (requires WiX Toolset)"
    bash "$SCRIPT_DIR/windows/build_msi.sh" "$VERSION" "$DIST_DIR"
  fi
fi

echo ""
echo "======================================"
echo " Done. Artifacts in: $DIST_DIR"
echo "======================================"
