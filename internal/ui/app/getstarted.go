package app

import (
	"errors"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/panes"
	"git.kebcloud.no/keb/lazysecrets/internal/update"
)

// When no account has anything to show, as on a first run, the right
// pane says how to get started: the sign-in for each kind of account
// (y copies it, L runs az login), and the demo.

// nothingFound reports whether every account has loaded, with nothing.
func (m Model) nothingFound() bool {
	if m.sourcesPending > 0 {
		return false
	}
	for _, p := range m.providers {
		if len(m.sourceResults[p.ID()].sources) > 0 {
			return false
		}
	}
	return true
}

// getStartedFields are the rows saying how to get started.
func (m Model) getStartedFields() []panes.Field {
	out := []panes.Field{{Value: "Nothing to show yet: sign in where your secrets are.", Info: true}}
	seen := map[provider.Kind]bool{}
	for _, p := range m.providers {
		if seen[p.Kind()] {
			continue
		}
		seen[p.Kind()] = true
		f := emptyHint(p.Kind())
		var auth *provider.AuthError
		switch err := m.sourceResults[p.ID()].err; {
		case errors.As(err, &auth) && len(auth.Login) > 0:
			f.Provider = p.ID() // L signs in
			out = append(out, f, panes.Field{Value: withKey(m.keys.Login, "%s runs it here"), Info: true})
		case err != nil:
			out = append(out, f, panes.Field{Value: err.Error(), Info: true, Err: true})
		default:
			out = append(out, f)
		}
	}
	return append(out,
		panes.Field{Label: "try", Value: "lazysecrets --demo"},
		panes.Field{Value: "built-in sample data, to look around", Info: true},
		panes.Field{Label: "docs", Value: "getting started", URL: update.DefaultRepo + "#get-started"},
	)
}

// emptyHint is how an account of kind gets something to show.
func emptyHint(kind provider.Kind) panes.Field {
	switch kind {
	case provider.KindDatabricks:
		return panes.Field{Label: "Databricks", Value: "databricks auth login --host https://<workspace>"}
	case provider.KindRepo:
		return panes.Field{Label: "repos", Value: "SOPS and .env files in your git repos",
			URL: update.DefaultRepo + "/src/branch/main/docs/repos.md"}
	}
	return panes.Field{Label: string(kind), Value: "az login"}
}
