package main

import (
	"context"
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

// value reads what r points at (ref.Read), with the account that last
// read it first.
func value(ctx context.Context, providers []provider.Provider, st *store.Store, r ref.Ref) (provider.SecretValue, error) {
	v, _, err := readKey(ctx, providers, st, r)
	return v, err
}

// readKey is value, also returning the version read.
func readKey(ctx context.Context, providers []provider.Provider, st *store.Store, r ref.Ref) (provider.SecretValue, string, error) {
	key := r
	key.Version = ""
	node := st.Node(key.String())
	return ref.Read(ctx, providers, r, node.Account, node.Source)
}
