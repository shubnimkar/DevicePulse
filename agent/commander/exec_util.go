package commander

import (
	"os/exec"
	"strings"
)

// execCommand runs an external command and returns its combined output.
func execCommand(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
