// Package lazysecrets holds what the module's root has for the app: the
// changelog, embedded so the status pane can show it offline.
package lazysecrets

import _ "embed"

// Changelog is CHANGELOG.md as of the build: every release up to it.
//
//go:embed CHANGELOG.md
var Changelog string
