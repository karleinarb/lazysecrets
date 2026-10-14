// Package icons maps backends to Nerd Font glyphs. They are on by default
// and can be turned off, since they render as boxes without a Nerd Font
// (v3+) installed.
package icons

import "git.kebcloud.no/keb/lazysecrets/internal/provider"

type Set struct {
	Azure      string
	Databricks string
	Repo       string
	Encrypted  string // an encrypted file (SOPS)
	PlainFile  string // a plain text secrets file (.env)
	Note       string // has a note (lazysecrets list)
}

// None renders no icons.
var None = Set{}

// NerdFont uses Nerd Fonts v3 Material Design glyphs. There is no Databricks
// logo glyph, so it uses stacked layers, which resembles it.
var NerdFont = Set{
	Azure:      "\U000F0805", // md-microsoft_azure
	Databricks: "\U000F0F58", // md-layers_triple
	Repo:       "\U000F02A2", // md-git
	Encrypted:  "\U000F033E", // md-lock
	PlainFile:  "\U000F0214", // md-file
	Note:       "\U000F09ED",
}

// Prefix returns the icon for kind followed by a space, or "" if the set has
// no icon for it.
func (s Set) Prefix(kind provider.Kind) string {
	var icon string
	switch kind {
	case provider.KindAzure:
		icon = s.Azure
	case provider.KindDatabricks:
		icon = s.Databricks
	case provider.KindRepo:
		icon = s.Repo
	}
	if icon == "" {
		return ""
	}
	return icon + " "
}
