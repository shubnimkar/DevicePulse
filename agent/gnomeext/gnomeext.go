// Package gnomeext installs and enables the DevicePulse GNOME Shell extension for
// the logged-in user.
//
// On GNOME Wayland (Ubuntu 22.10+ and 24.04) the only reliable way to learn which
// window has focus is a Shell extension: org.gnome.Shell.Eval is disabled and
// Wayland gives other programs no window list. Without it the agent has no focus
// data, so the per-user window service installs the extension itself. GNOME Shell
// loads extensions at startup, so on Wayland the new extension becomes active at
// the next login; Ensure reports that state instead of pretending it works.
//
// Set DEVICEPULSE_NO_EXTENSION_INSTALL=true to opt out.
package gnomeext

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// UUID identifies the extension; it is also its directory name.
const UUID = "devicepulse-focus@waybeyond.tech"

//go:embed assets/extension.js assets/metadata.json
var assets embed.FS

// States reported by Ensure.
const (
	StateRunning          = "running"                  // the extension is active and answering
	StateNeedsRelogin     = "needs_relogin"            // installed and enabled; GNOME Shell loads it at next login
	StateDisabledByPolicy = "user_extensions_disabled" // GNOME is configured to ignore user extensions
	StateNotGNOME         = "not_gnome"                // another desktop (or not Linux): nothing to do
	StateOptedOut         = "opted_out"
	StateError            = "error"
)

// Status is the outcome of Ensure. Note is a plain-language explanation that is
// reported to the dashboard when tracking is not yet fully working.
type Status struct {
	State string
	Note  string
}

type deps struct {
	home    string
	getenv  func(string) string
	run     func(name string, args ...string) (string, error)
	running func() bool
}

// Ensure makes sure the extension is installed and enabled for the current user.
// running reports whether the extension currently owns its D-Bus name.
func Ensure(running func() bool) Status {
	home, _ := os.UserHomeDir()
	return ensure(deps{
		home:   home,
		getenv: os.Getenv,
		run: func(name string, args ...string) (string, error) {
			out, err := exec.Command(name, args...).CombinedOutput()
			return strings.TrimSpace(string(out)), err
		},
		running: running,
	})
}

func ensure(d deps) Status {
	if runtime.GOOS != "linux" && d.getenv("DEVICEPULSE_TEST_FORCE_LINUX") != "true" {
		return Status{State: StateNotGNOME}
	}
	if strings.EqualFold(strings.TrimSpace(d.getenv("DEVICEPULSE_NO_EXTENSION_INSTALL")), "true") {
		return Status{State: StateOptedOut, Note: "GNOME focus extension install is disabled (DEVICEPULSE_NO_EXTENSION_INSTALL)"}
	}
	if !strings.Contains(strings.ToUpper(d.getenv("XDG_CURRENT_DESKTOP")), "GNOME") {
		return Status{State: StateNotGNOME}
	}
	if d.home == "" {
		return Status{State: StateError, Note: "cannot find the user's home directory to install the GNOME focus extension"}
	}

	changed, err := installFiles(filepath.Join(d.home, ".local", "share", "gnome-shell", "extensions", UUID))
	if err != nil {
		return Status{State: StateError, Note: "could not install the GNOME focus extension: " + err.Error()}
	}

	enabledNow, err := enableExtension(d)
	if err != nil {
		return Status{State: StateError, Note: "installed the GNOME focus extension but could not enable it: " + err.Error()}
	}

	if out, err := d.run("gsettings", "get", "org.gnome.shell", "disable-user-extensions"); err == nil && strings.TrimSpace(out) == "true" {
		return Status{State: StateDisabledByPolicy,
			Note: "GNOME is set to ignore user extensions, so app tracking cannot work here; an administrator must allow them (disable-user-extensions)"}
	}

	if d.running() {
		if changed {
			return Status{State: StateRunning, Note: "focus extension updated; the new version loads at the next login"}
		}
		return Status{State: StateRunning}
	}
	_ = enabledNow
	return Status{State: StateNeedsRelogin,
		Note: "GNOME focus extension installed; log out and back in to activate it. App tracking is unavailable until then"}
}

// installFiles writes the embedded extension into dir, replacing outdated copies.
// It reports whether anything was written.
func installFiles(dir string) (bool, error) {
	changed := false
	for _, name := range []string{"extension.js", "metadata.json"} {
		want, err := assets.ReadFile("assets/" + name)
		if err != nil {
			return changed, err
		}
		path := filepath.Join(dir, name)
		if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, want) {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return changed, err
		}
		tmp, err := os.CreateTemp(dir, "."+name+"-*")
		if err != nil {
			return changed, err
		}
		_, werr := tmp.Write(want)
		cerr := tmp.Close()
		if werr != nil || cerr != nil {
			os.Remove(tmp.Name())
			return changed, fmt.Errorf("write %s: %v %v", name, werr, cerr)
		}
		if err := os.Chmod(tmp.Name(), 0o644); err != nil {
			os.Remove(tmp.Name())
			return changed, err
		}
		if err := os.Rename(tmp.Name(), path); err != nil {
			os.Remove(tmp.Name())
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

// enableExtension adds the UUID to org.gnome.shell enabled-extensions. Setting the
// key works even before GNOME Shell has discovered the extension (unlike
// `gnome-extensions enable`, which refuses unknown extensions), so it is what
// makes the extension load at the next login. It reports whether it changed the list.
func enableExtension(d deps) (bool, error) {
	out, err := d.run("gsettings", "get", "org.gnome.shell", "enabled-extensions")
	if err != nil {
		return false, fmt.Errorf("gsettings: %v: %s", err, out)
	}
	list := parseStringList(out)
	for _, id := range list {
		if id == UUID {
			return false, nil
		}
	}
	list = append(list, UUID)
	if out, err := d.run("gsettings", "set", "org.gnome.shell", "enabled-extensions", formatStringList(list)); err != nil {
		return false, fmt.Errorf("gsettings set: %v: %s", err, out)
	}
	// Best effort: activates it at once if the Shell already knows the extension.
	_, _ = d.run("gnome-extensions", "enable", UUID)
	return true, nil
}

// parseStringList parses GVariant string-array text such as `['a', 'b']`,
// `@as []` or `[]`.
func parseStringList(s string) []string {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "@as"))
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"))
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.Trim(strings.TrimSpace(part), `'"`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func formatStringList(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = "'" + it + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
