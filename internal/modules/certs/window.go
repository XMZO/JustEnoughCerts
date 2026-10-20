package certs

import (
	"strconv"
	"strings"
	"time"
)

// Deploy windows: a new version of a certificate can wait for hours set
// aside, as at night, before it goes to its targets, as reloading a server
// or a CDN may disturb. A person who asks deploys at once.

// deployWindows are the windows to choose from, "" for any time.
var deployWindows = []string{"", "0-6", "1-5", "2-6", "22-6"}

// parseWindow reads a window as "22-6": from the hour from to the hour to.
func parseWindow(s string) (from, to int, ok bool) {
	a, b, found := strings.Cut(s, "-")
	if !found {
		return 0, 0, false
	}
	from, err1 := strconv.Atoi(a)
	to, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || from < 0 || from > 23 || to < 0 || to > 23 || from == to {
		return 0, 0, false
	}
	return from, to, true
}

// windowStart is when a deployment due at now may start in window: now
// within it, or else the window's next start, in the time zone loc.
func windowStart(window string, now time.Time, loc *time.Location) time.Time {
	from, to, ok := parseWindow(window)
	if !ok {
		return now
	}
	t := now.In(loc)
	h := t.Hour()
	if from < to && h >= from && h < to || from > to && (h >= from || h < to) {
		return now
	}
	start := time.Date(t.Year(), t.Month(), t.Day(), from, 0, 0, 0, loc)
	if !start.After(t) {
		start = start.AddDate(0, 0, 1)
	}
	return start
}
