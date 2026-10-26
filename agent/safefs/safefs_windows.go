//go:build windows

package safefs

import "os"

const nonblock = 0

// Windows has no O_NOFOLLOW; the agent's data directory lives under ProgramData
// with installer ACLs, so a Lstat check is the practical guard here.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrSymlink
	}
	return os.OpenFile(path, flag, perm)
}
