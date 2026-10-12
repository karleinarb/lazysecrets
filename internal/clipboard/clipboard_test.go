package clipboard

import "testing"

func fakeNative() (*Clipboard, *string) {
	var board string
	return &Clipboard{
		native: true,
		read:   func() (string, error) { return board, nil },
		write:  func(s string) error { board = s; return nil },
	}, &board
}

func TestClearOnlyIfUnchanged(t *testing.T) {
	c, board := fakeNative()
	c.Write("secret")()
	if *board != "secret" {
		t.Fatalf("board = %q", *board)
	}
	fp := Fingerprint("secret")

	*board = "something the user copied later"
	c.Clear(fp)()
	if *board != "something the user copied later" {
		t.Error("cleared text the user copied afterwards")
	}

	*board = "secret"
	c.Clear(fp)()
	if *board != "" {
		t.Errorf("not cleared: %q", *board)
	}
}
