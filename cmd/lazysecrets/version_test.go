package main

import "testing"

func TestPseudoVersions(t *testing.T) {
	for v, want := range map[string]string{
		"v0.1.3-0.20261014174204-1c31a75d0507":       "1c31a75d0507",
		"v0.1.3-0.20261014174204-1c31a75d0507+dirty": "1c31a75d0507",
		"v0.0.0-20261014174204-1c31a75d0507":         "1c31a75d0507",
		"v0.2.0-rc.1.0.20261014174204-1c31a75d0507":  "1c31a75d0507",
		"v0.1.2":      "",
		"v0.2.0-rc.1": "",
	} {
		got := ""
		if m := pseudo.FindStringSubmatch(v); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("%s: %q, want %q", v, got, want)
		}
	}
}
