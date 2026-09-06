package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIDiscoverFiltersStatusesWithoutLosingErrors(t *testing.T) {
	all := []Discovery{{"enabled", "enabled", "enabled"}, {"disabled", "disabled", "disabled"}, {"none", "no_config", "missing"}, {"excluded", "excluded", "excluded"}, {"bad", "config_error", "invalid"}, {"io", "discovery_error", "unreadable"}, {"repo", "repository_error", "invalid origin"}}
	for _, flag := range []string{"--all=false", "--all"} {
		app := &Application{LoadConfig: func(string) (Config, error) { return DefaultConfig(), nil }, DiscoverRepos: func(Config) []Discovery { return all }}
		var out, log bytes.Buffer
		code := newCLI(app, nil).Run(context.Background(), []string{"discover", flag}, nil, &out, &log)
		var r DiscoveryResult
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		want := 1
		if flag == "--all" {
			want = len(all)
		}
		if code != 2 || len(r.Discovery) != want || len(r.Errors) != 3 || log.Len() == 0 {
			t.Fatal(code, r, log.String())
		}
		if err := validateSchema("discover", asMap(r)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestCLIDiscoverNormalStatesAndIOFailure(t *testing.T) {
	for _, tc := range []struct {
		status string
		code   int
	}{{"disabled", 0}, {"no_config", 0}, {"excluded", 0}, {"discovery_error", 1}, {"repository_error", 1}, {"config_error", 2}} {
		app := &Application{LoadConfig: func(string) (Config, error) { return DefaultConfig(), nil }, DiscoverRepos: func(Config) []Discovery { return []Discovery{{"repo", tc.status, "reason"}} }}
		var out, log bytes.Buffer
		if code := newCLI(app, nil).Run(context.Background(), []string{"discover"}, nil, &out, &log); code != tc.code {
			t.Fatal(tc, code)
		}
		if !json.Valid(out.Bytes()) {
			t.Fatal(out.String())
		}
	}
}
func TestCommandContractsBeforeDependencies(t *testing.T) {
	cases := [][]string{{"pr", "list"}, {"config", "show"}, {"config", "show", "--all"}, {"pr", "list", "--repo", ""}, {"pr", "list", "--repo", "x", "--pr", "1"}, {"pr", "list", "--all"}, {"discover", "extra"}, {"init", "--repo", "x", "--config", "x"}}
	for _, old := range []string{"inspect", "merge", "feedback", "post-merge", "status", "abandon", "view", "validate", "evaluate", "run"} {
		cases = append(cases, []string{old})
	}
	for _, flag := range []string{"--state-dir", "--run", "--decision", "--finish"} {
		cases = append(cases, []string{"pr", "list", "--repo", "x", flag, "x"})
	}
	for _, args := range cases {
		var out, log bytes.Buffer
		if code := newCLI(&Application{}, nil).Run(context.Background(), args, nil, &out, &log); code != 2 || log.Len() == 0 {
			t.Fatal(args, code, out.String(), log.String())
		}
		if !json.Valid(out.Bytes()) {
			t.Fatal(args, out.String())
		}
	}
}
func TestCLISchemaAndHelpWithoutDependencies(t *testing.T) {
	for _, name := range []string{"config", "repo", "config-show", "pr-list", "discover", "result"} {
		var out, log bytes.Buffer
		if code := newCLI(&Application{}, nil).Run(context.Background(), []string{"schema", name}, nil, &out, &log); code != 0 || !json.Valid(out.Bytes()) {
			t.Fatal(name, code, out.String(), log.String())
		}
	}
	for _, name := range []string{"state", "decision", "post-input"} {
		if _, err := Schema(name); err == nil {
			t.Fatal("obsolete schema exists", name)
		}
	}
	for _, args := range [][]string{{"--help"}, {"config", "show", "--help"}, {"pr", "list", "--help"}, {"version"}, {"--version"}} {
		var out, log bytes.Buffer
		if code := newCLI(&Application{}, nil).Run(context.Background(), args, nil, &out, &log); code != 0 || out.Len() == 0 || log.Len() != 0 {
			t.Fatal(args, code, out.String(), log.String())
		}
	}
}
func TestConfigShowOfflineSourcesAndDisabled(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := cliRepo(t, t.TempDir(), "repo", "git@github.com:o/r.git")
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	writeFile(t, filepath.Join(state, "old-state"), "preserve")
	for _, enabled := range []bool{true, false} {
		writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: "+strconvQuoteBool(enabled)+"\n")
		var out, log bytes.Buffer
		app := newApplication(&log)
		app.Reader = func(context.Context, Config) (PRListReader, error) {
			t.Fatal("offline command authenticated")
			return nil, nil
		}
		if code := newCLI(app, nil).Run(context.Background(), []string{"config", "show", "--repo", dir}, nil, &out, &log); code != 0 {
			t.Fatal(code, out.String(), log.String())
		}
		var r ConfigResult
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if r.Repo != "o/r" || r.Enabled == nil || *r.Enabled != enabled || r.Sources == nil || r.Sources.Common != nil || r.Config == nil || len(r.Config.PullRequests.Authors) != 2 {
			t.Fatal(r)
		}
		if err := validateSchema("config-show", asMap(r)); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(t.TempDir(), "common.yaml")
	writeFile(t, cfg, "version: 1\ndefaults:\n  checks:\n    minimum: 2\n")
	r, err := newApplication(io.Discard).ConfigShow(context.Background(), ConfigRequest{Repo: dir, ConfigPath: cfg})
	if err != nil || r.Sources.Common == nil || *r.Sources.Common != cfg || r.Config.Checks.Minimum != 2 {
		t.Fatal(r, err)
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 1 || entries[0].Name() != "old-state" {
		t.Fatal(entries, err)
	}
}
func strconvQuoteBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
func TestConfigurationErrorsDoNotFabricatePolicyOrAuthenticate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := cliRepo(t, t.TempDir(), "repo", "https://github.com/o/r.git")
	for _, data := range []string{"version: 1\nenabled: false\n", "version: 1\nrules: null\n", "missing"} {
		path := filepath.Join(dir, "renstiq.yaml")
		if data == "missing" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		} else {
			writeFile(t, path, data)
		}
		for _, args := range [][]string{{"pr", "list", "--repo", dir}, {"pr", "list", "--repo", dir, "--all"}, {"config", "show", "--repo", dir}} {
			var out, log bytes.Buffer
			app := newApplication(&log)
			app.Reader = func(context.Context, Config) (PRListReader, error) {
				t.Fatal("bad/disabled config authenticated")
				return nil, nil
			}
			code := newCLI(app, nil).Run(context.Background(), args, nil, &out, &log)
			if args[0] == "config" && strings.Contains(data, "false") {
				if code != 0 {
					t.Fatal(code)
				}
				continue
			}
			if code != 2 || log.Len() == 0 || !json.Valid(out.Bytes()) {
				t.Fatal(args, code, out.String(), log.String())
			}
			var payload map[string]any
			_ = json.Unmarshal(out.Bytes(), &payload)
			if _, exists := payload["config"]; exists {
				t.Fatal("fabricated config", payload)
			}
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output failed") }
func TestOutputFailure(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"version"}, {"schema", "repo"}, {"discover"}} {
		app := &Application{LoadConfig: func(string) (Config, error) { return DefaultConfig(), nil }, DiscoverRepos: func(Config) []Discovery { return nil }}
		var log bytes.Buffer
		if code := newCLI(app, nil).Run(context.Background(), args, nil, failingWriter{}, &log); code != 1 || log.Len() == 0 {
			t.Fatal(args, code, log.String())
		}
	}
}

func TestRepeatedCLIInvocationsResetRequests(t *testing.T) {
	app := &Application{LoadConfig: func(string) (Config, error) { return DefaultConfig(), nil }, DiscoverRepos: func(Config) []Discovery {
		return []Discovery{{"a", "enabled", "enabled"}, {"b", "disabled", "disabled"}}
	}}
	runner := newCLI(app, nil)
	for _, args := range [][]string{{"discover", "--all"}, {"discover", "--help"}, {"discover"}} {
		var out, log bytes.Buffer
		if code := runner.Run(context.Background(), args, nil, &out, &log); code != 0 {
			t.Fatal(code, log.String())
		}
		if len(args) > 1 && args[1] == "--help" {
			continue
		}
		var r DiscoveryResult
		_ = json.Unmarshal(out.Bytes(), &r)
		want := 1
		if len(args) > 1 {
			want = 2
		}
		if len(r.Discovery) != want {
			t.Fatal("flags leaked", r)
		}
	}
}
func TestSelectionOutputSchemaRejectsInvalidClassification(t *testing.T) {
	r := emptyPRResult()
	r.Complete = true
	r.OpenRenovateCount = ptr(1)
	r.PullRequests = append(r.PullRequests, PRListItem{PRInfo: validPR(), Selection: SelectCandidate(defaultPolicy(), CandidateFacts{PR: validPR()})})
	if err := validateSchema("pr-list", asMap(r)); err != nil {
		t.Fatal(err)
	}
	r.PullRequests[0].Status = "merge-approved"
	if err := validateSchema("pr-list", asMap(r)); err == nil {
		t.Fatal("schema accepted a merge decision as selection")
	}
}
