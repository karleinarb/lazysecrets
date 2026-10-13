package share

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Target is where an opened file goes.
type Target struct {
	File
	Abs    string
	Exists bool
	// Exposed is set when dir is a git repo that doesn't ignore the file:
	// its values could end up in a commit.
	Exposed bool
}

// Plan says where files go under dir, without writing anything. A path
// that leads out of dir through a symlink, or is one, is an error.
func Plan(dir string, files []File) ([]Target, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Target, len(files))
	for i, f := range files {
		if err := CheckPath(f.Path); err != nil {
			return nil, err
		}
		t := Target{File: f, Abs: filepath.Join(root, filepath.FromSlash(f.Path))}
		if err := inside(root, filepath.Dir(t.Abs)); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}
		switch info, err := os.Lstat(t.Abs); {
		case err == nil && info.Mode()&fs.ModeSymlink != 0:
			return nil, fmt.Errorf("%s is a symlink: not writing through it", f.Path)
		case err == nil && !info.Mode().IsRegular():
			return nil, fmt.Errorf("%s isn't a file", f.Path)
		case err == nil:
			t.Exists = true
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
		t.Exposed = exposed(root, f.Path)
		out[i] = t
	}
	return out, nil
}

// inside checks that dir, or the part of it that exists, is under root
// once symlinks are followed.
func inside(root, dir string) error {
	for {
		real, err := filepath.EvalSymlinks(dir)
		if errors.Is(err, fs.ErrNotExist) && dir != root {
			dir = filepath.Dir(dir) // created when writing
			continue
		}
		if err != nil {
			return err
		}
		if real != root && !strings.HasPrefix(real, root+string(filepath.Separator)) {
			return errors.New("leads out of the folder through a symlink")
		}
		return nil
	}
}

// exposed reports whether root is in a git repo that doesn't ignore p.
func exposed(root, p string) bool {
	err := exec.Command("git", "-C", root, "check-ignore", "-q", "--", p).Run()
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 1 // 0: ignored; 128: no repo
}

// Write writes the file, readable by the user only when it's new.
func (t Target) Write() error {
	if err := os.MkdirAll(filepath.Dir(t.Abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(t.Abs, t.Data, 0o600)
}
