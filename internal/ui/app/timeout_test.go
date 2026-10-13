package app

import (
	"strings"
	"testing"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
)

// A provider's own timeout applies to it alone.
func TestTimeoutPerProvider(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{Timeout: 5 * time.Second, Timeouts: map[provider.Kind]time.Duration{provider.KindAzure: 10 * time.Millisecond}},
		fake.Azure(300*time.Millisecond), fake.Databricks(300*time.Millisecond))
	scr := d.screen()
	if !strings.Contains(scr, "✗ Azure: context deadline exceeded") || !strings.Contains(scr, "Databricks (…)") ||
		strings.Contains(scr, "Databricks: context deadline") {
		t.Errorf("want Azure timed out and Databricks still loading:\n%s", scr)
	}
	if got := (timeouts{def: time.Minute}).of(provider.KindRepo); got != time.Minute {
		t.Errorf("no override: %s, want the default", got)
	}
}
