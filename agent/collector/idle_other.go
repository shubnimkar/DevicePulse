//go:build !linux

package collector

import "time"

// userIdle is only implemented for Linux (GNOME) so far; elsewhere tracking
// proceeds without idle detection.
func userIdle(threshold time.Duration) (idle bool, known bool) { return false, false }
