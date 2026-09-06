package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
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
