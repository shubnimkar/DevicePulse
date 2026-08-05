#!/usr/bin/env bash
# =============================================================================
# Build a .rpm package for DevicePulse Agent (x86_64)
#
# Requires: rpmbuild (ships with rpm-build on RHEL/Fedora/CentOS)
# Called by: packaging/build.sh
# Usage: build_rpm.sh <version> <dist_dir>
# =============================================================================
set -euo pipefail

VERSION="${1:?version required}"
DIST_DIR="${2:?dist_dir required}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARCH="amd64"
RPM_ARCH="x86_64"
BINARY="$DIST_DIR/devicepulse-agent-${VERSION}-linux-${ARCH}"
PKG_NAME="devicepulse-agent"
BUILD_ROOT="$DIST_DIR/rpm-buildroot-${VERSION}"

if [[ ! -f "$BINARY" ]]; then
  echo "  ✗ Binary not found: $BINARY"
  exit 1
fi

if ! command -v rpmbuild &>/dev/null; then
  echo "  ✗ rpmbuild not found. Install with: sudo dnf install rpm-build  OR  sudo yum install rpm-build"
  exit 1
fi

# ── RPM build tree ────────────────────────────────────────────────────────────
rm -rf "$BUILD_ROOT"
mkdir -p "$BUILD_ROOT"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
mkdir -p "$BUILD_ROOT/BUILDROOT/${PKG_NAME}-${VERSION}-1.${RPM_ARCH}/usr/local/bin"
mkdir -p "$BUILD_ROOT/BUILDROOT/${PKG_NAME}-${VERSION}-1.${RPM_ARCH}/usr/lib/systemd/system"
mkdir -p "$BUILD_ROOT/BUILDROOT/${PKG_NAME}-${VERSION}-1.${RPM_ARCH}/usr/lib/systemd/user"
mkdir -p "$BUILD_ROOT/BUILDROOT/${PKG_NAME}-${VERSION}-1.${RPM_ARCH}/var/lib/devicepulse"

install -m 0755 "$BINARY" \
  "$BUILD_ROOT/BUILDROOT/${PKG_NAME}-${VERSION}-1.${RPM_ARCH}/usr/local/bin/devicepulse-agent"

install -m 0644 "$SCRIPT_DIR/devicepulse-agent.service" \
  "$BUILD_ROOT/BUILDROOT/${PKG_NAME}-${VERSION}-1.${RPM_ARCH}/usr/lib/systemd/system/devicepulse-agent.service"

install -m 0644 "$SCRIPT_DIR/devicepulse-agent-window.service" \
  "$BUILD_ROOT/BUILDROOT/${PKG_NAME}-${VERSION}-1.${RPM_ARCH}/usr/lib/systemd/user/devicepulse-agent-window.service"

# ── Spec file ─────────────────────────────────────────────────────────────────
cat > "$BUILD_ROOT/SPECS/${PKG_NAME}.spec" <<EOF
Name:           ${PKG_NAME}
Version:        ${VERSION}
Release:        1%{?dist}
Summary:        DevicePulse endpoint telemetry agent
License:        MIT
BuildArch:      ${RPM_ARCH}

%description
Lightweight agent that streams hardware stats, processes, browser history,
active window tracking, installed apps, open ports, services, USB events,
and OS updates to the DevicePulse API.

%install
# Files are already staged in BUILDROOT by the shell script above
exit 0

%files
%attr(0755, root, root) /usr/local/bin/devicepulse-agent
%attr(0644, root, root) /usr/lib/systemd/system/devicepulse-agent.service
%attr(0644, root, root) /usr/lib/systemd/user/devicepulse-agent-window.service
%dir %attr(0777, root, root) /var/lib/devicepulse

%pre
# Nothing — service runs as root, no dedicated user needed

%post
# Set correct permissions on data directory
chmod 0777 /var/lib/devicepulse
systemctl daemon-reload
systemctl enable devicepulse-agent
systemctl start devicepulse-agent
# Enable window tracker for all users
systemctl --global enable devicepulse-agent-window 2>/dev/null || \
  echo "Note: enable manually with: systemctl --user enable --now devicepulse-agent-window"
echo "DevicePulse Agent installed and started."
echo "Logs: journalctl -u devicepulse-agent -f"

%preun
if [ \$1 -eq 0 ]; then
  systemctl stop devicepulse-agent  || true
  systemctl disable devicepulse-agent || true
  systemctl --global disable devicepulse-agent-window 2>/dev/null || true
fi

%postun
systemctl daemon-reload || true
if [ \$1 -eq 0 ]; then
  rm -rf /var/lib/devicepulse
fi

%changelog
* $(date '+%a %b %d %Y') DevicePulse <support@devicepulse.io> - ${VERSION}-1
- Release ${VERSION}
EOF

# ── Build the .rpm ────────────────────────────────────────────────────────────
rpmbuild \
  --define "_topdir $BUILD_ROOT" \
  --define "_builddir $BUILD_ROOT/BUILD" \
  --define "_buildrootdir $BUILD_ROOT/BUILDROOT" \
  --define "_rpmdir $BUILD_ROOT/RPMS" \
  --define "_srcrpmdir $BUILD_ROOT/SRPMS" \
  -bb "$BUILD_ROOT/SPECS/${PKG_NAME}.spec"

OUTPUT_RPM=$(find "$BUILD_ROOT/RPMS" -name "*.rpm" | head -1)
FINAL="$DIST_DIR/${PKG_NAME}-${VERSION}-1.${RPM_ARCH}.rpm"
mv "$OUTPUT_RPM" "$FINAL"
rm -rf "$BUILD_ROOT"

echo "  ✓ $FINAL"
