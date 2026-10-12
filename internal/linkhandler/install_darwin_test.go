package linkhandler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The applet builds, claims the scheme and passes its signature check. It
// isn't registered, so the system's handler is untouched.
func TestBuildApp(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Lazysecrets Links.app")
	if err := buildApp(app, []string{"/usr/local/bin/lazysecrets", "launch", "--terminal", "kitty"}, []string{"PATH=/opt/homebrew/bin:/usr/bin"}); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(app, "Contents", "Info.plist")
	out, err := exec.Command("plutil", "-extract", "CFBundleURLTypes.0.CFBundleURLSchemes.0", "raw", plist).Output()
	if err != nil || strings.TrimSpace(string(out)) != "lazysecrets" {
		t.Errorf("scheme = %q, %v", out, err)
	}
	if out, err := exec.Command("codesign", "--verify", "--strict", app).CombinedOutput(); err != nil {
		t.Errorf("signature: %v: %s", err, out)
	}
	src, err := exec.Command("osadecompile", filepath.Join(app, "Contents", "Resources", "Scripts", "main.scpt")).Output()
	if err != nil || !strings.Contains(string(src), `'/usr/bin/env' 'PATH=/opt/homebrew/bin:/usr/bin' '/usr/local/bin/lazysecrets' 'launch'`) {
		t.Errorf("script:\n%s %v", src, err)
	}
}

// Check, against an applet in a temporary home (never registered).
func TestCheck(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if r := Check(); !strings.Contains(r, "don't open lazysecrets yet") {
		t.Errorf("nothing installed: %q", r)
	}
	app, _ := AppPath()
	exe := filepath.Join(t.TempDir(), "lazysecrets")
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := buildApp(app, []string{exe, "launch"}, nil); err != nil {
		t.Fatal(err)
	}
	if r := Check(); r != "" {
		t.Errorf("installed: %q", r)
	}
	os.Remove(exe)
	if r := Check(); !strings.Contains(r, "no longer exists") {
		t.Errorf("binary gone: %q", r)
	}
}
