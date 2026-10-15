// Package update finds newer lazysecrets releases and installs one in
// place of the running binary: the archive for this OS and CPU, checked
// against the release's checksums.txt, tried (--version), then renamed
// over the binary. A running lazysecrets keeps its old binary until it
// restarts.
//
// Releases are published in more than one place; each is tried in turn
// (GitHub, then git.kebcloud.no), so one being down doesn't stop updates.
package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// DefaultRepo is lazysecrets' home: its source, docs and releases.
// GitHubRepo mirrors it, with the issues and the releases too.
const (
	DefaultRepo = "https://git.kebcloud.no/keb/lazysecrets"
	GitHubRepo  = "https://github.com/karleinarb/lazysecrets"
)

// DefaultServers are where releases are looked for, in order.
var DefaultServers = []string{GitHubRepo, DefaultRepo}

// serverTimeout bounds asking one server for the latest release or its
// checksums, so a server that's down leaves time for the next.
const serverTimeout = 5 * time.Second

// maxDownload bounds what's read from the server.
const maxDownload = 200 << 20

// Release is a published release.
type Release struct {
	Tag string `json:"tag_name"`
	// Body is the changelog section, then install notes. From Latest,
	// it's the changelogs of the recent releases, each under a "## v…"
	// heading, so someone a few versions behind sees all they missed.
	Body       string `json:"body"`
	URL        string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Notes is the changelog since the version current: of each release
// newer than it (just this one for a dev build), without the install
// notes every release ends with.
func (r Release) Notes(current string) string {
	body, _, _ := strings.Cut(r.Body, "## Install")
	var keep, headings []string
	in := true // before any heading: a single release's notes
	for _, l := range strings.Split(body, "\n") {
		if tag, ok := strings.CutPrefix(l, "## "); ok && semver.IsValid(tag) {
			in = Newer(current, tag) || tag == r.Tag
			if in {
				headings = append(headings, l)
			}
		}
		if in {
			keep = append(keep, l)
		}
	}
	notes := strings.Join(keep, "\n")
	if len(headings) == 1 { // the title names it
		notes = strings.Replace(notes, headings[0], "", 1)
	}
	return PlainLinks(strings.TrimSpace(notes))
}

// PlainLinks shows Markdown links as their text, for a terminal.
func PlainLinks(s string) string { return mdLink.ReplaceAllString(s, "$1") }

var mdLink = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)\s]+)\)`)

// Client talks to the release servers.
type Client struct {
	// Servers are the repositories' web addresses, on GitHub or a Gitea
	// or Forgejo server, tried in order; DefaultServers when empty.
	Servers []string
	HTTP    *http.Client
}

func (c *Client) servers() []string {
	if len(c.Servers) > 0 {
		return c.Servers
	}
	return DefaultServers
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

// API is a repository's API: GitHub's for github.com, else Gitea's (and
// Forgejo's).
func API(repo string) (string, error) {
	u, err := url.Parse(strings.TrimSuffix(repo, "/"))
	if err != nil || u.Host == "" || strings.Count(u.Path, "/") != 2 {
		return "", fmt.Errorf("%q isn't a repository's address, like %s", repo, GitHubRepo)
	}
	if u.Host == "github.com" {
		return "https://api.github.com/repos" + u.Path, nil
	}
	return u.Scheme + "://" + u.Host + "/api/v1/repos" + u.Path, nil
}

// Latest is the newest release (pre-releases aside), from the first
// server that answers.
func (c *Client) Latest(ctx context.Context) (Release, error) {
	var errs []error
	for _, repo := range c.servers() {
		r, err := c.latest(ctx, repo)
		if err == nil {
			return r, nil
		}
		errs = append(errs, err)
	}
	return Release{}, errors.Join(errs...)
}

// recentReleases is how many releases Latest reads the changelogs of.
const recentReleases = 10

func (c *Client) latest(ctx context.Context, repo string) (Release, error) {
	api, err := API(repo)
	if err != nil {
		return Release{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, serverTimeout)
	defer cancel()
	// The recent releases in one request (per_page on GitHub, limit on
	// Gitea), for their changelogs.
	b, err := c.get(ctx, fmt.Sprintf("%s/releases?per_page=%d&limit=%[2]d", api, recentReleases), 1<<20)
	if err != nil {
		return Release{}, err
	}
	var all []Release
	if err := json.Unmarshal(b, &all); err != nil {
		return Release{}, fmt.Errorf("reading the releases from %s: %w", repo, err)
	}
	all = slices.DeleteFunc(all, func(r Release) bool { return r.Draft || r.Prerelease || !semver.IsValid(r.Tag) })
	if len(all) == 0 {
		return Release{}, fmt.Errorf("no releases on %s", repo)
	}
	slices.SortFunc(all, func(a, b Release) int { return semver.Compare(b.Tag, a.Tag) })
	var notes strings.Builder
	for _, r := range all {
		section, _, _ := strings.Cut(r.Body, "## Install")
		fmt.Fprintf(&notes, "## %s\n\n%s\n\n", r.Tag, strings.TrimSpace(section))
	}
	latest := all[0]
	latest.Body = strings.TrimSpace(notes.String())
	return latest, nil
}

// Newer reports whether latest is newer than current. A build that isn't
// a version (dev-…) is never offered one.
func Newer(current, latest string) bool {
	current, latest = canonical(current), canonical(latest)
	return semver.IsValid(current) && semver.IsValid(latest) && semver.Compare(latest, current) > 0
}

func canonical(v string) string {
	if v != "" && !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// Managed says which package manager owns exe, if one does: it updates
// lazysecrets there, not lazysecrets itself.
func Managed(exe string) string {
	switch {
	case strings.Contains(exe, "/Cellar/") || strings.HasPrefix(exe, "/opt/homebrew/") || strings.Contains(exe, "/linuxbrew/"):
		return "Homebrew (brew upgrade lazysecrets)"
	case strings.HasPrefix(exe, "/nix/store/"):
		return "Nix"
	case strings.HasPrefix(exe, "/usr/bin/") || strings.HasPrefix(exe, "/bin/"):
		return "your system's package manager"
	}
	return ""
}

// Executable is the running binary's real path.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// Install replaces the binary at exe with r's, for this OS and CPU.
func (c *Client) Install(ctx context.Context, r Release, exe string) error {
	if m := Managed(exe); m != "" {
		return fmt.Errorf("%s is managed by %s: update it there", exe, m)
	}
	if (runtime.GOOS != "darwin" && runtime.GOOS != "linux") || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return fmt.Errorf("no release build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	version := strings.TrimPrefix(r.Tag, "v")
	name := fmt.Sprintf("lazysecrets_%s_%s_%s", version, runtime.GOOS, runtime.GOARCH)
	archive, err := c.download(ctx, r.Tag, name+".tar.gz")
	if err != nil {
		return err
	}
	bin, err := extract(archive, path.Join(name, "lazysecrets"))
	if err != nil {
		return err
	}

	// Next to the binary, so the rename that replaces it is atomic.
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".lazysecrets-update-*")
	if err != nil {
		return fmt.Errorf("can't write next to %s (%w): reinstall it with install.sh, or update as the user who owns it", exe, err)
	}
	defer os.Remove(tmp.Name()) // gone after the rename; cleans up on failure
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	// It must run, and be the version asked for, before it replaces
	// anything.
	out, err := exec.CommandContext(ctx, tmp.Name(), "--version").Output()
	if err != nil || !strings.Contains(string(out), version) {
		return fmt.Errorf("the downloaded %s doesn't run as expected (%q, %v)", r.Tag, strings.TrimSpace(string(out)), err)
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		return fmt.Errorf("replacing %s: %w", exe, err)
	}
	return nil
}

// download fetches a release's file from the first server that has it,
// checked against that server's checksums.txt. A file that doesn't match
// stops there: it's damaged, or not the release.
func (c *Client) download(ctx context.Context, tag, file string) ([]byte, error) {
	var errs []error
	for _, repo := range c.servers() {
		base := strings.TrimSuffix(repo, "/") + "/releases/download/" + tag + "/"
		sctx, cancel := context.WithTimeout(ctx, serverTimeout)
		sums, err := c.get(sctx, base+"checksums.txt", 1<<20)
		cancel()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		want, err := checksum(sums, file)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", repo, err))
			continue
		}
		b, err := c.get(ctx, base+file, maxDownload)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if got := sha256.Sum256(b); hex.EncodeToString(got[:]) != want {
			return nil, fmt.Errorf("%s from %s doesn't match checksums.txt: damaged, or not the release", file, repo)
		}
		return b, nil
	}
	return nil, errors.Join(errs...)
}

func (c *Client) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: larger than expected", url)
	}
	return b, nil
}

// checksum finds file's sha256 in a checksums.txt.
func checksum(sums []byte, file string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == file {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("%s isn't in checksums.txt", file)
}

// extract reads one file from a .tar.gz.
func extract(archive []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s isn't in the archive", name)
		}
		if err != nil {
			return nil, err
		}
		if h.Name == name && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}
