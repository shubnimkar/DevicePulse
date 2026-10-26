package main

import "strings"

// Apps reach the API under different spellings depending on how the agent found
// them: the GNOME extension reports the window class ("Code", "Google-chrome"),
// older agents the process name ("code", "chrome"). Without normalising, one app
// would be split into several rows and its time spread across them.

// appAliases maps lower-cased spellings to the name already used in history.
var appAliases = map[string]string{
	"google-chrome":        "chrome",
	"google-chrome-stable": "chrome",
	"chromium-browser":     "chromium",
	"firefox_firefox":      "firefox",
	"org.mozilla.firefox":  "firefox",
	"firefox-esr":          "firefox",
}

// displayAppName returns the name to show: trimmed, with known aliases resolved.
func displayAppName(name string) string {
	name = strings.TrimSpace(name)
	if alias, ok := appAliases[strings.ToLower(name)]; ok {
		return alias
	}
	return name
}

// appKey is the case-insensitive identity used to merge rows for the same app.
func appKey(name string) string { return strings.ToLower(displayAppName(name)) }
