// Package match filters and ranks lists the way fzf (and snacks.nvim's
// picker, a port of it) does: fzf's own scoring, so a match at the start
// of a word or in one run of characters beats letters scattered through a
// name, and fzf's extended search syntax:
//
//	prod db     both terms, in any order
//	db | pw     either
//	'prod       exactly "prod", not fuzzy
//	^kv-  -dev$ starts with / ends with
//	!test       without "test"
//
// ^ and $ anchor to an item's name, the part of it matches count most
// for (a secret's name rather than the path to it). Lowercase ignores case; an uppercase letter makes that term exact
// about case (smartcase). Results sort by score, then the shorter text,
// then their order in the list.
package match

import (
	"slices"
	"strings"

	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"
)

func init() { algo.Init("default") }

// nameBonus is added to a term's score when all its letters are in the
// item's name rather than in its context (a path, a note), like snacks'
// filename_bonus: "pass" should find db-password before a secret that
// only lives in a vault called "passwords".
const nameBonus = 24

type kind int

const (
	fuzzy kind = iota
	exact
	prefix
	suffix
	equal
)

type term struct {
	kind          kind
	inverse       bool
	caseSensitive bool
	text          []rune
}

// Query is a parsed search: every group must match (AND), a group
// matches when any of its terms does (OR).
type Query struct {
	groups [][]term
}

// Parse reads a search in fzf's extended syntax.
func Parse(q string) Query {
	var out Query
	or := false
	for _, tok := range strings.Fields(q) {
		if tok == "|" {
			or = true
			continue
		}
		t, ok := parseTerm(tok)
		if !ok {
			continue
		}
		if n := len(out.groups); or && n > 0 {
			out.groups[n-1] = append(out.groups[n-1], t)
		} else {
			out.groups = append(out.groups, []term{t})
		}
		or = false
	}
	return out
}

func parseTerm(tok string) (term, bool) {
	t := term{kind: fuzzy}
	if s, ok := strings.CutPrefix(tok, "!"); ok {
		t.inverse, t.kind, tok = true, exact, s
	}
	switch {
	case strings.HasPrefix(tok, "'"):
		t.kind, tok = exact, tok[1:]
	case strings.HasPrefix(tok, "^"):
		t.kind, tok = prefix, tok[1:]
	}
	if s, ok := strings.CutSuffix(tok, "$"); ok && s != "" {
		if t.kind == prefix {
			t.kind = equal
		} else {
			t.kind = suffix
		}
		tok = s
	}
	if tok == "" {
		return term{}, false
	}
	t.caseSensitive = strings.ToLower(tok) != tok
	if !t.caseSensitive {
		tok = strings.ToLower(tok)
	}
	t.text = []rune(tok)
	return t, true
}

// Empty reports whether the query has no terms: everything matches.
func (q Query) Empty() bool { return len(q.groups) == 0 }

// Item is a row to match: Text is everything searched, and runes
// NameStart up to NameEnd of it are the row's name (the rest is context,
// like a path or a note). Without a name, nothing gets the name bonus.
type Item struct {
	Text               string
	NameStart, NameEnd int
}

// Hit is a matching item: its index in the list, score, and the rune
// positions of the matched letters in its Text.
type Hit struct {
	Index int
	Score int
	Pos   []int
}

// Rank returns the items matching q, best first.
func Rank(q Query, items []Item) []Hit {
	var hits []Hit
	for i, it := range items {
		if h, ok := q.match(it); ok {
			h.Index = i
			hits = append(hits, h)
		}
	}
	slices.SortStableFunc(hits, func(a, b Hit) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return len(items[a.Index].Text) - len(items[b.Index].Text)
	})
	return hits
}

func (q Query) match(it Item) (Hit, bool) {
	chars := util.ToChars([]byte(it.Text))
	// ^ and $ anchor to the name: "vaults starting with kv-", not to the
	// path before it.
	name := chars
	if it.NameEnd > 0 {
		runes := []rune(it.Text)
		name = util.RunesToChars(runes[it.NameStart:min(it.NameEnd, len(runes))])
	}
	var h Hit
	for _, group := range q.groups {
		best, matched := 0, false
		var bestPos []int
		for _, t := range group {
			in, offset := &chars, 0
			if t.kind == prefix || t.kind == suffix || t.kind == equal {
				in, offset = &name, it.NameStart
			}
			score, pos, ok := t.match(in, offset, it.NameStart, it.NameEnd)
			if ok && (!matched || score > best) {
				best, bestPos, matched = score, pos, true
			}
		}
		if !matched {
			return Hit{}, false
		}
		h.Score += best
		h.Pos = append(h.Pos, bestPos...)
	}
	slices.Sort(h.Pos)
	h.Pos = slices.Compact(h.Pos)
	return h, true
}

// match matches t in chars, whose first rune is at offset in the item's
// text: positions are in the text.
func (t term) match(chars *util.Chars, offset, nameStart, nameEnd int) (int, []int, bool) {
	fn := algo.FuzzyMatchV2
	switch t.kind {
	case exact:
		fn = algo.ExactMatchNaive
	case prefix:
		fn = algo.PrefixMatch
	case suffix:
		fn = algo.SuffixMatch
	case equal:
		fn = algo.EqualMatch
	}
	res, pos := fn(t.caseSensitive, false, true, chars, t.text, !t.inverse, nil)
	found := res.Start >= 0
	if t.inverse {
		return 0, nil, !found
	}
	if !found {
		return 0, nil, false
	}
	var p []int
	if pos != nil {
		for _, i := range *pos {
			p = append(p, i+offset)
		}
	} else { // prefix, suffix and equal give a range
		for i := res.Start; i < res.End; i++ {
			p = append(p, i+offset)
		}
	}
	score := res.Score
	if len(p) > 0 && slices.Min(p) >= nameStart && slices.Max(p) < nameEnd {
		score += nameBonus
	}
	return score, p, true
}
