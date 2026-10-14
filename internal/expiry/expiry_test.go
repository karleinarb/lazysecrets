package expiry

import (
	"testing"
	"time"
)

func TestOf(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	warn := 30 * 24 * time.Hour
	for _, c := range []struct {
		expires time.Time
		want    State
		left    string
	}{
		{time.Time{}, None, ""},
		{now.Add(-time.Hour), Expired, ""},
		{now.Add(12*24*time.Hour + time.Hour), Soon, "12d"},
		{now.Add(5 * time.Hour), Soon, "5h"},
		{now.Add(60 * 24 * time.Hour), None, ""},
	} {
		state, left := Of(c.expires, now, warn)
		if state != c.want || (c.left != "" && Left(left) != c.left) {
			t.Errorf("%v: %v %s, want %v %s", c.expires, state, Left(left), c.want, c.left)
		}
	}
}
