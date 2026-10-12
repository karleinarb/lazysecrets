package app

import (
	"fmt"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// Secrets, keys and certificates with an expiry date are marked in the
// list once they have expired (✗ expired) or will within the configured
// warning period (⚠ 12d), and typing "expir" in the filter finds them.

type expiryState int

const (
	notExpiring expiryState = iota
	expiresSoon
	expired
)

// expiry tells whether o has expired or will within the warning period,
// and how long it has left.
func (m Model) expiry(o provider.Object) (expiryState, time.Duration) {
	if o.Expires.IsZero() {
		return notExpiring, 0
	}
	left := o.Expires.Sub(m.now())
	switch {
	case left <= 0:
		return expired, 0
	case left <= m.expiryWarning:
		return expiresSoon, left
	}
	return notExpiring, left
}

// expiryWord is matched by the filter: "expired" or "expiring".
func (m Model) expiryWord(o provider.Object) string {
	switch state, _ := m.expiry(o); state {
	case expired:
		return "expired"
	case expiresSoon:
		return "expiring"
	}
	return ""
}

// daysLeft is a short "12d", or "5h" on the last day.
func daysLeft(d time.Duration) string {
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", max(1, int(d.Hours())))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
