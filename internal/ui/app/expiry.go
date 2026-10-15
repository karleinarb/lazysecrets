package app

import (
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/expiry"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// Secrets, keys and certificates with an expiry date are marked in the
// list once they have expired (✗ expired) or will within the configured
// warning period (⚠ 12d), and typing "expir" in the filter finds them.

// expiry tells whether o has expired or will within the warning period,
// and how long it has left.
func (m Model) expiry(o provider.Object) (expiry.State, time.Duration) {
	return expiry.Of(o.Expires, m.now(), m.expiryWarning)
}

// expiryWord is matched by the filter: "expired" or "expiring".
func (m Model) expiryWord(o provider.Object) string {
	switch state, _ := m.expiry(o); state {
	case expiry.Expired:
		return "expired"
	case expiry.Soon:
		return "expiring"
	}
	return ""
}
