package renstiq

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

//go:embed schemas/*.json
var schemas embed.FS

type Config struct {
	Version   int `json:"version"`
	Discovery struct {
		Include []string `json:"include"`
		Exclude []string `json:"exclude"`
	} `json:"discovery"`
	Source *string `json:"-"`
}

func DefaultConfig() Config {
	c := Config{Version: configVersion}
	return c
}
func Schema(name string) ([]byte, error) {
	switch name {
	case "config-show":
		return outputSchema(ConfigResult{})
	case "pr-list":
		return outputSchema(PRListResult{})
	case "discover":
		return outputSchema(DiscoveryResult{})
	case "result":
		return outputSchema(Result{})
	}
	return schemas.ReadFile("schemas/" + name + ".json")
}
func validateSchema(name string, v any) error {
	b, e := Schema(name)
	if e != nil {
		return e
	}
	schema, e := compileJSONSchema(name, b)
	if e != nil {
		return e
	}
	// Normalize YAML values (including timestamps) to their JSON representation,
	// matching decodeMap and preserving the previous validator's conversion.
	b, e = json.Marshal(v)
	if e != nil {
		return e
	}
	v, e = jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if e != nil {
		return e
	}
	return schema.Validate(v)
}

func compileJSONSchema(name string, b []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft7)
	location := "urn:renstiq:schema:" + name
	if err := c.AddResource(location, doc); err != nil {
		return nil, err
	}
	return c.Compile(location)
}
func yamlValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) != 1 {
			return nil, errors.New("empty YAML")
		}
		return yamlValue(n.Content[0])
	case yaml.MappingNode:
		m := map[string]any{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Tag != "!!str" {
				return nil, errors.New("mapping keys must be strings; YAML merge keys are not supported")
			}
			if _, ok := m[k.Value]; ok {
				return nil, fmt.Errorf("duplicate key: %s", k.Value)
			}
			v, e := yamlValue(n.Content[i+1])
			if e != nil {
				return nil, e
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		a := []any{}
		for _, c := range n.Content {
			v, e := yamlValue(c)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		return a, nil
	case yaml.ScalarNode:
		var v any
		if e := n.Decode(&v); e != nil {
			return nil, e
		}
		return v, nil
	default:
		return nil, errors.New("YAML aliases are not supported")
	}
}
func readConfig(path, name string) (result map[string]any, err error) {
	defer func() {
		if err == nil {
			return
		}
		var pathError *os.PathError
		if !errors.As(err, &pathError) || errors.Is(err, os.ErrNotExist) {
			err = &InputError{err}
		}
		err = fmt.Errorf("%s: %w", path, err)
	}()
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	var n yaml.Node
	if e = d.Decode(&n); e != nil {
		return nil, e
	}
	var extra yaml.Node
	if e = d.Decode(&extra); e != io.EOF {
		return nil, errors.New("only one YAML document is allowed")
	}
	v, e := yamlValue(&n)
	if e != nil {
		return nil, e
	}
	if e = validateSchema(name, v); e != nil {
		return nil, e
	}
	return v.(map[string]any), nil
}
func asMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	return m
}
func decodeMap(v any, out any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, out)
}
func configPath() string {
	d := os.Getenv("XDG_CONFIG_HOME")
	if d == "" {
		h, _ := os.UserHomeDir()
		d = filepath.Join(h, ".config")
	}
	return filepath.Join(d, "renstiq", "config.yaml")
}
func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	explicit := path != ""
	if !explicit {
		path = configPath()
	}
	path, pathErr := filepath.Abs(expandHome(path))
	if pathErr != nil {
		return c, pathErr
	}
	m, e := readConfig(path, "config")
	if errors.Is(e, os.ErrNotExist) && !explicit {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	if e = decodeMap(m, &c); e != nil {
		return c, e
	}
	c.Source = &path
	for _, p := range append(append([]string{}, c.Discovery.Include...), c.Discovery.Exclude...) {
		if !filepath.IsAbs(expandHome(p)) || !doublestar.ValidatePattern(p) {
			return c, &InputError{fmt.Errorf("discovery pattern must be absolute and valid: %s", p)}
		}
	}
	return c, nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

// LoadPolicy reads only the repository policy; no shared defaults are applied.
func LoadPolicy(dir string) (Policy, bool, error) {
	m, err := readConfig(filepath.Join(dir, "renstiq.yaml"), "repo")
	if err != nil {
		return Policy{}, false, err
	}
	enabled := m["enabled"] == true
	for _, section := range []string{"rules", "on_blocked", "after_merge", "after_repo"} {
		seen := map[string]bool{}
		entries, _ := m[section].([]any)
		for _, value := range entries {
			entry := value.(map[string]any)
			id := entry["id"].(string)
			if seen[id] {
				return Policy{}, enabled, &InputError{fmt.Errorf("%s: duplicate id: %s", section, id)}
			}
			seen[id] = true
			if _, ok := entry["enabled"]; !ok {
				entry["enabled"] = true
			}
		}
	}
	p := Policy{Rules: []Rule{}, OnBlocked: []Instruction{}, AfterMerge: []Instruction{}, AfterRepo: []Instruction{}}
	if err := decodeMap(m, &p); err != nil {
		return p, enabled, err
	}
	return p, enabled, validatePolicy(p)
}
func contains(a []string, s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}
func matchAny(patterns []string, s string) bool {
	for _, p := range patterns {
		if ok, _ := doublestar.Match(p, s); ok {
			return true
		}
	}
	return false
}
