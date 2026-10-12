package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

// get prints the value a link points at, for scripts:
//
//	export DB_PASSWORD=$(lazysecrets get lazysecrets://azure/kv.vault.azure.net/secrets/db-password)
//
// It ends with a newline only when printing to a terminal, so command
// substitution and pipes get the value exactly.
func get(ctx context.Context, providers []provider.Provider, st *store.Store, r ref.Ref) error {
	v, err := value(ctx, providers, st, r)
	if err != nil {
		return err
	}
	out := v.Reveal()
	if term.IsTerminal(int(os.Stdout.Fd())) {
		out += "\n"
	}
	_, err = os.Stdout.WriteString(out)
	return err
}

// value reads what r points at without listing its vault: the vault and
// name are enough for Azure, tried with each account (the one that last
// read it first); for Databricks the workspace's profile is found by its
// host.
func value(ctx context.Context, providers []provider.Provider, st *store.Store, r ref.Ref) (provider.SecretValue, error) {
	if r.IsSource() || r.IsContainer() {
		return provider.SecretValue{}, fmt.Errorf("%s points at a %s, not a secret", r, map[bool]string{true: "subscription or workspace", false: "vault or scope"}[r.IsSource()])
	}
	if r.Kind != provider.KindAzure && r.Host == "" {
		return provider.SecretValue{}, errors.New("{{secrets/…}} names no workspace: use its lazysecrets:// link (s in lazysecrets)")
	}
	key := r
	key.Version = ""
	node := st.Node(key.String())

	type try struct {
		p provider.Provider
		o provider.Object
	}
	var tries []try
	for _, p := range providers {
		if p.Kind() != r.Kind {
			continue
		}
		o := provider.Object{Name: r.Name, Type: cmp.Or(r.Type, provider.TypeSecret), Version: r.Version}
		if r.Kind != provider.KindAzure { // by host: workspaces, repos
			loc, ok := p.(provider.Locator)
			if !ok {
				continue
			}
			profile, found, err := loc.Locate(ctx, r.Host)
			if err != nil || !found {
				continue
			}
			src := provider.Source{Kind: r.Kind, Provider: p.ID(), ID: profile}
			o.Container = provider.Container{Source: src, Name: r.Scope}
			// Listing the scopes tells a Key Vault-backed one, whose value
			// can then be read from the vault if Databricks refuses.
			if cs, err := p.Containers(ctx, src); err == nil {
				for _, c := range cs {
					if c.Name == r.Scope {
						c.Source = src
						o.Container = c
					}
				}
			}
		} else {
			src := provider.Source{Kind: r.Kind, Provider: p.ID(), ID: node.Source}
			o.Container = provider.Container{Source: src, Name: r.ContainerName(), Detail: r.VaultURL()}
		}
		t := try{p, o}
		if p.ID() == node.Account {
			tries = append([]try{t}, tries...)
		} else {
			tries = append(tries, t)
		}
	}
	if len(tries) == 0 {
		switch r.Kind {
		case provider.KindDatabricks:
			return provider.SecretValue{}, fmt.Errorf("no Databricks profile for workspace %s", r.Host)
		case provider.KindRepo:
			return provider.SecretValue{}, fmt.Errorf("repo %s isn't here: run it inside the repo, or list it under providers.repos.paths", r.Host)
		}
		return provider.SecretValue{}, fmt.Errorf("no %s account to read %s with", r.Kind, r)
	}
	var errs []error
	for _, t := range tries {
		v, err := t.p.Value(ctx, t.o)
		if err == nil {
			return v, nil
		}
		if len(tries) == 1 {
			return provider.SecretValue{}, err
		}
		errs = append(errs, fmt.Errorf("%s: %w", t.p.Label(), err))
	}
	return provider.SecretValue{}, errors.Join(errs...)
}
