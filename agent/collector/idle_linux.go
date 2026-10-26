//go:build linux

package collector

import (
	"context"
	"time"

	dbus "github.com/godbus/dbus/v5"
)

// userIdle reports whether the user is away: the GNOME screen is locked, or no
// keyboard/pointer input for at least threshold. known is false when the desktop
// does not answer (not GNOME, no session bus), in which case tracking is unchanged.
func userIdle(threshold time.Duration) (idle bool, known bool) {
	if threshold <= 0 {
		return false, false
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// A locked screen counts as away at once, whatever the idle clock says.
	var locked bool
	if err := conn.Object("org.gnome.ScreenSaver", "/org/gnome/ScreenSaver").
		CallWithContext(ctx, "org.gnome.ScreenSaver.GetActive", 0).Store(&locked); err == nil && locked {
		return true, true
	}

	var ms uint64
	if err := conn.Object("org.gnome.Mutter.IdleMonitor", "/org/gnome/Mutter/IdleMonitor/Core").
		CallWithContext(ctx, "org.gnome.Mutter.IdleMonitor.GetIdletime", 0).Store(&ms); err != nil {
		return false, false
	}
	return time.Duration(ms)*time.Millisecond >= threshold, true
}
