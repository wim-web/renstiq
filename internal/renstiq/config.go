package renstiq

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/xeipuuv/gojsonschema"
	"gopkg.in/yaml.v3"
)

//go:embed schemas/*.json
var schemas embed.FS

type CheckRequirement struct {
	Name     string `json:"name"`
	Workflow string `json:"workflow,omitempty"`
	AppID    int64  `json:"app_id,omitempty"`
}
type Checks struct {
	Minimum    int                `json:"minimum"`
	Required   []CheckRequirement `json:"required"`
	AllSuccess bool               `json:"all_success"`
}
type Match struct {
	Files        []string `json:"changed_files_any"`
	Dependencies []string `json:"dependencies"`
	Types        []string `json:"update_types"`
}
type Rule struct {
	ID           string       `json:"id"`
	Files        []string     `json:"files"`
	Dependencies []string     `json:"dependencies"`
	Types        []string     `json:"update_types"`
	Checks       *ChecksPatch `json:"checks,omitempty"`
	Instructions string       `json:"instructions,omitempty"`
}
type ChecksPatch struct {
	Minimum    *int               `json:"minimum,omitempty"`
	Required   []CheckRequirement `json:"required"`
	AllSuccess *bool              `json:"all_success,omitempty"`
}
type PostCommand struct {
	ID             string   `json:"id"`
	Timing         string   `json:"timing"`
	Match          Match    `json:"match"`
	Command        []string `json:"command"`
	WorkingDir     string   `json:"working_dir,omitempty"`
	RequiresReview bool     `json:"requires_review"`
}
type Policy struct {
	PullRequests struct {
		Authors       []string `json:"authors"`
		Bases         []string `json:"base_branches"`
		Heads         []string `json:"head_branches"`
		CommitAuthors []string `json:"commit_authors"`
		Files         []string `json:"files"`
	} `json:"pull_requests"`
	Checks Checks `json:"checks"`
	Merge  struct {
		Method       string `json:"method"`
		RequireClean bool   `json:"require_clean"`
		DeleteBranch bool   `json:"delete_branch"`
	} `json:"merge"`
	Review struct {
		Instructions string `json:"instructions"`
	} `json:"review"`
	Rules    []Rule `json:"rules"`
	Feedback struct {
		CommentOn []string `json:"comment_on"`
		Labels    []string `json:"labels"`
	} `json:"feedback"`
	PostMerge  []PostCommand `json:"post_merge"`
	WorkingDir string        `json:"working_dir,omitempty"`
}
type Retry struct {
	MaxAttempts     int     `json:"max_attempts"`
	IntervalSeconds float64 `json:"interval_seconds"`
}
type Config struct {
	Version   int `json:"version"`
	Discovery struct {
		Include []string `json:"include"`
		Exclude []string `json:"exclude"`
	} `json:"discovery"`
	Retry Retry `json:"retry"`
	CI    struct {
		PollSeconds float64 `json:"poll_seconds"`
	} `json:"ci"`
	Defaults map[string]any `json:"defaults"`
}

func DefaultConfig() Config {
	c := Config{Version: 1, Retry: Retry{3, 2}, Defaults: map[string]any{}}
	c.CI.PollSeconds = 15
	return c
}
func defaultPolicy() Policy {
	var p Policy
	p.PullRequests.Authors = []string{"app/renovate", "renovate[bot]"}
	p.PullRequests.Bases = []string{"main"}
	p.Checks.AllSuccess = true
	p.Merge.Method = "squash"
	p.Feedback.CommentOn = []string{"compatibility", "human_review", "resolved"}
	p.Feedback.Labels = []string{"renovate-needs-manual-review"}
	return p
}
func Schema(name string) ([]byte, error) {
	switch name {
	case "result":
		return outputSchema(Result{})
	case "post-input":
		return outputSchema(PostInput{})
	case "state":
		return outputSchema(State{})
	}
	return schemas.ReadFile("schemas/" + name + ".json")
}
func validateSchema(name string, v any) error {
	b, e := Schema(name)
	if e != nil {
		return e
	}
	r, e := gojsonschema.Validate(gojsonschema.NewBytesLoader(b), gojsonschema.NewGoLoader(v))
	if e != nil {
		return e
	}
	if !r.Valid() {
		var a []string
		for _, e := range r.Errors() {
			a = append(a, e.String())
		}
		return errors.New(strings.Join(a, "; "))
	}
	return nil
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
func readConfig(path, name string) (map[string]any, error) {
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
func overlay(a, b map[string]any) map[string]any {
	for k, v := range b {
		bm, ok := v.(map[string]any)
		am, aok := a[k].(map[string]any)
		if ok && aok {
			a[k] = overlay(am, bm)
		} else {
			a[k] = v
		}
	}
	return a
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
	m, e := readConfig(path, "config")
	if errors.Is(e, os.ErrNotExist) && !explicit {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	if e = decodeMap(overlay(asMap(c), m), &c); e != nil {
		return c, e
	}
	for _, p := range append(append([]string{}, c.Discovery.Include...), c.Discovery.Exclude...) {
		if !filepath.IsAbs(expandHome(p)) || !doublestar.ValidatePattern(p) {
			return c, fmt.Errorf("discovery pattern must be absolute and valid: %s", p)
		}
	}
	p := defaultPolicy()
	if e = decodeMap(overlay(asMap(p), c.Defaults), &p); e != nil {
		return c, e
	}
	return c, validatePolicy(p)
}
func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}
func LoadPolicy(dir string, c Config) (Policy, string, error) {
	p := defaultPolicy()
	m, e := readConfig(filepath.Join(dir, "renstiq.yaml"), "repo")
	if e != nil {
		return p, "", e
	}
	if m["enabled"] != true {
		return p, "", errors.New("repository must explicitly set enabled: true")
	}
	delete(m, "version")
	delete(m, "enabled")
	v := overlay(overlay(asMap(p), c.Defaults), m)
	if e = decodeMap(v, &p); e != nil {
		return p, "", e
	}
	if e = validatePolicy(p); e != nil {
		return p, "", e
	}
	return p, digest(p), nil
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func validatePolicy(p Policy) error {
	ids := map[string]bool{}
	for _, r := range p.Rules {
		if ids[r.ID] || len(r.Files) == 0 || len(r.Types) == 0 {
			return fmt.Errorf("invalid or duplicate rule: %s", r.ID)
		}
		ids[r.ID] = true
		for _, t := range r.Types {
			if !contains([]string{"patch", "minor", "major", "digest", "pin", "lockfile", "unknown"}, t) {
				return fmt.Errorf("unknown update type: %s", t)
			}
		}
		for _, g := range r.Files {
			if !doublestar.ValidatePattern(g) {
				return fmt.Errorf("invalid glob: %s", g)
			}
		}
	}
	ids = map[string]bool{}
	for _, a := range p.PostMerge {
		if ids[a.ID] || len(a.Command) == 0 || strings.TrimSpace(a.Command[0]) == "" {
			return fmt.Errorf("invalid or duplicate post_merge: %s", a.ID)
		}
		ids[a.ID] = true
		for _, g := range a.Match.Files {
			if !doublestar.ValidatePattern(g) {
				return fmt.Errorf("invalid glob: %s", g)
			}
		}
	}
	for _, g := range append(append([]string{}, p.PullRequests.Files...), p.PullRequests.Heads...) {
		if !doublestar.ValidatePattern(g) {
			return fmt.Errorf("invalid glob: %s", g)
		}
	}
	return nil
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
