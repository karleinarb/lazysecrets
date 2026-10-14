// Package expiry says whether a secret, key or certificate has expired
// or will soon, and how long it has left, the same way everywhere: the
// TUI's lists and status pane, and lazysecrets list.
package expiry

import (
	"fmt"
	"time"
)

type State int

const (
	None    State = iota // no expiry, or not within the warning period
	Soon                 // within the warning period
	Expired              // already
)

// Of tells whether something expiring at expires (zero: never) has
// expired by now or will within warn, and how long it has left.
func Of(expires, now time.Time, warn time.Duration) (State, time.Duration) {
	if expires.IsZero() {
		return None, 0
	}
	left := expires.Sub(now)
	switch {
	case left <= 0:
		return Expired, 0
	case left <= warn:
		return Soon, left
	}
	return None, left
}

// Left is a short "12d", or "5h" on the last day.
func Left(d time.Duration) string {
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", max(1, int(d.Hours())))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
