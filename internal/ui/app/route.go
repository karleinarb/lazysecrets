package app

import (
	"context"
	"fmt"
	"slices"

	"git.kebcloud.no/keb/lazysecrets/internal/cachefile"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// containerKey identifies a container in the UI and caches. The same vault
// seen through two accounts must not share cached lists, since each
// account may see different things.
func containerKey(c provider.Container) string { return cachefile.ContainerKey(c) }

// providerFor finds the provider instance a source came from. Sources
// remembered from an earlier run (favourites) may name an account that
// isn't signed in any more, or predate accounts; they fall back to the
// first provider of the same kind.
func (m Model) providerFor(src provider.Source) provider.Provider {
	if p, ok := m.byID[src.Provider]; ok {
		return p
	}
	for _, p := range m.providers {
		if p.Kind() == src.Kind {
			return p
		}
	}
	return missingProvider{src}
}

// candidates lists the providers that may serve src, in configured order:
// its own provider when that is known, otherwise every provider of its kind
// that lists the source (all of that kind if their sources haven't loaded).
// Favourites saved before accounts existed, or by an account that is no
// longer signed in, take the second path.
func (m Model) candidates(src provider.Source) []provider.Provider {
	if p, ok := m.byID[src.Provider]; ok {
		return []provider.Provider{p}
	}
	var seeing, ofKind []provider.Provider
	for _, p := range m.providers {
		if p.Kind() != src.Kind {
			continue
		}
		ofKind = append(ofKind, p)
		r, loaded := m.sourceResults[p.ID()]
		if !loaded || slices.ContainsFunc(r.sources, func(s provider.Source) bool { return s.ID == src.ID }) {
			seeing = append(seeing, p)
		}
	}
	switch {
	case len(seeing) > 0:
		return seeing
	case len(ofKind) > 0:
		return ofKind
	}
	return []provider.Provider{missingProvider{src}}
}

// boundTo returns src as served by p.
func boundTo(src provider.Source, p provider.Provider) provider.Source {
	src.Provider, src.ProviderLabel = p.ID(), p.Label()
	return src
}

// missingProvider answers for a provider that isn't configured, e.g. a
// favourite from a provider disabled in the config.
type missingProvider struct{ src provider.Source }

func (p missingProvider) err() error {
	return fmt.Errorf("%s isn't available (disabled in the config or not signed in)", p.src.Kind)
}

func (p missingProvider) Kind() provider.Kind { return p.src.Kind }
func (p missingProvider) ID() string          { return p.src.Provider }
func (p missingProvider) Label() string       { return string(p.src.Kind) }
func (p missingProvider) Sources(context.Context) ([]provider.Source, error) {
	return nil, p.err()
}
func (p missingProvider) Containers(context.Context, provider.Source) ([]provider.Container, error) {
	return nil, p.err()
}
func (p missingProvider) Objects(context.Context, provider.Container, provider.ObjectType) ([]provider.Object, error) {
	return nil, p.err()
}
func (p missingProvider) Describe(_ context.Context, o provider.Object) (provider.Object, error) {
	return o, p.err()
}
func (p missingProvider) Versions(context.Context, provider.Object) ([]provider.Object, error) {
	return nil, p.err()
}
func (p missingProvider) Value(context.Context, provider.Object) (provider.SecretValue, error) {
	return provider.SecretValue{}, p.err()
}
