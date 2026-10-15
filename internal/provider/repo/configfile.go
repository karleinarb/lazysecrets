package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"git.kebcloud.no/keb/lazysecrets/internal/jasypt"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// Config files (Spring's, compose's) are read directly, like env files,
// but most of their keys aren't secret: those the secret patterns
// (patterns.go) don't match are hidden, and a file with none of them is.

// configType is a type of config file: what it's called (in settings, as
// spring:, and on the file's row) and how its keys are read.
type configType struct {
	name   string
	is     func(file string) bool
	format func(file string) string // yaml, properties
	parse  func(file string, b []byte) ([]configKey, error)
	// jasyptSetting is the key that holds the Jasypt password for the
	// key named name.
	jasyptSetting func(name string) string
	// placeholder reads a value that only refers to another,
	// ${DB_PASSWORD}: what it names, and its default if it has one.
	placeholder func(v string) (name, def string, hasDef, ok bool)
}

var configTypes = []configType{springType, composeType}

func isConfig(f string) bool {
	_, ok := configTypeOf(f)
	return ok
}

// configTypeOf is the type of config file f is, if it's one.
func configTypeOf(f string) (configType, bool) {
	for _, t := range configTypes {
		if t.is(f) {
			return t, true
		}
	}
	return configType{}, false
}

// detail is the file's row: "spring · yaml".
func (t configType) detail(file string) string { return t.name + " · " + t.format(file) }

// configKey is a key of a config file, with its value.
type configKey struct {
	key   string // as the app sees it: spring.datasource.password, DB_PASSWORD
	name  string // one key in the file: spring.datasource.password (prod), api.DB_PASSWORD
	value string
	ref   bool // the value only refers to the secret, set elsewhere: ${DB_PASSWORD}
	line  int  // 1-based
}

func (k configKey) jasypt() bool { return jasypt.Is(k.value) }

// plain reports whether the file holds the value itself, readable.
func (k configKey) plain() bool { return k.value != "" && !k.ref && !k.jasypt() }

// reference reports whether v is only a placeholder without a default:
// one with a default has the default in the file.
func reference(placeholder func(string) (string, string, bool, bool), v string) bool {
	_, _, hasDef, ok := placeholder(v)
	return ok && !hasDef
}

func (t configType) read(file string) ([]configKey, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return t.parse(file, b)
}

func (t configType) keyNamed(file, name string) (configKey, error) {
	keys, err := t.read(file)
	if err != nil {
		return configKey{}, err
	}
	for _, k := range keys {
		if k.name == name {
			return k, nil
		}
	}
	return configKey{}, fmt.Errorf("%s isn't in %s", name, filepath.Base(file))
}

func (t configType) value(file, name string) (provider.SecretValue, error) {
	k, err := t.keyNamed(file, name)
	if err != nil {
		return provider.SecretValue{}, err
	}
	return provider.NewSecretValue([]byte(k.value)), nil
}

// configHidden reports whether a config file has no secret keys. One that
// can't be read isn't hidden: its error is worth seeing.
func (p *Provider) configHidden(t configType, root, file string) bool {
	keys, err := t.read(file)
	if err != nil {
		return false
	}
	filter, err := p.filterFor(root, t.name)
	if err != nil {
		return false
	}
	return !slices.ContainsFunc(keys, func(k configKey) bool { return filter.secret(k.key, k.value) })
}

// configObjects lists the file's keys, those that don't look like secrets
// hidden.
func (p *Provider) configObjects(t configType, c provider.Container, b []byte) ([]provider.Object, error) {
	keys, err := t.parse(c.ID, b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.Name, err)
	}
	filter, err := p.filterFor(c.Source.ID, t.name)
	if err != nil {
		return nil, err
	}
	format := provider.Attr{Label: "format", Value: t.name + " " + t.format(c.ID)}
	var out []provider.Object
	for _, k := range keys {
		state, detail := provider.Attr{Label: "encrypted", Value: "no: plain text in the file"}, ""
		switch {
		case k.ref:
			state, detail = provider.Attr{Label: "value", Value: "a reference: the secret is set elsewhere"}, "reference"
		case k.jasypt():
			state, detail = provider.Attr{Label: "encrypted", Value: "jasypt"}, "jasypt"
		}
		out = append(out, provider.Object{
			Container: c, Type: provider.TypeSecret, ID: c.ID + "\x00" + k.name, Name: k.name, Detail: detail,
			Hidden: !filter.secret(k.key, k.value), Attrs: []provider.Attr{format, state},
		})
	}
	return out, nil
}

// configDetails says how many of the file's keys aren't listed, and
// whether any listed one is in plain text.
func (p *Provider) configDetails(t configType, c provider.Container) (attrs []provider.Attr, plain bool) {
	keys, err := t.read(c.ID)
	if err != nil {
		return []provider.Attr{{Label: "keys", Value: err.Error(), Warn: true}}, false
	}
	filter, err := p.filterFor(c.Source.ID, t.name)
	if err != nil {
		return []provider.Attr{{Label: "keys", Value: err.Error(), Warn: true}}, false
	}
	secrets, jasypt := 0, 0
	for _, k := range keys {
		if filter.secret(k.key, k.value) {
			secrets++
			plain = plain || k.plain()
		}
		if k.jasypt() {
			jasypt++
		}
	}
	return []provider.Attr{
		{Label: "encryption", Value: jasyptShare(jasypt, len(keys))},
		{Label: "hidden keys", Value: fmt.Sprintf("%d, not matched by the secret patterns", len(keys)-secrets)},
	}, plain
}
