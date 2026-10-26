package gnomeext

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeEnv struct {
	home    string
	env     map[string]string
	gsets   map[string]string // gsettings key -> current value text
	calls   []string
	running bool
}

func (f *fakeEnv) deps() deps {
	env := map[string]string{"XDG_CURRENT_DESKTOP": "ubuntu:GNOME", "DEVICEPULSE_TEST_FORCE_LINUX": "true"}
	for k, v := range f.env {
		env[k] = v
	}
	return deps{
		home:   f.home,
		getenv: func(k string) string { return env[k] },
		run: func(name string, args ...string) (string, error) {
			f.calls = append(f.calls, name+" "+strings.Join(args, " "))
			if name == "gsettings" && args[0] == "get" {
				return f.gsets[args[2]], nil
			}
			if name == "gsettings" && args[0] == "set" {
				f.gsets[args[2]] = args[3]
			}
			return "", nil
		},
		running: func() bool { return f.running },
	}
}

func newFake(t *testing.T) *fakeEnv {
	return &fakeEnv{home: t.TempDir(), gsets: map[string]string{"enabled-extensions": "@as []", "disable-user-extensions": "false"}}
}

func (f *fakeEnv) sets() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "gsettings set") {
			out = append(out, c)
		}
	}
	return out
}

func TestFreshInstallWritesFilesEnablesAndAsksForRelogin(t *testing.T) {
	f := newFake(t)
	st := ensure(f.deps())
	if st.State != StateNeedsRelogin || !strings.Contains(st.Note, "log out") {
		t.Fatalf("status = %+v", st)
	}
	dir := filepath.Join(f.home, ".local/share/gnome-shell/extensions", UUID)
	for _, name := range []string{"extension.js", "metadata.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s not installed: %v", name, err)
		}
	}
	if got := f.gsets["enabled-extensions"]; got != "['"+UUID+"']" {
		t.Fatalf("enabled-extensions = %q", got)
	}
}

func TestExistingExtensionsAreKept(t *testing.T) {
	f := newFake(t)
	f.gsets["enabled-extensions"] = "['ubuntu-dock@ubuntu.com', 'tiling-assistant@ubuntu.com']"
	ensure(f.deps())
	want := "['ubuntu-dock@ubuntu.com', 'tiling-assistant@ubuntu.com', '" + UUID + "']"
	if f.gsets["enabled-extensions"] != want {
		t.Fatalf("enabled-extensions = %q, want %q", f.gsets["enabled-extensions"], want)
	}
}

func TestRunningAndUpToDateChangesNothing(t *testing.T) {
	f := newFake(t)
	ensure(f.deps()) // installs
	f.running = true
	f.calls = nil
	st := ensure(f.deps())
	if st.State != StateRunning || st.Note != "" {
		t.Fatalf("status = %+v", st)
	}
	if len(f.sets()) != 0 {
		t.Fatalf("must not touch settings again: %v", f.sets())
	}
}

func TestOutdatedCopyIsReplaced(t *testing.T) {
	f := newFake(t)
	ensure(f.deps())
	path := filepath.Join(f.home, ".local/share/gnome-shell/extensions", UUID, "extension.js")
	if err := os.WriteFile(path, []byte("old version"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.running = true
	st := ensure(f.deps())
	if st.State != StateRunning || !strings.Contains(st.Note, "next login") {
		t.Fatalf("status = %+v", st)
	}
	data, _ := os.ReadFile(path)
	if string(data) == "old version" {
		t.Fatal("outdated extension was not replaced")
	}
}

func TestUserExtensionsDisabledByPolicyIsReported(t *testing.T) {
	f := newFake(t)
	f.gsets["disable-user-extensions"] = "true"
	if st := ensure(f.deps()); st.State != StateDisabledByPolicy {
		t.Fatalf("status = %+v", st)
	}
}

func TestNonGNOMEAndOptOutDoNothing(t *testing.T) {
	f := newFake(t)
	f.env = map[string]string{"XDG_CURRENT_DESKTOP": "KDE"}
	if st := ensure(f.deps()); st.State != StateNotGNOME {
		t.Fatalf("status = %+v", st)
	}
	f = newFake(t)
	f.env = map[string]string{"DEVICEPULSE_NO_EXTENSION_INSTALL": "true"}
	if st := ensure(f.deps()); st.State != StateOptedOut {
		t.Fatalf("status = %+v", st)
	}
	for _, f := range []*fakeEnv{f} {
		if _, err := os.Stat(filepath.Join(f.home, ".local")); err == nil {
			t.Fatal("opt-out must not write anything")
		}
	}
}

func TestParseAndFormatStringList(t *testing.T) {
	for in, want := range map[string][]string{
		"@as []":         nil,
		"[]":             nil,
		"['a']":          {"a"},
		"['a', 'b@c.d']": {"a", "b@c.d"},
	} {
		if got := parseStringList(in); !reflect.DeepEqual(got, want) {
			t.Errorf("parseStringList(%q) = %v, want %v", in, got, want)
		}
	}
	if got := formatStringList([]string{"a", "b"}); got != "['a', 'b']" {
		t.Errorf("formatStringList = %q", got)
	}
}

// The agent embeds its own copy of the extension; the packaging copy used by
// install.sh must not drift from it.
func TestEmbeddedExtensionMatchesPackagingCopy(t *testing.T) {
	for _, name := range []string{"extension.js", "metadata.json"} {
		embedded, err := assets.ReadFile("assets/" + name)
		if err != nil {
			t.Fatal(err)
		}
		packaged, err := os.ReadFile(filepath.Join("..", "..", "packaging", "gnome-shell", UUID, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(embedded) != string(packaged) {
			t.Errorf("%s differs between agent/gnomeext/assets and packaging/gnome-shell/%s", name, UUID)
		}
	}
}
