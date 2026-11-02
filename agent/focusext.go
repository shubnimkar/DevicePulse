package main

import (
	"log"
	"os"
	"time"

	"github.com/devicepulse/agent/collector"
	"github.com/devicepulse/agent/gnomeext"
)

// maintainFocusExtension keeps the GNOME focus extension installed and enabled for
// the logged-in user, and tells the tracker (and so the dashboard) while focus
// tracking is not fully working, for example until the user logs in again.
func maintainFocusExtension(tracker *collector.ActiveWindowTracker) {
	last := ""
	for {
		// Started before the desktop session existed, this process has no
		// XDG_CURRENT_DESKTOP and would wrongly conclude it is not on GNOME.
		collector.AdoptSessionEnv()
		st := gnomeext.Ensure(collector.GnomeExtensionRunning)
		tracker.SetFocusNote(st.Note)
		if st.State != last {
			log.Printf("Focus extension: %s %s", st.State, st.Note)
			last = st.State
		}
		switch st.State {
		case gnomeext.StateNotGNOME:
			if os.Getenv("XDG_CURRENT_DESKTOP") == "" {
				time.Sleep(time.Minute) // no session found yet; look again
				continue
			}
			return
		case gnomeext.StateOptedOut:
			return
		case gnomeext.StateRunning:
			time.Sleep(time.Hour) // keep the installed copy current
		default:
			time.Sleep(5 * time.Minute) // waiting for a login, or retrying an error
		}
	}
}
