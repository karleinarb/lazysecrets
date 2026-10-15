package repo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// Details are how a file is encrypted and for whom, and what git knows of
// it: a plain .env file that git tracks, or could, is marked.
func (p *Provider) Details(ctx context.Context, c provider.Container) ([]provider.Attr, error) {
	r, ok := p.repo(c.Source.ID)
	if !ok {
		return nil, provider.ErrUnsupported
	}
	rel, err := filepath.Rel(r.root, c.ID)
	if err != nil {
		return nil, err
	}
	var out []provider.Attr
	meta, isSops := readMeta(c.ID)
	// A plain file's values are at risk in git, unless they're all Jasypt;
	// a config file's only when one of its secrets is in plain text.
	plain := !isSops
	if t, ok := configTypeOf(c.ID); ok && !isSops {
		out, plain = p.configDetails(t, c)
	} else if isSops {
		out = append(out, provider.Attr{Label: "encryption", Value: "sops · " + strings.Join(meta.methods, ", ")})
		for i, k := range meta.recipients {
			label := ""
			if i == 0 {
				label = "keys"
			}
			out = append(out, provider.Attr{Label: label, Value: k})
		}
		if !meta.modified.IsZero() {
			out = append(out, provider.Attr{Label: "encrypted", Value: meta.modified.Local().Format("2006-01-02 15:04")})
		}
	} else {
		out, plain = envDetails(c.ID)
	}

	git := func(args ...string) (string, bool) {
		b, err := exec.CommandContext(ctx, "git", append([]string{"-C", r.root}, args...)...).Output()
		return strings.TrimSpace(string(b)), err == nil
	}
	switch _, tracked := git("ls-files", "--error-unmatch", "--", rel); {
	case tracked && plain:
		out = append(out, provider.Attr{Label: "git", Value: "tracked: its values are in the repo's history", Warn: true})
	case tracked:
		out = append(out, provider.Attr{Label: "git", Value: "tracked"})
	default:
		if _, ignored := git("check-ignore", "-q", "--", rel); ignored {
			out = append(out, provider.Attr{Label: "git", Value: "ignored"})
		} else {
			out = append(out, provider.Attr{Label: "git", Value: "untracked and not ignored: easy to commit by mistake", Warn: plain})
		}
		return out, nil
	}
	if last, ok := git("log", "-1", "--format=%h · %ar", "--", rel); ok && last != "" {
		out = append(out, provider.Attr{Label: "last commit", Value: last})
	}
	return out, nil
}

// envDetails says how a plain env file's values are encrypted, and whether
// any is in plain text.
func envDetails(file string) (attrs []provider.Attr, plain bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return []provider.Attr{{Label: "encryption", Value: err.Error(), Warn: true}}, true
	}
	keys, _ := parseKeys(file, b)
	n := 0
	for _, k := range keys {
		if k.jasypt {
			n++
		}
	}
	return []provider.Attr{{Label: "encryption", Value: jasyptShare(n, len(keys))}}, n < len(keys)
}

// jasyptShare is how many of a plain file's values are Jasypt's.
func jasyptShare(n, of int) string {
	if n == 0 {
		return "none: plain text"
	}
	return fmt.Sprintf("jasypt: %d of %d values", n, of)
}
