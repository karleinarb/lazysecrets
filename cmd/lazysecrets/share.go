package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"git.kebcloud.no/keb/lazysecrets/internal/config"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/repo"
	"git.kebcloud.no/keb/lazysecrets/internal/share"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

// shareFiles prints a share link to env files of one repo, encrypted with
// the --key secret's value, else the repo's or the config's sharing.key:
//
//	lazysecrets share .env apps/api/.env
func shareFiles(ctx context.Context, providers []provider.Provider, st *store.Store, general string, rules share.Rules, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("share", flag.ContinueOnError)
	keyLink := fs.String("key", "", "the secret to encrypt with: its lazysecrets:// link, Key Vault URL or reference (default: sharing.key)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("usage: lazysecrets share [--key <secret>] <env files>")
	}

	var root, id string
	var files []share.File
	for _, name := range fs.Args() {
		abs, err := filepath.Abs(name)
		if err != nil {
			return err
		}
		r, i, err := repo.Root(filepath.Dir(abs))
		if err != nil {
			return err
		}
		if root == "" {
			root, id = r, i
		} else if r != root {
			return fmt.Errorf("%s isn't in %s: share files of one repo at a time", name, root)
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, real)
		if err != nil {
			return err
		}
		f := share.File{Path: filepath.ToSlash(rel)}
		if err := share.CheckPath(f.Path); err != nil {
			return err
		}
		if f.Data, err = os.ReadFile(real); err != nil {
			return err
		}
		files = append(files, f)
	}

	key, ok, err := share.DefaultKey(root, general)
	if *keyLink != "" {
		key, err = share.ParseKey(*keyLink)
		ok = err == nil
	}
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("which secret to encrypt with? --key <secret>, or sharing.key in the repo's .lazysecrets.yaml or the config")
	}
	v, version, err := readKey(ctx, providers, st, key)
	if err != nil {
		return fmt.Errorf("reading the key %s: %w", key, err)
	}
	link, err := share.Seal(share.Header{Key: share.Pin(key, version), Repo: id}, []byte(v.Reveal()), files, rules)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, link)
	return err
}

// shareRules are the config's sharing.keyRules.
func shareRules(cfg config.Config) share.Rules {
	return share.Rules{MinLength: cfg.Sharing.KeyRules.MinLength, MinClasses: cfg.Sharing.KeyRules.MinClasses}
}
