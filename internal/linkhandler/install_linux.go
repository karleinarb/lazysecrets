package linkhandler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// On Linux a desktop entry claims the x-scheme-handler/lazysecrets type,
// and xdg-open passes links to it as %u.

const desktopFile = "lazysecrets-links.desktop"

// AppPath is where the desktop entry is installed.
func AppPath() (string, error) {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "applications", desktopFile), nil
}

// Install registers launch (lazysecrets launch and its flags, the link
// appended) as the handler for lazysecrets:// links, with env set.
func Install(launch []string, env []string) (string, error) {
	path, err := AppPath()
	if err != nil {
		return "", err
	}
	argv := append(append([]string{"env"}, env...), launch...)
	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Lazysecrets Links
Comment=Opens lazysecrets:// links in lazysecrets
Exec=%s %%u
MimeType=x-scheme-handler/lazysecrets;
NoDisplay=true
Terminal=false
%s=%s
`, desktopExec(argv), exeKey, launch[0])
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(entry), 0o644); err != nil {
		return "", err
	}
	if out, err := exec.Command("xdg-mime", "default", desktopFile, "x-scheme-handler/lazysecrets").CombinedOutput(); err != nil {
		return "", fmt.Errorf("xdg-mime: %v: %s", err, strings.TrimSpace(string(out)))
	}
	_ = exec.Command("update-desktop-database", filepath.Dir(path)).Run() // optional
	return path, nil
}

// desktopExec quotes args for a desktop entry's Exec key.
func desktopExec(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		a = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", `\$`, "%", "%%").Replace(a)
		q[i] = `"` + a + `"`
	}
	return strings.Join(q, " ")
}

// exeKey records, in the desktop entry, the lazysecrets it runs.
const exeKey = "X-Lazysecrets-Executable"

// Check reports why lazysecrets:// links don't open lazysecrets, or "" if
// they do (as far as can be told without opening one).
func Check() string {
	path, err := AppPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "lazysecrets:// links don't open lazysecrets yet"
	}
	if out, err := exec.Command("xdg-mime", "query", "default", "x-scheme-handler/lazysecrets").Output(); err == nil &&
		strings.TrimSpace(string(out)) != desktopFile {
		return "lazysecrets:// links open " + strings.TrimSpace(string(out)) + ", not lazysecrets"
	}
	for _, l := range strings.Split(string(b), "\n") {
		if exe, ok := strings.CutPrefix(l, exeKey+"="); ok {
			if _, err := os.Stat(exe); err != nil {
				return "lazysecrets:// links run " + exe + ", which no longer exists"
			}
		}
	}
	return ""
}

// Uninstall removes the desktop entry.
func Uninstall() (string, error) {
	path, err := AppPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("not installed (%s doesn't exist)", path)
	}
	return path, os.Remove(path)
}
