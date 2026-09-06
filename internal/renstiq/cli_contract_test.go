package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestCommandContractsRejectBeforeAnyDependencies(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"merge"}, "--repo is required"},
		{[]string{"merge", "--repo", "repo"}, "--run and --decision are required"},
		{[]string{"feedback", "--repo", "repo", "--run", "r"}, "--run and --decision are required"},
		{[]string{"merge", "--repo", "repo", "--all"}, "flag provided but not defined: -all"},
		{[]string{"inspect"}, "specify exactly one of --repo or --all"},
		{[]string{"inspect", "--repo", "repo", "--all"}, "specify exactly one of --repo or --all"},
		{[]string{"inspect", "--all=false"}, "specify exactly one of --repo or --all"},
		{[]string{"inspect", "--all", "--run", "r"}, "--pr and --run require one --repo"},
		{[]string{"inspect", "--all", "--pr", "1"}, "--pr and --run require one --repo"},
		{[]string{"inspect", "--repo", "repo", "--pr", "0"}, "--pr must be positive"},
		{[]string{"inspect", "--repo", "repo", "--pr", "-1"}, "--pr must be positive"},
		{[]string{"inspect", "--repo", "repo", "--pr", "no"}, "invalid value"},
		{[]string{"inspect", "--repo", "repo", "--run", ""}, "--run requires a nonempty value"},
		{[]string{"inspect", "--repo", "repo", "--state-dir", ""}, "--state-dir requires a nonempty value"},
		{[]string{"inspect", "--repo", "repo", "--decision", "d"}, "flag provided but not defined: -decision"},
		{[]string{"status", "--repo", "repo", "--finish"}, "flag provided but not defined: -finish"},
		{[]string{"status", "--repo", "repo", "--config", "config"}, "status --config is only used with --all"},
		{[]string{"post-merge", "--repo", "repo"}, "post-merge requires --run"},
		{[]string{"post-merge", "--all", "--run", "r"}, "--run requires one --repo"},
		{[]string{"abandon", "--repo", "repo", "--run", "r"}, "abandon requires --run and --reason"},
		{[]string{"abandon", "--repo", "repo", "--run", "r", "--reason", " "}, "--reason requires a nonempty value"},
		{[]string{"abandon", "--all"}, "flag provided but not defined: -all"},
		{[]string{"init", "--repo", "repo", "--config", "config"}, "init accepts either --repo DIR or --config FILE"},
		{[]string{"init", "--repo", ""}, "--repo requires a nonempty value"},
		{[]string{"discover", "--config", ""}, "--config requires a nonempty value"},
		{[]string{"discover", "--pr", "1"}, "flag provided but not defined: -pr"},
		{[]string{"discover", "unexpected"}, "unexpected positional arguments"},
		{[]string{"not-a-command"}, "unknown command: not-a-command"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			// Every dependency is nil: entering execution is an immediate test failure.
			var out, log bytes.Buffer
			code := newCLI(&Application{}, nil).Run(context.Background(), tc.args, nil, &out, &log)
			var result Result
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err, out.String())
			}
			if code != 2 || !strings.Contains(result.Error, tc.want) {
				t.Fatalf("code=%d error=%q want=%q", code, result.Error, tc.want)
			}
		})
	}
}

func TestCommandLocalHelpAndRegistry(t *testing.T) {
	c := newCLI(&Application{}, nil)
	for _, command := range c.commands {
		t.Run(command.name, func(t *testing.T) {
			var out, log bytes.Buffer
			if code := c.Run(context.Background(), []string{command.name, "--help"}, nil, &out, &log); code != 0 {
				t.Fatal(code, log.String())
			}
			if !strings.Contains(out.String(), "renstiq "+command.name) {
				t.Fatal(out.String())
			}
			if command.name == "status" && (strings.Contains(out.String(), "-decision") || strings.Contains(out.String(), "-finish") || strings.Contains(out.String(), "-run")) {
				t.Fatal("unrelated options in help", out.String())
			}
		})
	}
	var out, log bytes.Buffer
	if code := c.Run(context.Background(), []string{"--help"}, nil, &out, &log); code != 0 {
		t.Fatal(code)
	}
	for _, command := range c.commands {
		if !strings.Contains(out.String(), "renstiq "+command.name) {
			t.Fatal("registry command missing from help", command.name)
		}
	}
	out.Reset()
	if code := c.Run(context.Background(), []string{"help", "update"}, nil, &out, &log); code != 0 || !strings.Contains(out.String(), "renstiq update") {
		t.Fatal(code, out.String())
	}
}

func TestParsersProduceCommandSpecificRequests(t *testing.T) {
	got, err := parseInspect([]string{"--repo", "repo", "--pr", "7", "--run", "run", "--config", "config", "--state-dir", "state"}, io.Discard)
	want := InspectRequest{Target: RepoTarget{Repo: "repo"}, PR: 7, RunID: "run", ConfigPath: "config", StateDir: "state"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err)
	}
	discovery, err := parseDiscover([]string{"--config", "explicit"}, io.Discard)
	if err != nil || discovery.ConfigPath != "explicit" {
		t.Fatal(discovery, err)
	}
	post, err := parsePostMerge([]string{"--all", "--finish", "--config", "config"}, io.Discard)
	if err != nil || !post.Target.All || !post.Finish || post.ConfigPath != "config" {
		t.Fatal(post, err)
	}
}

func TestReviewHandlerMapsDecisionAndDoesNotReadItDuringParsing(t *testing.T) {
	decision := validDecision()
	reads, calls := 0, 0
	read := func(path string, in io.Reader) (Decision, error) {
		reads++
		if path != "-" {
			t.Fatal(path)
		}
		b, _ := io.ReadAll(in)
		if string(b) != "stdin" {
			t.Fatal(string(b))
		}
		return decision, nil
	}
	run := func(ctx context.Context, req MergeRequest) (RepoResult, error) {
		calls++
		if req.Repo != "repo" || req.RunID != "run" || req.StateDir != "state" || req.ConfigPath != "config" || !reflect.DeepEqual(req.Decision, decision) {
			t.Fatalf("%+v", req)
		}
		return RepoResult{RunID: req.RunID}, nil
	}
	command := mergeCommand(run, read)
	action, err := command.parse([]string{"--repo", "repo", "--run", "run", "--decision", "-", "--config", "config", "--state-dir", "state"}, io.Discard)
	if err != nil || reads != 0 || calls != 0 {
		t.Fatal(err, reads, calls)
	}
	var out, log bytes.Buffer
	if code := action(context.Background(), strings.NewReader("stdin"), &out, &log); code != 0 {
		t.Fatal(code, out.String())
	}
	if reads != 1 || calls != 1 {
		t.Fatal(reads, calls)
	}
}

func TestDecisionReadFailureNeverEntersUseCase(t *testing.T) {
	command := feedbackCommand(func(context.Context, FeedbackRequest) (RepoResult, error) {
		t.Fatal("use case called")
		return RepoResult{}, nil
	}, func(string, io.Reader) (Decision, error) { return Decision{}, errors.New("invalid decision") })
	var out, log bytes.Buffer
	code := (cli{commands: []cliCommand{command}}).Run(context.Background(), []string{"feedback", "--repo", "repo", "--run", "run", "--decision", "-"}, nil, &out, &log)
	if code != 2 || !strings.Contains(out.String(), "invalid decision") {
		t.Fatal(code, out.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output failed") }

func TestCLIOutputFailuresAreNotReportedAsSuccess(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"version"}, {"schema", "state"}} {
		if code := RunCLI(context.Background(), args, nil, failingWriter{}, io.Discard); code != 1 {
			t.Fatal(args, code)
		}
	}
	if code := emitResult(failingWriter{}, io.Discard, Result{}, nil); code != 1 {
		t.Fatal(code)
	}
	if code := runTestUpdateCLI(context.Background(), nil, failingWriter{}, io.Discard, func(context.Context) (UpdateResult, error) { return UpdateResult{}, nil }); code != 1 {
		t.Fatal(code)
	}
}
