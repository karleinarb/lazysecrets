package main

import "runtime/debug"

// version is set by release builds:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/lazysecrets
var version string

// versionString is the release version, or for other builds what Go
// recorded: the module version (go install …@v1.2.3), else the commit.
func versionString() string {
	if version != "" {
		return version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
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
