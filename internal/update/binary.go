package update

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// Binary is the binary a running lazysecrets started from, to notice a
// newer one installed over it (by install.sh, go install, a package
// manager or another lazysecrets): the running one keeps its old code
// until it restarts.
type Binary struct {
	path string // as started, not resolved: a symlink a package manager moves is followed
	info os.FileInfo
}

// Running is the binary this process runs, as it is now.
func Running() (*Binary, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(exe)
	if err != nil {
		return nil, err
	}
	return &Binary{path: exe, info: info}, nil
}

// Replaced is the version of a binary installed over b since it was last
// looked at, as its --version says; ok is false while it's the same
// file. A look costs a stat; --version runs only when it changed.
func (b *Binary) Replaced(ctx context.Context) (version string, ok bool) {
	info, err := os.Stat(b.path)
	if err != nil || (os.SameFile(b.info, info) && info.ModTime().Equal(b.info.ModTime())) {
		return "", false
	}
	out, err := exec.CommandContext(ctx, b.path, "--version").Output()
	if err != nil {
		return "", false // mid-install, or broken: look again later
	}
	b.info = info
	v, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "lazysecrets ")
	return v, ok
}
