package main

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// version is set by release builds:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/lazysecrets
var version string

// pseudo matches a Go pseudo-version (v0.1.3-0.20261014174204-1c31a75d0507,
// maybe +dirty): its commit.
var pseudo = regexp.MustCompile(`[-.]\d{14}-([0-9a-f]{12})(\+.*)?$`)

// versionString is the release version, or for other builds what Go
// recorded: the module version (go install …@v1.2.3), else the commit
// (dev-c1a4f90, -dirty with changes not committed).
func versionString() string {
	if version != "" {
		return version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		// A commit, not a release: a local build (Go stamps those too) or
		// go install …@<commit>.
		if m := pseudo.FindStringSubmatch(v); m != nil {
			dirty := ""
			if strings.HasSuffix(v, "+dirty") {
				dirty = "-dirty"
			}
			return "dev-" + m[1][:7] + dirty
		}
		return v
	}
	var rev, dirty string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if len(rev) >= 7 {
		return "dev-" + rev[:7] + dirty
	}
	return "dev"
}
