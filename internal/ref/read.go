package ref

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// Read reads what r points at without listing its vault: the vault and
// name are enough for Azure, tried with each account (account, the one
// that last read it, first, in source); for Databricks and repos the
// profile or repo is found by its host. It also returns the version read,
// when the provider tells (provider.Versioned).
func Read(ctx context.Context, providers []provider.Provider, r Ref, account, source string) (provider.SecretValue, string, error) {
	if r.IsShare() || r.IsSource() || r.IsContainer() {
		what := "vault or scope"
		switch {
		case r.IsShare():
			what = "share link"
		case r.IsSource():
			what = "subscription or workspace"
		}
		return provider.SecretValue{}, "", fmt.Errorf("%s points at a %s, not a secret", r, what)
	}
	if r.Kind != provider.KindAzure && r.Host == "" {
		return provider.SecretValue{}, "", errors.New("{{secrets/…}} names no workspace: use its lazysecrets:// link (s in lazysecrets)")
	}

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
			src := provider.Source{Kind: r.Kind, Provider: p.ID(), ID: source}
			o.Container = provider.Container{Source: src, Name: r.ContainerName(), Detail: r.VaultURL()}
		}
		t := try{p, o}
		if p.ID() == account {
			tries = append([]try{t}, tries...)
		} else {
			tries = append(tries, t)
		}
	}
	if len(tries) == 0 {
		switch r.Kind {
		case provider.KindDatabricks:
			return provider.SecretValue{}, "", fmt.Errorf("no Databricks profile for workspace %s", r.Host)
		case provider.KindRepo:
			return provider.SecretValue{}, "", fmt.Errorf("repo %s isn't here: run it inside the repo, or list it under providers.repos.paths", r.Host)
		}
		return provider.SecretValue{}, "", fmt.Errorf("no %s account to read %s with", r.Kind, r)
	}
	var errs []error
	for _, t := range tries {
		v, version, err := value(ctx, t.p, t.o)
		if err == nil {
			return v, version, nil
		}
		if len(tries) == 1 || ctx.Err() != nil {
			return provider.SecretValue{}, "", err
		}
		errs = append(errs, fmt.Errorf("%s: %w", t.p.Label(), err))
	}
	return provider.SecretValue{}, "", errors.Join(errs...)
}

func value(ctx context.Context, p provider.Provider, o provider.Object) (provider.SecretValue, string, error) {
	if vp, ok := p.(provider.Versioned); ok {
		return vp.VersionedValue(ctx, o)
	}
	v, err := p.Value(ctx, o)
	return v, o.Version, err
}
