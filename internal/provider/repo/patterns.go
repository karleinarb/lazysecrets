package repo

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
)

// Config files (Spring's) hold all of an app's config, most of it not
// secret: of their keys, those named like a secret or with a value that
// looks like one are listed, the rest hidden (H shows them). The patterns
// can be set for all of a repo's files and per file type, in the config
// and in a repo's .lazysecrets.yaml.

// DefaultSecretKeys and DefaultSecretValues are the patterns used when
// nothing sets them. The key pattern is anchored at the end of the name:
// spring.datasource.password, client-secret, DB_PASSWORD and checkoutKey,
// not the oauth2.*, keycloak.* and token-uri keys every Spring app has.
// passord is Norwegian. They err on listing too much: a row too many is
// cheap, a secret missed isn't.
var (
	DefaultSecretKeys = []string{
		`(?i)(pass(word|wd|phrase|ord)?|pwd|secret|token|key|credentials?|salt|connection-?string)$`,
	}
	DefaultSecretValues = []string{
		`^ENC\(.+\)$`,            // Jasypt
		`://[^/\s:@]*:[^/\s@]+@`, // a URL with a password: jdbc:postgresql://user:pass@host
		`(?i)(AccountKey|SharedAccessKey|Password|Pwd)=`, // a connection string with one
	}
)

// SecretPatterns are regexps matched against a key's name and its value:
// a key is a secret when either matches. A list that isn't set is taken
// from the next place that sets it (see filterFor).
type SecretPatterns struct {
	Keys   *[]string `yaml:"keys"`
	Values *[]string `yaml:"values"`
}

// FileType holds the settings of one type of file (spring:), for now its
// own secret patterns.
type FileType struct {
	SecretPatterns SecretPatterns `yaml:"secretPatterns"`
}

// Check says which pattern doesn't compile; field is where they're set
// (spring.secretPatterns), for the message.
func (p SecretPatterns) Check(field string) error {
	var errs []error
	for name, list := range map[string]*[]string{"keys": p.Keys, "values": p.Values} {
		if list == nil {
			continue
		}
		for i, s := range *list {
			if _, err := regexp.Compile(s); err != nil {
				errs = append(errs, fmt.Errorf("%s.%s[%d]: %w", field, name, i, err))
			}
		}
	}
	return errors.Join(errs...)
}

type secretFilter struct{ keys, values []*regexp.Regexp }

func (f secretFilter) secret(key, value string) bool {
	return anyMatch(f.keys, key) || anyMatch(f.values, value)
}

// filterFor is the patterns for a repo's files of one type (spring), each
// list from the first that sets it: the repo's .lazysecrets.yaml for the
// type, then for all its files, the config for the type, then for all
// repos, then the defaults.
func (p *Provider) filterFor(root, fileType string) (secretFilter, error) {
	f, err := readLazysecretsFile(root)
	if err != nil {
		return secretFilter{}, err
	}
	own := filepath.Join(root, ".lazysecrets.yaml") + ": "
	type source struct {
		field    string
		patterns SecretPatterns
	}
	sources := []source{
		{own + fileType + ".secretPatterns", f.types()[fileType].SecretPatterns},
		{own + "secretPatterns", f.SecretPatterns},
		{"providers.repos." + fileType + ".secretPatterns", p.s.Types[fileType].SecretPatterns},
		{"providers.repos.secretPatterns", p.s.SecretPatterns},
	}
	pick := func(name string, list func(SecretPatterns) *[]string, defaults []string) ([]*regexp.Regexp, error) {
		for _, s := range sources {
			if l := list(s.patterns); l != nil {
				return compileAll(*l, s.field+"."+name)
			}
		}
		return compileAll(defaults, "default "+name)
	}
	keys, kerr := pick("keys", func(p SecretPatterns) *[]string { return p.Keys }, DefaultSecretKeys)
	values, verr := pick("values", func(p SecretPatterns) *[]string { return p.Values }, DefaultSecretValues)
	return secretFilter{keys, values}, errors.Join(kerr, verr)
}

func compileAll(patterns []string, field string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for i, s := range patterns {
		re, err := regexp.Compile(s)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", field, i, err)
		}
		out = append(out, re)
	}
	return out, nil
}
