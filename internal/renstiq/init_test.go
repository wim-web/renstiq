package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func runInit(t *testing.T, args ...string) (int, Result) {
	t.Helper()
	var out, log bytes.Buffer
	code := runTestCLI(context.Background(), append([]string{"init"}, args...), nil, &out, &log, func(context.Context, Config, io.Writer) (*GitHub, error) {
		t.Fatal("init must not access GitHub")
		return nil, nil
	})
	var result Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout=%q stderr=%q: %v", out.String(), log.String(), err)
	}
	assertCLIOutputSchema(t, "result", out.Bytes())
	return code, result
}

func TestInitCommonConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	code, r := runInit(t)
	if code != 0 || r.Init == nil || !r.Init.Created || r.Init.Scope != "common" || r.Init.Path != configPath() {
		t.Fatalf("code=%d result=%+v", code, r)
	}
	c, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Discovery.Include) != 0 || len(c.Defaults) != 0 {
		t.Fatal("init populated environment-specific settings")
	}
	info, err := os.Stat(configPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	original := "version: 1\ndiscovery:\n  include: []\n# user settings\n"
	writeFile(t, configPath(), original)
	code, r = runInit(t)
	if code != 1 || r.Init.Created || r.Error == "" {
		t.Fatalf("existing config accepted: %+v", r)
	}
	got, err := os.ReadFile(configPath())
	if err != nil || string(got) != original {
		t.Fatal("existing settings changed", err)
	}
}

func TestInitExplicitPathIndependentOfExistingConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeFile(t, configPath(), "invalid common configuration")
	target := filepath.Join(t.TempDir(), "nested", "config.yaml")
	code, r := runInit(t, "--config", target)
	if code != 0 || r.Init.Path != target {
		t.Fatalf("code=%d result=%+v", code, r)
	}
	if _, err := LoadConfig(target); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(configPath())
	if string(got) != "invalid common configuration" {
		t.Fatal("modified default config")
	}
}

func TestInitRepoConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeFile(t, configPath(), "invalid common configuration")
	dir := t.TempDir()
	mustGit(t, dir, "init", "--initial-branch=main")
	code, r := runInit(t, "--repo", dir)
	canonical, err := canonicalDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(canonical, "renstiq.yaml")
	if code != 0 || r.Init == nil || !r.Init.Created || r.Init.Scope != "repo" || r.Init.Path != target {
		t.Fatalf("code=%d result=%+v", code, r)
	}
	m, err := readConfig(target, "repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m["enabled"] != true {
		t.Fatal("repo config must only opt in and inherit defaults", m)
	}
	c := DefaultConfig()
	c.Defaults = map[string]any{"checks": map[string]any{"minimum": 2}}
	p, _, err := LoadPolicy(dir, c)
	if err != nil || p.Checks.Minimum != 2 {
		t.Fatal("shared defaults not inherited", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatal(info.Mode())
	}
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	code, r = runInit(t, "--repo", dir)
	if code != 1 || r.Init.Created {
		t.Fatal("existing repo config replaced")
	}
	after, _ := os.ReadFile(target)
	if !bytes.Equal(before, after) {
		t.Fatal("existing repo config changed")
	}
}

func TestInitRepoRequiresRoot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	mustGit(t, dir, "init", "--initial-branch=main")
	child := filepath.Join(dir, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{child, t.TempDir()} {
		code, r := runInit(t, "--repo", path)
		if code != 1 || r.Error == "" {
			t.Fatalf("non-root accepted: %+v", r)
		}
		if _, err := os.Stat(filepath.Join(path, "renstiq.yaml")); !os.IsNotExist(err) {
			t.Fatal("created config outside repo root")
		}
	}
	if _, err := os.Stat(configPath()); !os.IsNotExist(err) {
		t.Fatal("repo init created common config")
	}
}

func TestInitDoesNotReplaceSymlink(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.yaml")
	target := filepath.Join(dir, "config.yaml")
	writeFile(t, existing, "existing settings")
	if err := os.Symlink(existing, target); err != nil {
		t.Fatal(err)
	}
	code, r := runInit(t, "--config", target)
	if code != 1 || r.Init.Created {
		t.Fatal("symlink overwritten")
	}
	if link, err := os.Readlink(target); err != nil || link != existing {
		t.Fatal("symlink changed", err)
	}
	got, _ := os.ReadFile(existing)
	if string(got) != "existing settings" {
		t.Fatal("symlink target changed")
	}
}

func TestInitRejectsConflictingOrEmptyFlags(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, args := range [][]string{{"--repo", ".", "--config", "config.yaml"}, {"--repo", ""}, {"--config", ""}, {"--all"}, {"unexpected"}} {
		code, r := runInit(t, args...)
		if code != 2 || r.Error == "" {
			t.Fatalf("arguments accepted: %v", args)
		}
	}
	if _, err := os.Stat(configPath()); !os.IsNotExist(err) {
		t.Fatal("invalid arguments created a config")
	}
}
