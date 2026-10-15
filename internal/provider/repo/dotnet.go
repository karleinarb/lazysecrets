package repo

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// .NET config files: appsettings.json and appsettings.<Environment>.json.
// Keys are named as .NET's configuration names them, sections joined by
// colons: ConnectionStrings:Default, Servers:0:Name.

var dotnetType = configType{
	name: "dotnet", is: IsDotnet, format: func(string) string { return "json" }, parse: parseDotnet,
	placeholder: dotnetPlaceholder,
}

var dotnetName = regexp.MustCompile(`^appsettings(\.[^/]+)?\.json$`)

// IsDotnet reports whether f is named like a .NET appsettings file.
func IsDotnet(f string) bool {
	return dotnetName.MatchString(strings.ToLower(path.Base(f)))
}

// dotnetPlaceholder reads an App Service Key Vault reference,
// @Microsoft.KeyVault(SecretUri=…): the secret is in the vault.
func dotnetPlaceholder(v string) (name, def string, hasDef, ok bool) {
	if strings.HasPrefix(v, "@Microsoft.KeyVault(") && strings.HasSuffix(v, ")") {
		return v, "", false, true
	}
	return "", "", false, false
}

func parseDotnet(_ string, b []byte) ([]configKey, error) {
	var doc yaml.Node // JSON is YAML, and the YAML parser says where each key is
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	var out []configKey
	if len(doc.Content) > 0 {
		dotnetWalk(doc.Content[0], "", &out)
	}
	for i := range out {
		out[i].name = out[i].key
		out[i].ref = reference(dotnetPlaceholder, out[i].value)
	}
	return out, nil
}

func dotnetWalk(n *yaml.Node, name string, out *[]configKey) {
	join := func(child string) string {
		if name == "" {
			return child
		}
		return name + ":" + child
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			dotnetWalk(n.Content[i+1], join(n.Content[i].Value), out)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			dotnetWalk(c, join(fmt.Sprint(i)), out)
		}
	case yaml.ScalarNode:
		if name != "" {
			*out = append(*out, configKey{key: name, value: n.Value, line: n.Line})
		}
	}
}
