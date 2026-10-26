//go:build !windows

package safefs

import (
	"os"
	"syscall"
)

// nonblock keeps an open on a FIFO planted at the path from blocking forever.
const nonblock = syscall.O_NONBLOCK

func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
}
