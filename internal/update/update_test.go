package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		current, latest string
		want            bool
	}{
		{"0.1.1", "v0.2.0", true},
		{"v0.2.0", "v0.2.0", false},
		{"0.3.0", "v0.2.0", false},
		{"v0.1.2-0.20261009185704-2ba762ce2224", "v0.1.1", false}, // go install of a later commit
		{"v0.1.2-0.20261009185704-2ba762ce2224", "v0.1.2", true},
		{"dev-2ba762c", "v0.2.0", false},
		{"dev", "v0.2.0", false},
		{"", "v0.2.0", false},
	} {
		if got := Newer(c.current, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.current, c.latest, got)
		}
	}
}

func TestNotes(t *testing.T) {
	r := Release{Body: "### Added\n\n- Install with a one-liner (#7)\n\n## Install\n\nOn macOS…"}
	if got := r.Notes(); got != "### Added\n\n- Install with a one-liner (#7)" {
		t.Errorf("notes %q", got)
	}
}

func TestManaged(t *testing.T) {
	if Managed("/opt/homebrew/Cellar/lazysecrets/0.2.0/bin/lazysecrets") == "" || Managed("/nix/store/abc-lazysecrets/bin/lazysecrets") == "" {
		t.Error("package managers not recognised")
	}
	if m := Managed("/Users/x/.local/bin/lazysecrets"); m != "" {
		t.Errorf("~/.local/bin is ours: %q", m)
	}
}

// server publishes v0.2.0 with a "binary" (a script) printing its version.
func server(t *testing.T, tamper bool) *httptest.Server {
	t.Helper()
	name := fmt.Sprintf("lazysecrets_0.2.0_%s_%s", runtime.GOOS, runtime.GOARCH)
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	script := []byte("#!/bin/sh\necho lazysecrets 0.2.0\n")
	tw.WriteHeader(&tar.Header{Name: name + "/lazysecrets", Mode: 0o755, Size: int64(len(script)), Typeflag: tar.TypeReg})
	tw.Write(script)
	tw.Close()
	gz.Close()
	sum := sha256.Sum256(archive.Bytes())
	if tamper {
		sum[0] ^= 0xff
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/keb/lazysecrets/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v0.2.0","body":"### Added\n\n- x\n\n## Install\n","html_url":"https://example.com/r"}`)
	})
	mux.HandleFunc("/keb/lazysecrets/releases/download/v0.2.0/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s.tar.gz\n", hex.EncodeToString(sum[:]), name)
	})
	mux.HandleFunc("/keb/lazysecrets/releases/download/v0.2.0/"+name+".tar.gz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(archive.Bytes())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestInstall(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("releases are for macOS and Linux")
	}
	srv := server(t, false)
	c := &Client{Servers: []string{srv.URL + "/keb/lazysecrets"}}
	r, err := c.Latest(context.Background())
	if err != nil || r.Tag != "v0.2.0" {
		t.Fatalf("latest %+v %v", r, err)
	}
	exe := filepath.Join(t.TempDir(), "lazysecrets")
	os.WriteFile(exe, []byte("old"), 0o755)
	if err := c.Install(context.Background(), r, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); !strings.Contains(string(b), "echo lazysecrets 0.2.0") {
		t.Errorf("not replaced: %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".lazysecrets-update-*")); len(left) > 0 {
		t.Errorf("left behind: %v", left)
	}
}

// A server that's down, or doesn't have the release, is skipped.
func TestFallsBackToTheNextServer(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("releases are for macOS and Linux")
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	t.Cleanup(down.Close)
	srv := server(t, false)
	c := &Client{Servers: []string{down.URL + "/keb/lazysecrets", srv.URL + "/keb/lazysecrets"}}
	r, err := c.Latest(context.Background())
	if err != nil || r.Tag != "v0.2.0" {
		t.Fatalf("latest %+v %v", r, err)
	}
	exe := filepath.Join(t.TempDir(), "lazysecrets")
	os.WriteFile(exe, []byte("old"), 0o755)
	if err := c.Install(context.Background(), r, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); !strings.Contains(string(b), "echo lazysecrets 0.2.0") {
		t.Errorf("not replaced: %q", b)
	}

	// Every server down: both say why.
	c = &Client{Servers: []string{down.URL + "/a/b", down.URL + "/c/d"}}
	if _, err := c.Latest(context.Background()); err == nil || strings.Count(err.Error(), "502") != 2 {
		t.Errorf("all down: %v", err)
	}
}

func TestAPI(t *testing.T) {
	for repo, want := range map[string]string{
		"https://github.com/karleinarb/lazysecrets":   "https://api.github.com/repos/karleinarb/lazysecrets",
		"https://git.kebcloud.no/keb/lazysecrets/":    "https://git.kebcloud.no/api/v1/repos/keb/lazysecrets",
		"https://codeberg.org/someone/lazysecrets":    "https://codeberg.org/api/v1/repos/someone/lazysecrets",
		"https://git.kebcloud.no/keb":                 "",
		"git.kebcloud.no/keb/lazysecrets":             "",
		"https://github.com/karleinarb/lazysecrets/x": "",
	} {
		got, err := API(repo)
		if got != want || (err != nil) != (want == "") {
			t.Errorf("API(%q) = %q, %v", repo, got, err)
		}
	}
}

func TestInstallRefusesATamperedArchive(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("releases are for macOS and Linux")
	}
	srv := server(t, true)
	c := &Client{Servers: []string{srv.URL + "/keb/lazysecrets"}}
	exe := filepath.Join(t.TempDir(), "lazysecrets")
	os.WriteFile(exe, []byte("old"), 0o755)
	err := c.Install(context.Background(), Release{Tag: "v0.2.0"}, exe)
	if err == nil || !strings.Contains(err.Error(), "doesn't match checksums.txt") {
		t.Fatalf("err %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Error("replaced anyway")
	}
}

func TestNotesShowLinksAsText(t *testing.T) {
	r := Release{Body: "### Added\n\n- Search ([#11](https://git.example.com/pulls/11)) ([65dc2d7](https://git.example.com/commit/65dc2d7abc))\n\n## Install\n\ncurl …"}
	if got, want := r.Notes(), "### Added\n\n- Search (#11) (65dc2d7)"; got != want {
		t.Errorf("notes %q, want %q", got, want)
	}
}
