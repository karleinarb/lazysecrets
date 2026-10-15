package match

import (
	"slices"
	"testing"
)

func names(items []Item, hits []Hit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, items[h.Index].Text)
	}
	return out
}

func plain(texts ...string) []Item {
	var out []Item
	for _, t := range texts {
		out = append(out, Item{Text: t, NameEnd: len([]rune(t))})
	}
	return out
}

func TestRanking(t *testing.T) {
	for _, c := range []struct {
		query string
		items []string
		want  []string // the order of the hits
	}{
		// A word that's there beats letters scattered through a name.
		{"prod", []string{"p-r-o-d-ucer", "kv-app-prod"}, []string{"kv-app-prod", "p-r-o-d-ucer"}},
		// At the start of a word beats inside one.
		{"pass", []string{"bypass-token", "db-password"}, []string{"db-password", "bypass-token"}},
		// Equal scores: the shorter first.
		{"db", []string{"db-password-old-backup", "db-password"}, []string{"db-password", "db-password-old-backup"}},
		// Terms in any order, all of them.
		{"pass prod", []string{"kv-app-prod/db-password", "kv-app-dev/db-password", "kv-app-prod/api-key"}, []string{"kv-app-prod/db-password"}},
		// Either.
		{"pass | key", []string{"db-password", "api-key", "storage-account"}, []string{"db-password", "api-key"}},
		// Exact, prefix, suffix, without.
		{"'prod", []string{"p-r-o-d", "kv-prod"}, []string{"kv-prod"}},
		{"^kv-", []string{"old-kv-app", "kv-app"}, []string{"kv-app"}},
		{"-dev$", []string{"kv-dev-old", "kv-dev"}, []string{"kv-dev"}},
		{"kv !dev", []string{"kv-dev", "kv-prod"}, []string{"kv-prod"}},
		// Smartcase.
		{"API", []string{"api-key", "API-KEY"}, []string{"API-KEY"}},
		{"api", []string{"api-key", "API-KEY"}, []string{"api-key", "API-KEY"}},
	} {
		items := plain(c.items...)
		if got := names(items, Rank(Parse(c.query), items)); !slices.Equal(got, c.want) {
			t.Errorf("%q: %v, want %v", c.query, got, c.want)
		}
	}
}

func TestNameBeatsContext(t *testing.T) {
	// "secret · note": a hit in the name beats the same hit in the note.
	items := []Item{
		{Text: "api-key the password for the api", NameEnd: len("api-key")},
		{Text: "sub / vault / password", NameStart: len("sub / vault / "), NameEnd: len("sub / vault / password")},
	}
	got := names(items, Rank(Parse("password"), items))
	if got[0] != "sub / vault / password" {
		t.Errorf("%v: the name should win over a note", got)
	}
}

func TestPositions(t *testing.T) {
	items := plain("kv-app-prød")
	hits := Rank(Parse("prød"), items)
	if len(hits) != 1 || !slices.Equal(hits[0].Pos, []int{7, 8, 9, 10}) {
		t.Errorf("%+v: want rune positions 7-10", hits)
	}
	hits = Rank(Parse("^kv"), items)
	if len(hits) != 1 || !slices.Equal(hits[0].Pos, []int{0, 1}) {
		t.Errorf("prefix: %+v", hits)
	}
}

func TestAnchorsOnTheName(t *testing.T) {
	path := func(p, name string) Item {
		return Item{Text: p + name + " · ops", NameStart: len([]rune(p)), NameEnd: len([]rune(p + name))}
	}
	items := []Item{
		path("󰠅 platform-prod / ", "kv-app-prod"),
		path("󰠅 platform-prod / kv-app-prod / ", "db-password"),
		path("󰠅 platform-dev / ", "kv-app-dev"),
	}
	got := names(items, Rank(Parse("^kv- !dev"), items))
	if len(got) != 1 || got[0] != items[0].Text {
		t.Errorf("^kv- !dev: %v, want just the kv-app-prod vault", got)
	}
	hits := Rank(Parse("^kv"), items[:1])
	start := len([]rune("󰠅 platform-prod / "))
	if len(hits) != 1 || !slices.Equal(hits[0].Pos, []int{start, start + 1}) {
		t.Errorf("^kv positions %v, want %d-%d", hits, start, start+1)
	}
	if got := names(items, Rank(Parse("password$"), items)); len(got) != 1 || got[0] != items[1].Text {
		t.Errorf("password$: %v", got)
	}
}
