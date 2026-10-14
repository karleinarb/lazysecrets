package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const key = "lazysecrets://azure/kv.vault.azure.net/secrets/db-password"

func TestUpdateAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "state.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(key, func(n *Node) {
		n.Name, n.Path, n.StarredBy, n.Note = "db-password", []string{"prod", "kv"}, []string{"Azure"}, "prod SQL admin"
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLayout(Layout{Split: 8}); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := again.Node(key); !n.StarredFor("Azure") || n.Note != "prod SQL admin" || n.Path[1] != "kv" {
		t.Errorf("reloaded %+v", n)
	}
	if again.Layout().Split != 8 {
		t.Error("layout lost")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", info.Mode().Perm())
	}
}

func TestContextAloneIsntKept(t *testing.T) {
	s, _ := Load("")
	_ = s.Update(key, func(n *Node) { n.StarredBy, n.Name = []string{"Azure"}, "db-password" })
	_ = s.Update(key, func(n *Node) { n.StarredBy = nil })
	if _, ok := s.Nodes()[key]; ok {
		t.Error("a record with only context left was kept")
	}
}

func TestTwoWindowsDontLoseEachOthersChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	a, _ := Load(path)
	b, _ := Load(path)
	_ = a.Update("one", func(n *Node) { n.StarredBy = []string{"Azure"} })
	_ = b.Update("two", func(n *Node) { n.Note = "hi" }) // b hasn't seen "one"
	_ = a.Update("one", func(n *Node) { n.Uses++; n.LastUsed = time.Now() })

	final, _ := Load(path)
	if n := final.Node("one"); !n.StarredFor("Azure") || n.Uses != 1 {
		t.Errorf("one = %+v", n)
	}
	if final.Node("two").Note != "hi" {
		t.Error("b's change lost")
	}
}

func TestNewerVersionReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	newer := `{"version": 99, "nodes": {"one": {"note": "hi"}}}`
	_ = os.WriteFile(path, []byte(newer), 0o600)
	s, err := Load(path)
	if !errors.Is(err, ErrNewer) || s == nil || s.Node("one").Note != "hi" {
		t.Fatalf("Load = %v, %v", s, err)
	}
	_ = s.SetFlag("x", true)
	if b, _ := os.ReadFile(path); string(b) != newer {
		t.Errorf("the newer file was overwritten: %s", b)
	}
}

func TestUseCountsAndPrunes(t *testing.T) {
	s, _ := Load("")
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	ctx := func(n *Node) { n.Name = "x" }
	_ = s.Use("old", now.Add(-100*24*time.Hour), ctx)
	_ = s.Update("old-starred", func(n *Node) { n.StarredBy = []string{"Azure"}; n.Uses = 1 })
	_ = s.Use("a", now.Add(-time.Hour), ctx)
	_ = s.Use("a", now, ctx)
	if n := s.Node("a"); n.Uses != 2 || !n.LastUsed.Equal(now) || n.Name != "x" {
		t.Errorf("a = %+v", n)
	}
	if s.Has("old") {
		t.Error("record unused for 100 days kept")
	}
	if !s.Has("old-starred") {
		t.Error("starred record pruned")
	}

	for i := range usageMax + 5 {
		_ = s.Use(fmt.Sprint("n", i), now.Add(time.Duration(i)*time.Minute), ctx)
	}
	if got := len(s.Nodes()); got != usageMax+1 { // + old-starred
		t.Errorf("%d records, want %d", got, usageMax+1)
	}
	if s.Has("n0") || !s.Has(fmt.Sprint("n", usageMax+4)) {
		t.Error("pruned the wrong end")
	}
}

func TestStarsPerAccount(t *testing.T) {
	var n Node
	if !n.ToggleStar("Azure:alice") || !n.StarredFor("Azure:alice") || n.StarredFor("Azure:bob") {
		t.Errorf("star for alice: %v", n.StarredBy)
	}
	n.ToggleStar("Azure:bob")
	if n.ToggleStar("Azure:alice") || n.StarredFor("Azure:alice") || !n.StarredFor("Azure:bob") {
		t.Errorf("unstar alice: %v", n.StarredBy)
	}
}

func TestUsedLately(t *testing.T) {
	now := time.Now()
	for ago, want := range map[time.Duration]bool{time.Hour: true, 89 * 24 * time.Hour: true, 2 * 365 * 24 * time.Hour: false} {
		if got := (Node{LastUsed: now.Add(-ago)}).UsedLately(now); got != want {
			t.Errorf("used %v ago: %v", ago, got)
		}
	}
	if (Node{}).UsedLately(now) {
		t.Error("never used is lately")
	}
}
