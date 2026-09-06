package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestRecoveryCommandsDoNotLoadConfigPolicyOrAuthentication(t *testing.T) {
	for _, name := range []string{"status", "abandon"} {
		t.Run(name, func(t *testing.T) {
			s, _ := newMemorySession()
			app := &Application{
				LoadConfig: func(string) (Config, error) { t.Fatal("recovery loaded config"); return Config{}, nil },
				LoadPolicy: func(string, Config) (Policy, string, error) {
					t.Fatal("recovery loaded policy")
					return Policy{}, "", nil
				},
				Remotes: func(context.Context, Config) (remotePorts, error) {
					t.Fatal("recovery authenticated")
					return remotePorts{}, nil
				},
				ResolveRepo: func(context.Context, string) (Repository, error) { return Repository{Dir: "/repo", Name: "o/r"}, nil },
				OpenState:   func(string, string) (StateSession, error) { return s, nil }, Now: fixedClock,
			}
			args := []string{name, "--repo", "repo"}
			if name == "abandon" {
				args = append(args, "--run", "run", "--reason", "reconciled")
			}
			var out, log bytes.Buffer
			if code := newCLI(app, nil).Run(context.Background(), args, nil, &out, &log); code != 0 {
				t.Fatal(code, out.String())
			}
			var result Result
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !s.closed || len(result.Results) != 1 || result.Results[0].State == nil {
				t.Fatal(s.closed, result)
			}
		})
	}
}

type inspectionFake struct {
	session **memorySession
	fail    bool
}

func (f *inspectionFake) OpenPullRequests(context.Context, string) ([]PRState, error) {
	return []PRState{{Number: 1, Author: "renovate[bot]"}}, nil
}
func (f *inspectionFake) PullRequestState(context.Context, string, int) (PRState, error) {
	return PRState{Number: 1, Author: "renovate[bot]"}, nil
}
func (f *inspectionFake) WaitPR(context.Context, string, int, Revision, bool) (PullRequest, error) {
	if (*f.session).closed {
		panic("repository lock released before remote inspection")
	}
	if f.fail {
		return PullRequest{}, effectError
	}
	return validPR(), nil
}

func TestApplicationBatchContinuesAndKeepsSessionThroughEffects(t *testing.T) {
	first, _ := newMemorySession()
	second, _ := newMemorySession()
	var active *memorySession
	remote := &inspectionFake{session: &active}
	visits := []string{}
	app := &Application{
		LoadConfig: func(string) (Config, error) { return DefaultConfig(), nil },
		LoadPolicy: func(string, Config) (Policy, string, error) { p := defaultPolicy(); return p, digest(p), nil },
		DiscoverRepos: func(Config) []Discovery {
			return []Discovery{{Path: "first", Status: "enabled"}, {Path: "bad-config", Status: "config_error"}, {Path: "second", Status: "enabled"}}
		},
		ResolveRepo: func(_ context.Context, path string) (Repository, error) {
			visits = append(visits, path)
			return Repository{Dir: path, Name: path}, nil
		},
		OpenState: func(_, repo string) (StateSession, error) {
			if repo == "first" {
				active = first
				remote.fail = true
			} else {
				active = second
				remote.fail = false
			}
			return active, nil
		},
		Remotes: func(context.Context, Config) (remotePorts, error) { return remotePorts{Inspect: remote}, nil }, Now: fixedClock,
	}
	batch, err := app.Inspect(context.Background(), InspectRequest{Target: RepoTarget{All: true}})
	if err != nil || len(batch.Results) != 2 || batch.Results[0].Error == "" || batch.Results[1].Error != "" {
		t.Fatal(batch, err)
	}
	if !first.closed || !second.closed || !reflect.DeepEqual(visits, []string{"first", "second"}) {
		t.Fatal(visits, first.closed, second.closed)
	}
	if code := emitResult(io.Discard, io.Discard, batchOutput(batch), nil); code != 1 {
		t.Fatal("partial failure exit", code)
	}
}

func TestPolicyFailureStillReleasesSession(t *testing.T) {
	s, _ := newMemorySession()
	app := &Application{
		LoadConfig:  func(string) (Config, error) { return Config{}, nil },
		LoadPolicy:  func(string, Config) (Policy, string, error) { return Policy{}, "", effectError },
		ResolveRepo: func(context.Context, string) (Repository, error) { return Repository{Name: "o/r"}, nil },
		OpenState:   func(string, string) (StateSession, error) { return s, nil },
		Remotes:     func(context.Context, Config) (remotePorts, error) { return remotePorts{}, nil },
	}
	batch, err := app.Inspect(context.Background(), InspectRequest{Target: RepoTarget{Repo: "repo"}})
	if err != nil || batch.Results[0].Error != effectError.Error() || !s.closed {
		t.Fatal(batch, err, s.closed)
	}
}

func TestConfigurationErrorsRetainInputClassification(t *testing.T) {
	app := &Application{LoadConfig: func(string) (Config, error) { return Config{}, effectError }}
	_, err := app.Inspect(context.Background(), InspectRequest{Target: RepoTarget{Repo: "repo"}})
	var input *InputError
	if !errors.As(err, &input) || !errors.Is(err, effectError) {
		t.Fatal(err)
	}
	if code := emitResult(io.Discard, io.Discard, Result{}, err); code != 2 {
		t.Fatal(code)
	}
}
