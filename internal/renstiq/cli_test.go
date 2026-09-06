package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func cliRepo(t *testing.T, root, name, remote string) {
	t.Helper()
	dir := filepath.Join(root, name)
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: true\n")
	mustGit(t, dir, "init", "--initial-branch=main")
	mustGit(t, dir, "remote", "add", "origin", remote)
}

func TestCLIDiscoverFiltersStatuses(t *testing.T) {
	first := Discovery{Path: "a-enabled", Status: "enabled", Reason: "explicitly enabled"}
	second := Discovery{Path: "z-enabled", Status: "enabled", Reason: "explicitly enabled"}
	other := []Discovery{
		{Path: "b-disabled", Status: "disabled", Reason: "enabled: true is not explicitly set"},
		{Path: "c-no-config", Status: "no_config", Reason: "renstiq.yaml does not exist"},
		{Path: "d-excluded", Status: "excluded", Reason: "matched discovery.exclude"},
		{Path: "e-config-error", Status: "config_error", Reason: "invalid config"},
		{Path: "f-discovery-error", Status: "discovery_error", Reason: "cannot read directory"},
		{Path: "g-repository-error", Status: "repository_error", Reason: "not a repository root"},
	}
	all := append([]Discovery{first}, other...)
	all = append(all, second)
	cases := []struct {
		name  string
		flags []string
		input []Discovery
		want  []Discovery
		code  int
	}{
		{name: "default", input: all, want: []Discovery{first, second}},
		{name: "all", flags: []string{"--all"}, input: all, want: all, code: 1},
		{name: "explicit false", flags: []string{"--all=false"}, input: all, want: []Discovery{first, second}},
		{name: "no enabled repositories", input: other},
		{name: "empty discovery"},
		{name: "all without errors", flags: []string{"--all"}, input: other[:3], want: other[:3]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := &Application{
				LoadConfig: func(path string) (Config, error) {
					if path != "explicit" {
						t.Fatalf("config path = %q", path)
					}
					return DefaultConfig(), nil
				},
				DiscoverRepos: func(Config) []Discovery { return tc.input },
			}
			args := append([]string{"discover", "--config", "explicit"}, tc.flags...)
			var out, log bytes.Buffer
			code := newCLI(app, nil).Run(context.Background(), args, nil, &out, &log)
			if code != tc.code || log.Len() != 0 {
				t.Fatalf("code=%d want=%d stdout=%q stderr=%q", code, tc.code, out.String(), log.String())
			}
			var result Result
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Discovery, tc.want) {
				t.Fatalf("discovery=%+v want=%+v", result.Discovery, tc.want)
			}
			if err := validateSchema("result", result); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCLIAllContinuesAfterFailures(t *testing.T) {
	_, g := newFake(t)
	root := t.TempDir()
	cliRepo(t, root, "a-fail", "https://github.com/missing/repo.git")
	cliRepo(t, root, "b-ok", "https://github.com/o/r.git")
	writeFile(t, filepath.Join(root, "c-invalid", "renstiq.yaml"), "version: 1\nenabled: true\nunknown: true\n")
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, cfg, "version: 1\ndiscovery:\n  include: ["+strconvQuote(root+"/*/")+"]\n")
	var out, log bytes.Buffer
	code := runTestCLI(context.Background(), []string{"inspect", "--all", "--config", cfg, "--state-dir", t.TempDir()}, strings.NewReader(""), &out, &log, func(context.Context, Config, io.Writer) (*GitHub, error) { return g, nil })
	if code != 1 {
		t.Fatal(code, out.String())
	}
	var result Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 2 || result.Results[0].Error == "" || result.Results[1].Error != "" || len(result.Results[1].PRs) != 1 {
		t.Fatal(out.String())
	}
	if len(result.Discovery) != 3 {
		t.Fatal("discovery omitted failed configuration")
	}
	if err := validateSchema("result", result); err != nil {
		t.Fatal(err)
	}
}
func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }
func TestCLISchemaAndUsage(t *testing.T) {
	for _, name := range []string{"config", "repo", "decision", "result", "post-input", "state"} {
		var out, log bytes.Buffer
		if code := RunCLI(context.Background(), []string{"schema", name}, nil, &out, &log); code != 0 {
			t.Fatal(log.String())
		}
		var v any
		if err := json.Unmarshal(out.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"merge"}, {"inspect", "--repo", "x", "--all"}, {"bad"}, {"discover", "unexpected"}} {
		var out, log bytes.Buffer
		if code := RunCLI(context.Background(), args, nil, &out, &log); code != 2 {
			t.Fatal(args, code)
		}
		var v Result
		if err := json.Unmarshal(out.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
	}
}
func TestStatusWorksAfterConfigCorruption(t *testing.T) {
	root := t.TempDir()
	cliRepo(t, root, "repo", "https://github.com/o/r.git")
	writeFile(t, filepath.Join(root, "repo", "renstiq.yaml"), "bad: config")
	batch, err := newApplication(io.Discard).Status(context.Background(), StatusRequest{Target: RepoTarget{Repo: filepath.Join(root, "repo")}, StateDir: t.TempDir()})
	if err != nil || len(batch.Results) != 1 {
		t.Fatal(batch, err)
	}
	result := batch.Results[0]
	if result.Error != "" || result.State == nil {
		t.Fatal(result)
	}
}

func TestVersionWithoutConfigurationOrAuthentication(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeFile(t, configPath(), "invalid configuration")
	for _, command := range []string{"version", "--version"} {
		var out, log bytes.Buffer
		code := runTestCLI(context.Background(), []string{command}, nil, &out, &log, func(context.Context, Config, io.Writer) (*GitHub, error) {
			t.Fatal("version must not access GitHub")
			return nil, nil
		})
		if code != 0 || !strings.HasPrefix(out.String(), "renstiq ") || log.Len() != 0 {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), log.String())
		}
	}
}
