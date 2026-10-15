package repo

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// Spring Boot config files: application*.properties/.yml and bootstrap*,
// their keys filtered by the secret patterns (patterns.go).

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

var springType = configType{
	name: "spring", is: IsSpring, format: springFormat, parse: parseSpring,
	jasyptSetting: func(string) string { return "jasypt.encryptor.password" },
	placeholder:   springPlaceholder,
}

// springPlaceholderRe is Spring's ${name} or ${name:default}.
var springPlaceholderRe = regexp.MustCompile(`^\$\{([^:}]+)(:([^}]*))?\}$`)

func springPlaceholder(v string) (name, def string, hasDef, ok bool) {
	m := springPlaceholderRe.FindStringSubmatch(v)
	if m == nil {
		return "", "", false, false
	}
	return m[1], m[3], m[2] != "", true
}

func parseSpring(file string, b []byte) ([]configKey, error) {
	var keys []configKey
	var err error
	if springFormat(file) == "properties" {
		keys = parseProperties(b)
	} else {
		keys, err = parseSpringYAML(b)
	}
	for i := range keys {
		keys[i].ref = reference(springPlaceholder, keys[i].value)
	}
	return keys, err
}

// springDocs names the keys of a multi-document file's later documents
// after the profile they're for, so each name is one key:
// spring.datasource.password (prod).
func springDocs(docs [][]configKey) []configKey {
	var out []configKey
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

func parseSpringYAML(b []byte) ([]configKey, error) {
	b = mavenFiltered.ReplaceAllFunc(b, func(m []byte) []byte {
		sub := mavenFiltered.FindSubmatch(m)
		return fmt.Appendf(nil, "%s'%s'", sub[1], bytes.ReplaceAll(sub[2], []byte("'"), []byte("''")))
	})
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var docs [][]configKey
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		var keys []configKey
		if len(doc.Content) > 0 {
			springWalk(doc.Content[0], "", &keys)
		}
		docs = append(docs, keys)
	}
	return springDocs(docs), nil
}

// springWalk names keys the way Spring does: nested keys joined by dots,
// list items by index (servers[0]).
func springWalk(n *yaml.Node, name string, out *[]configKey) {
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
			*out = append(*out, configKey{key: name, value: n.Value, line: n.Line})
		}
	}
}

// parseProperties reads a Java .properties file: key=value, key: value or
// key value, lines continued with a trailing backslash, # and ! comments,
// and #--- between documents (Spring Boot 2.4+).
func parseProperties(b []byte) []configKey {
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	var docs [][]configKey
	var doc []configKey
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
		doc = append(doc, configKey{key: unescapeProperty(k), value: unescapeProperty(v), line: start})
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
