package linkhandler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// On macOS a URL scheme belongs to an app bundle, and links arrive as an
// Apple Event rather than an argument. A small AppleScript applet,
// registered for the scheme, passes them on to "lazysecrets launch".

const bundleID = "no.kebcloud.lazysecrets.links"

// AppPath is where the applet is installed.
func AppPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Applications", "Lazysecrets Links.app"), nil
}

const lsregister = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

// Install registers launch (lazysecrets launch and its flags, the link
// appended) as the handler for lazysecrets:// links. env is set for it,
// since apps start with a minimal environment (no Homebrew on PATH, so no
// az).
func Install(launch []string, env []string) (string, error) {
	app, err := AppPath()
	if err != nil {
		return "", err
	}
	if err := buildApp(app, launch, env); err != nil {
		return "", err
	}
	if err := run(lsregister, "-f", app); err != nil {
		return "", err
	}
	return app, nil
}

// buildApp writes the applet to app.
func buildApp(app string, launch []string, env []string) error {
	cmd := shellJoin(append(append([]string{"/usr/bin/env"}, env...), launch...))
	script := fmt.Sprintf(`on open location theURL
	do shell script %s & " " & quoted form of theURL & " >/dev/null 2>&1 &"
end open location

on run
	display dialog "Lazysecrets Links opens lazysecrets:// links in lazysecrets. It has nothing to show on its own." buttons {"OK"} default button 1 with title "Lazysecrets Links"
end run
`, appleString(cmd))

	if err := os.MkdirAll(filepath.Dir(app), 0o755); err != nil {
		return err
	}
	_ = os.RemoveAll(app)
	f, err := os.CreateTemp("", "lazysecrets-links-*.applescript")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	src := f.Name()
	plist := filepath.Join(app, "Contents", "Info.plist")
	steps := [][]string{
		{"osacompile", "-o", app, src},
		{"plutil", "-replace", "CFBundleIdentifier", "-string", bundleID, plist},
		{"plutil", "-replace", "LSUIElement", "-bool", "true", plist}, // no Dock icon
		{"plutil", "-replace", exeKey, "-string", launch[0], plist},   // for Check
		{"plutil", "-replace", "CFBundleURLTypes", "-json",
			`[{"CFBundleURLName":"lazysecrets link","CFBundleURLSchemes":["lazysecrets"]}]`, plist},
		// Editing Info.plist breaks osacompile's signature; sign again,
		// ad hoc, so macOS runs it.
		{"codesign", "--force", "--sign", "-", app},
	}
	for _, s := range steps {
		if err := run(s...); err != nil {
			return err
		}
	}
	return nil
}

func run(argv ...string) error {
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %v: %s", filepath.Base(argv[0]), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// exeKey records, in the applet's Info.plist, the lazysecrets it runs.
const exeKey = "LazysecretsExecutable"

// Check reports why lazysecrets:// links don't open lazysecrets, or "" if
// they do (as far as can be told without opening one).
func Check() string {
	app, err := AppPath()
	if err != nil {
		return ""
	}
	if _, err := os.Stat(app); err != nil {
		return "lazysecrets:// links don't open lazysecrets yet"
	}
	out, err := exec.Command("plutil", "-extract", exeKey, "raw", filepath.Join(app, "Contents", "Info.plist")).Output()
	if err != nil {
		return "" // can't tell: assume it works
	}
	exe := strings.TrimSpace(string(out))
	if _, err := os.Stat(exe); err != nil {
		return "lazysecrets:// links run " + exe + ", which no longer exists"
	}
	return ""
}

// Uninstall removes the applet and its registration.
func Uninstall() (string, error) {
	app, err := AppPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(app); os.IsNotExist(err) {
		return "", fmt.Errorf("not installed (%s doesn't exist)", app)
	}
	_ = exec.Command(lsregister, "-u", app).Run()
	return app, os.RemoveAll(app)
}
