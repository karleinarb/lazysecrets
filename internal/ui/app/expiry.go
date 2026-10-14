package app

import (
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/expiry"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// Secrets, keys and certificates with an expiry date are marked in the
// list once they have expired (✗ expired) or will within the configured
// warning period (⚠ 12d), and typing "expir" in the filter finds them.

type expiryState = expiry.State

const (
	notExpiring = expiry.None
	expiresSoon = expiry.Soon
	expired     = expiry.Expired
)

// expiry tells whether o has expired or will within the warning period,
// and how long it has left.
func (m Model) expiry(o provider.Object) (expiryState, time.Duration) {
	return expiry.Of(o.Expires, m.now(), m.expiryWarning)
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
func daysLeft(d time.Duration) string { return expiry.Left(d) }
