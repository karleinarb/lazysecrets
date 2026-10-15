package repo

import (
	"path"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Docker Compose files: compose.yaml, docker-compose.yml and their
// variants (docker-compose.test.yml). Their keys are the services'
// environment variables, named service.VAR.

var composeType = configType{
	name: "compose", is: IsCompose, format: func(string) string { return "yaml" }, parse: parseCompose,
	jasyptSetting: func(name string) string {
		service, _, _ := strings.Cut(name, ".")
		return service + "." + jasyptEnv
	},
	placeholder: composePlaceholder,
}

var composeName = regexp.MustCompile(`^(docker-)?compose([.-][^/]+)?\.ya?ml$`)

// IsCompose reports whether f is named like a Docker Compose file.
func IsCompose(f string) bool {
	return composeName.MatchString(strings.ToLower(path.Base(f)))
}

// composePlaceholderRe is compose's ${VAR}, ${VAR:-default}, ${VAR-default},
// ${VAR:?error}, ${VAR?error} or $VAR.
var composePlaceholderRe = regexp.MustCompile(`^\$(?:\{(\w+)(?:(:?-)([^}]*)|:?\?[^}]*)?\}|(\w+))$`)

func composePlaceholder(v string) (name, def string, hasDef, ok bool) {
	m := composePlaceholderRe.FindStringSubmatch(v)
	if m == nil {
		return "", "", false, false
	}
	return m[1] + m[4], m[3], m[2] != "", true
}

func parseCompose(_ string, b []byte) ([]configKey, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	var out []configKey
	for _, service := range pairs(field(doc.Content[0], "services")) {
		for _, k := range environment(field(service.value, "environment")) {
			k.name = service.key.Value + "." + k.key
			out = append(out, k)
		}
	}
	return out, nil
}

// environment reads a service's environment, a map (KEY: value) or a list
// (- KEY=value). A variable without a value (KEY:, - KEY) is taken from
// where compose runs: a reference.
func environment(n *yaml.Node) []configKey {
	var out []configKey
	switch n = deref(n); {
	case n == nil:
	case n.Kind == yaml.MappingNode:
		for _, p := range pairs(n) {
			v := deref(p.value)
			ref := v.Tag == "!!null" || reference(composePlaceholder, v.Value)
			out = append(out, configKey{key: p.key.Value, value: v.Value, ref: ref, line: p.key.Line})
		}
	case n.Kind == yaml.SequenceNode:
		for _, item := range n.Content {
			item = deref(item)
			k, v, ok := strings.Cut(item.Value, "=")
			out = append(out, configKey{key: k, value: v, ref: !ok || reference(composePlaceholder, v), line: item.Line})
		}
	}
	return out
}

type pair struct{ key, value *yaml.Node }

// pairs are a mapping's entries, with those merged in by << (anchors
// shared between services) first and its own over them.
func pairs(n *yaml.Node) []pair {
	n = deref(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var merged, own []pair
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Value != "<<" {
			own = append(own, pair{k, v})
			continue
		}
		from := []*yaml.Node{v}
		if v.Kind == yaml.SequenceNode {
			from = v.Content
		}
		for _, m := range from {
			merged = append(merged, pairs(m)...)
		}
	}
	var out []pair
	for _, p := range append(merged, own...) {
		if i := indexOf(out, p.key.Value); i >= 0 {
			out[i] = p
		} else {
			out = append(out, p)
		}
	}
	return out
}

func indexOf(ps []pair, key string) int {
	for i, p := range ps {
		if p.key.Value == key {
			return i
		}
	}
	return -1
}

func field(n *yaml.Node, key string) *yaml.Node {
	ps := pairs(n)
	if i := indexOf(ps, key); i >= 0 {
		return ps[i].value
	}
	return nil
}

func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}
