#!/usr/bin/env bash
# =============================================================================
# DevicePulse GNOME Shell extension installer (run as the logged-in user).
#
# Required for accurate app-usage tracking on GNOME Wayland (Ubuntu 22.10+ /
# 24.04), where org.gnome.Shell.Eval is blocked by default since GNOME 41.
# The extension exposes org.devicepulse.Shell.GetFocusedWindow on the session
# bus; the agent queries it before falling back to legacy detection paths.
# =============================================================================
set -euo pipefail

UUID="devicepulse-focus@waybeyond.tech"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC_DIR="${SCRIPT_DIR}/${UUID}"
DEST="${HOME}/.local/share/gnome-shell/extensions/${UUID}"

if [[ ! -f "${SRC_DIR}/extension.js" || ! -f "${SRC_DIR}/metadata.json" ]]; then
    echo "ERROR: extension files not found under ${SRC_DIR}" >&2
    exit 1
fi

mkdir -p "$(dirname "${DEST}")"
rm -rf "${DEST}"
cp -r "${SRC_DIR}" "${DEST}"

if command -v gnome-extensions >/dev/null 2>&1; then
    gnome-extensions enable "${UUID}" \
        || echo "WARN: could not auto-enable ${UUID}; enable it via the Extensions app after restarting the shell."
else
    echo "WARN: gnome-extensions CLI not found; enable ${UUID} via the Extensions app."
fi

echo "Installed ${UUID} -> ${DEST}"
echo "Wayland: log out and back in (X11: Alt+F2, type 'r') to restart GNOME Shell."
echo "Verify:  busctl --user call org.devicepulse.Shell /org/devicepulse/Shell org.devicepulse.Shell GetFocusedWindow"
