package repo

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"

	"git.kebcloud.no/keb/lazysecrets/internal/jasypt"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// Spring Boot config files (application*.properties/.yml, bootstrap*) hold
// all of an app's config, most of it not secret: of their keys, only those
// named like a secret or with a value that looks like one are listed. The
// patterns are configurable, and the file's details count the rest, so a
// secret the patterns miss isn't hidden without a trace.

// DefaultSpringKeys and DefaultSpringValues are the patterns used when
// neither the config nor the repo's .lazysecrets.yaml sets them. The key
// pattern is anchored at the end of the name: spring.datasource.password,
// client-secret and checkoutKey, not the oauth2.*, keycloak.* and
// token-uri keys every Spring app has. passord is Norwegian. The file's
// details count the keys left out.
var (
	DefaultSpringKeys = []string{
		`(?i)(pass(word|wd|phrase|ord)?|pwd|secret|token|key|credentials?|salt|connection-?string)$`,
	}
	DefaultSpringValues = []string{
		`^ENC\(.+\)$`,            // Jasypt
		`://[^/\s:@]*:[^/\s@]+@`, // a URL with a password: jdbc:postgresql://user:pass@host
		`(?i)(AccountKey|SharedAccessKey|Password|Pwd)=`, // a connection string with one
	}
)

// SpringPatterns are regexps matched against a Spring config key's name
// and its value: a key is listed when either matches. A list that isn't
// set is DefaultSpringKeys or DefaultSpringValues.
type SpringPatterns struct {
	Keys   *[]string `yaml:"keys"`
	Values *[]string `yaml:"values"`
}

// Check says which pattern doesn't compile.
func (p SpringPatterns) Check() error {
	_, err := compileSpring(p)
	return err
}

type springFilter struct{ keys, values []*regexp.Regexp }

func compileSpring(p SpringPatterns) (springFilter, error) {
	var f springFilter
	var errs []error
	add := func(field string, patterns *[]string, defaults []string, out *[]*regexp.Regexp) {
		list := defaults
		if patterns != nil {
			list = *patterns
		}
		for i, s := range list {
			re, err := regexp.Compile(s)
			if err != nil {
				errs = append(errs, fmt.Errorf("spring.%s[%d]: %w", field, i, err))
				continue
			}
			*out = append(*out, re)
		}
	}
	add("keys", p.Keys, DefaultSpringKeys, &f.keys)
	add("values", p.Values, DefaultSpringValues, &f.values)
	return f, errors.Join(errs...)
}

func (f springFilter) secret(k springKey) bool {
	return anyMatch(f.keys, k.key) || anyMatch(f.values, k.value)
}

// springFilterFor is the repo's patterns: its .lazysecrets.yaml's, else the
// config's, each list on its own.
func (p *Provider) springFilterFor(root string) (springFilter, error) {
	f, err := readLazysecretsFile(root)
	if err != nil {
		return springFilter{}, err
	}
	patterns := p.s.Spring
	if f.Spring.Keys != nil {
		patterns.Keys = f.Spring.Keys
	}
	if f.Spring.Values != nil {
		patterns.Values = f.Spring.Values
	}
	filter, err := compileSpring(patterns)
	if err != nil && (f.Spring.Keys != nil || f.Spring.Values != nil) {
		err = fmt.Errorf("%s/.lazysecrets.yaml: %w", root, err)
	}
	return filter, err
}

// springName is application.yml, application-prod.properties, bootstrap.yaml
// and the like; not applications.yaml (Argo CD's) or application.yml.example.
var springName = regexp.MustCompile(`^(application|bootstrap)(-[^.]+)?\.(properties|ya?ml)$`)

// IsSpring reports whether f is named like a Spring Boot config file, and
// isn't a Helm chart's template of one.
func IsSpring(f string) bool {
	return springName.MatchString(strings.ToLower(path.Base(f))) && path.Base(path.Dir(f)) != "templates"
}

func springFormat(f string) string {
	if strings.ToLower(filepath.Ext(f)) == ".properties" {
		return "properties"
	}
	return "yaml"
}

type springKey struct {
	key   string // spring.datasource.password
	name  string // key, with (profile) after it in a later document
	value string
	line  int // 1-based
}

// reference reports whether the value is only a placeholder, ${DB_PASSWORD}:
// the secret is elsewhere. One with a default, ${DB_PASSWORD:hunter2}, has
// the default in the file.
func (k springKey) reference() bool {
	return strings.HasPrefix(k.value, "${") && strings.HasSuffix(k.value, "}") &&
		!strings.ContainsAny(k.value[2:len(k.value)-1], ":${}")
}

func (k springKey) jasypt() bool {
	return jasypt.Is(k.value)
}

// plain reports whether the file holds the value itself, readable.
func (k springKey) plain() bool {
	return k.value != "" && !k.reference() && !k.jasypt()
}

// readSpring lists a Spring config file's keys, every one, with values.
func readSpring(file string) ([]springKey, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return parseSpring(file, b)
}

func parseSpring(file string, b []byte) ([]springKey, error) {
	if springFormat(file) == "properties" {
		return parseProperties(b), nil
	}
	return parseSpringYAML(b)
}

// springDocs names the keys of a multi-document file's later documents
// after the profile they're for, so each name is one key:
// spring.datasource.password (prod).
func springDocs(docs [][]springKey) []springKey {
	var out []springKey
	for i, doc := range docs {
		suffix := ""
		if i > 0 {
			suffix = fmt.Sprintf(" (document %d)", i+1)
			for _, k := range doc {
				if k.key == "spring.config.activate.on-profile" || k.key == "spring.profiles" {
					suffix = " (" + k.value + ")"
				}
			}
		}
		for _, k := range doc {
			k.name = k.key + suffix
			out = append(out, k)
		}
	}
	return out
}

// mavenFiltered is a value Maven fills in at build time, @project.version@:
// not YAML until it's quoted.
var mavenFiltered = regexp.MustCompile(`(?m)(:[ \t]+|^[ \t]*-[ \t]+)(@[^\n]*?)[ \t]*$`)

func parseSpringYAML(b []byte) ([]springKey, error) {
	b = mavenFiltered.ReplaceAllFunc(b, func(m []byte) []byte {
		sub := mavenFiltered.FindSubmatch(m)
		return fmt.Appendf(nil, "%s'%s'", sub[1], bytes.ReplaceAll(sub[2], []byte("'"), []byte("''")))
	})
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var docs [][]springKey
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		var keys []springKey
		if len(doc.Content) > 0 {
			springWalk(doc.Content[0], "", &keys)
		}
		docs = append(docs, keys)
	}
	return springDocs(docs), nil
}

// springWalk names keys the way Spring does: nested keys joined by dots,
// list items by index (servers[0]).
func springWalk(n *yaml.Node, name string, out *[]springKey) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			child := n.Content[i].Value
			if name != "" {
				child = name + "." + child
			}
			springWalk(n.Content[i+1], child, out)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			springWalk(c, fmt.Sprintf("%s[%d]", name, i), out)
		}
	case yaml.AliasNode:
		if n.Alias != nil {
			springWalk(n.Alias, name, out)
		}
	case yaml.ScalarNode:
		if name != "" {
			*out = append(*out, springKey{key: name, value: n.Value, line: n.Line})
		}
	}
}

// parseProperties reads a Java .properties file: key=value, key: value or
// key value, lines continued with a trailing backslash, # and ! comments,
// and #--- between documents (Spring Boot 2.4+).
func parseProperties(b []byte) []springKey {
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	var docs [][]springKey
	var doc []springKey
	for i := 0; i < len(lines); i++ {
		start := i + 1
		line := strings.TrimLeftFunc(lines[i], unicode.IsSpace)
		if line == "#---" || line == "!---" {
			docs, doc = append(docs, doc), nil
			continue
		}
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		for continued(line) && i+1 < len(lines) {
			i++
			line = line[:len(line)-1] + strings.TrimLeftFunc(lines[i], unicode.IsSpace)
		}
		k, v := splitProperty(line)
		doc = append(doc, springKey{key: unescapeProperty(k), value: unescapeProperty(v), line: start})
	}
	return springDocs(append(docs, doc))
}

// continued reports whether the line ends in an odd number of backslashes.
func continued(line string) bool {
	n := len(line) - len(strings.TrimRight(line, `\`))
	return n%2 == 1
}

// splitProperty cuts a logical line at its first unescaped =, : or
// whitespace; whitespace around the separator isn't part of either side.
func splitProperty(line string) (key, value string) {
	const space = " \t\f"
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\':
			i++
		case c == '=' || c == ':':
			return line[:i], strings.TrimLeft(line[i+1:], space)
		case strings.IndexByte(space, c) >= 0:
			rest := strings.TrimLeft(line[i:], space)
			if rest != "" && (rest[0] == '=' || rest[0] == ':') {
				rest = strings.TrimLeft(rest[1:], space)
			}
			return line[:i], rest
		}
	}
	return line, ""
}

func unescapeProperty(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			if i+5 <= len(s) {
				if r, err := strconv.ParseUint(s[i+1:i+5], 16, 32); err == nil {
					b.WriteRune(rune(r))
					i += 4
					continue
				}
			}
			b.WriteByte('u')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// springHidden reports whether a Spring file has no secret keys. One that
// can't be read isn't hidden: its error is worth seeing.
func (p *Provider) springHidden(root, file string) bool {
	keys, err := readSpring(file)
	if err != nil {
		return false
	}
	filter, err := p.springFilterFor(root)
	if err != nil {
		return false
	}
	return !slices.ContainsFunc(keys, filter.secret)
}

// springObjects lists the file's keys, those that don't look like secrets
// hidden.
func (p *Provider) springObjects(c provider.Container, b []byte) ([]provider.Object, error) {
	keys, err := parseSpring(c.ID, b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.Name, err)
	}
	filter, err := p.springFilterFor(c.Source.ID)
	if err != nil {
		return nil, err
	}
	format := provider.Attr{Label: "format", Value: "spring " + springFormat(c.ID)}
	var out []provider.Object
	for _, k := range keys {
		state, detail := provider.Attr{Label: "encrypted", Value: "no: plain text in the file"}, ""
		switch {
		case k.reference():
			state, detail = provider.Attr{Label: "value", Value: "a reference: the secret is set elsewhere"}, "reference"
		case k.jasypt():
			state, detail = provider.Attr{Label: "encrypted", Value: "jasypt"}, "jasypt"
		}
		out = append(out, provider.Object{
			Container: c, Type: provider.TypeSecret, ID: c.ID + "\x00" + k.name, Name: k.name, Detail: detail,
			Hidden: !filter.secret(k), Attrs: []provider.Attr{format, state},
		})
	}
	return out, nil
}

func springValue(file, name string) (provider.SecretValue, error) {
	k, err := springKeyNamed(file, name)
	if err != nil {
		return provider.SecretValue{}, err
	}
	return provider.NewSecretValue([]byte(k.value)), nil
}

func springKeyNamed(file, name string) (springKey, error) {
	keys, err := readSpring(file)
	if err != nil {
		return springKey{}, err
	}
	for _, k := range keys {
		if k.name == name {
			return k, nil
		}
	}
	return springKey{}, fmt.Errorf("%s isn't in %s", name, filepath.Base(file))
}

// springDetails says how many of the file's keys aren't listed, and
// whether any listed one is in plain text.
func (p *Provider) springDetails(c provider.Container) (attrs []provider.Attr, plain bool) {
	keys, err := readSpring(c.ID)
	if err != nil {
		return []provider.Attr{{Label: "keys", Value: err.Error(), Warn: true}}, false
	}
	filter, err := p.springFilterFor(c.Source.ID)
	if err != nil {
		return []provider.Attr{{Label: "keys", Value: err.Error(), Warn: true}}, false
	}
	secrets, jasypt := 0, 0
	for _, k := range keys {
		if filter.secret(k) {
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
