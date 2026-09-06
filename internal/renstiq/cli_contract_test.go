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

	"github.com/spf13/cobra"
)

func singleCommandCLI(newCommand func() *cobra.Command) cli {
	return cli{newCommands: func() []*cobra.Command {
		return []*cobra.Command{newCommand()}
	}}
}

func TestCommandContractsRejectBeforeAnyDependencies(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"merge"}, "--repo is required"},
		{[]string{"merge", "--repo", "repo"}, "--run and --decision are required"},
		{[]string{"feedback", "--repo", "repo", "--run", "r"}, "--run and --decision are required"},
		{[]string{"merge", "--repo", "repo", "--all"}, "unknown flag: --all"},
		{[]string{"inspect"}, "specify exactly one of --repo or --all"},
		{[]string{"inspect", "--repo", "repo", "--all"}, "specify exactly one of --repo or --all"},
		{[]string{"inspect", "--all=false"}, "specify exactly one of --repo or --all"},
		{[]string{"inspect", "--all", "--run", "r"}, "--pr and --run require one --repo"},
		{[]string{"inspect", "--all", "--pr", "1"}, "--pr and --run require one --repo"},
		{[]string{"inspect", "--repo", "repo", "--pr", "0"}, "--pr must be positive"},
		{[]string{"inspect", "--repo", "repo", "--pr", "-1"}, "--pr must be positive"},
		{[]string{"inspect", "--repo", "repo", "--pr", "no"}, "invalid argument"},
		{[]string{"inspect", "--repo", "repo", "--run", ""}, "--run requires a nonempty value"},
		{[]string{"inspect", "--repo", "repo", "--state-dir", ""}, "--state-dir requires a nonempty value"},
		{[]string{"inspect", "--repo", "repo", "--decision", "d"}, "unknown flag: --decision"},
		{[]string{"status", "--repo", "repo", "--finish"}, "unknown flag: --finish"},
		{[]string{"status", "--repo", "repo", "--config", "config"}, "status --config is only used with --all"},
		{[]string{"post-merge", "--repo", "repo"}, "post-merge requires --run"},
		{[]string{"post-merge", "--all", "--run", "r"}, "--run requires one --repo"},
		{[]string{"abandon", "--repo", "repo", "--run", "r"}, "abandon requires --run and --reason"},
		{[]string{"abandon", "--repo", "repo", "--run", "r", "--reason", " "}, "--reason requires a nonempty value"},
		{[]string{"abandon", "--all"}, "unknown flag: --all"},
		{[]string{"init", "--repo", "repo", "--config", "config"}, "init accepts either --repo DIR or --config FILE"},
		{[]string{"init", "--repo", ""}, "--repo requires a nonempty value"},
		{[]string{"discover", "--config", ""}, "--config requires a nonempty value"},
		{[]string{"discover", "--pr", "1"}, "unknown flag: --pr"},
		{[]string{"discover", "unexpected"}, "unexpected positional arguments"},
		{[]string{"not-a-command"}, `unknown command "not-a-command"`},
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
			if code != 2 || result.Command != tc.args[0] || log.Len() != 0 || !strings.Contains(result.Error, tc.want) {
				t.Fatalf("code=%d error=%q want=%q", code, result.Error, tc.want)
			}
		})
	}
}

func TestCommandLocalHelpAndRegistry(t *testing.T) {
	c := newCLI(&Application{}, nil)
	root := c.newRootCommand()
	root.InitDefaultCompletionCmd()
	for _, command := range root.Commands() {
		t.Run(command.Name(), func(t *testing.T) {
			var out, log bytes.Buffer
			if code := c.Run(context.Background(), []string{command.Name(), "--help"}, nil, &out, &log); code != 0 || log.Len() != 0 {
				t.Fatal(code, log.String())
			}
			if !strings.Contains(out.String(), "renstiq "+command.Name()) {
				t.Fatal(out.String())
			}
			if command.Name() == "status" && (strings.Contains(out.String(), "-decision") || strings.Contains(out.String(), "-finish") || strings.Contains(out.String(), "-run")) {
				t.Fatal("unrelated options in help", out.String())
			}
		})
	}
	var out, log bytes.Buffer
	if code := c.Run(context.Background(), []string{"--help"}, nil, &out, &log); code != 0 {
		t.Fatal(code)
	}
	for _, command := range root.Commands() {
		if !strings.Contains(out.String(), command.Name()) {
			t.Fatal("registry command missing from help", command.Name())
		}
	}
	out.Reset()
	if code := c.Run(context.Background(), []string{"help", "update"}, nil, &out, &log); code != 0 || !strings.Contains(out.String(), "renstiq update") {
		t.Fatal(code, out.String())
	}
}

func TestCommandsProduceCommandSpecificRequests(t *testing.T) {
	var inspection InspectRequest
	var discovery DiscoverRequest
	var post PostMergeRequest
	calls := 0
	cases := []struct {
		newCommand func() *cobra.Command
		args       []string
	}{
		{func() *cobra.Command {
			return inspectCommand(func(_ context.Context, req InspectRequest) (BatchResult, error) {
				inspection = req
				calls++
				return BatchResult{}, nil
			})
		}, []string{"inspect", "--repo", "repo", "--pr", "7", "--run", "run", "--config", "config", "--state-dir", "state"}},
		{func() *cobra.Command {
			return discoverCommand(func(_ context.Context, req DiscoverRequest) (BatchResult, error) {
				discovery = req
				calls++
				return BatchResult{}, nil
			})
		}, []string{"discover", "--config=explicit"}},
		{func() *cobra.Command {
			return postMergeCommand(func(_ context.Context, req PostMergeRequest) (BatchResult, error) {
				post = req
				calls++
				return BatchResult{}, nil
			})
		}, []string{"post-merge", "--all", "--finish", "--config", "config"}},
	}
	for _, tc := range cases {
		var out, log bytes.Buffer
		if code := singleCommandCLI(tc.newCommand).Run(context.Background(), tc.args, nil, &out, &log); code != 0 {
			t.Fatal(code, out.String(), log.String())
		}
	}
	want := InspectRequest{Target: RepoTarget{Repo: "repo"}, PR: 7, RunID: "run", ConfigPath: "config", StateDir: "state"}
	if calls != 3 || !reflect.DeepEqual(inspection, want) || discovery.ConfigPath != "explicit" || !post.Target.All || !post.Finish || post.ConfigPath != "config" {
		t.Fatal(calls, inspection, discovery, post)
	}
}

func TestCLIRepeatedRunsResetFlags(t *testing.T) {
	var requests []InspectRequest
	c := singleCommandCLI(func() *cobra.Command {
		return inspectCommand(func(_ context.Context, req InspectRequest) (BatchResult, error) {
			requests = append(requests, req)
			return BatchResult{}, nil
		})
	})
	cases := []struct {
		args []string
		code int
	}{
		{[]string{"inspect", "--repo", "repo-a", "--pr", "7", "--run", "run", "--config", "config", "--state-dir", "state"}, 0},
		{[]string{"inspect", "--repo", "repo-b"}, 0},
		{[]string{"inspect", "--all"}, 0},
		{[]string{"inspect", "--repo", "repo-c"}, 0},
		{[]string{"inspect", "--repo", "repo-d", "--pr", "0"}, 2},
		{[]string{"inspect", "--repo", "repo-e"}, 0},
	}
	for _, tc := range cases {
		var out, log bytes.Buffer
		if code := c.Run(context.Background(), tc.args, nil, &out, &log); code != tc.code {
			t.Fatalf("args=%v code=%d want=%d stdout=%q stderr=%q", tc.args, code, tc.code, out.String(), log.String())
		}
	}
	want := []InspectRequest{
		{Target: RepoTarget{Repo: "repo-a"}, PR: 7, RunID: "run", ConfigPath: "config", StateDir: "state"},
		{Target: RepoTarget{Repo: "repo-b"}},
		{Target: RepoTarget{All: true}},
		{Target: RepoTarget{Repo: "repo-c"}},
		{Target: RepoTarget{Repo: "repo-e"}},
	}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%+v want=%+v", requests, want)
	}
}

func TestCLIRepeatedRunsAfterHelp(t *testing.T) {
	c := newCLI(&Application{}, nil)
	for _, args := range [][]string{{"--help"}, {"version", "--help"}} {
		var out, log bytes.Buffer
		if code := c.Run(context.Background(), args, nil, &out, &log); code != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Fatal(args, code, out.String(), log.String())
		}
	}
	for _, args := range [][]string{{"--version"}, {"version"}} {
		var out, log bytes.Buffer
		if code := c.Run(context.Background(), args, nil, &out, &log); code != 0 || out.String() != "renstiq "+buildVersion()+"\n" || log.Len() != 0 {
			t.Fatal(args, code, out.String(), log.String())
		}
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
	c := singleCommandCLI(func() *cobra.Command { return mergeCommand(run, read) })
	args := []string{"--repo", "repo", "--run", "run", "--decision", "-", "--config", "config", "--state-dir", "state"}
	err := mergeCommand(run, read).ParseFlags(args)
	if err != nil || reads != 0 || calls != 0 {
		t.Fatal(err, reads, calls)
	}
	var out, log bytes.Buffer
	if code := c.Run(context.Background(), append([]string{"merge"}, args...), strings.NewReader("stdin"), &out, &log); code != 0 {
		t.Fatal(code, out.String())
	}
	if reads != 1 || calls != 1 {
		t.Fatal(reads, calls)
	}
}

func TestDecisionReadFailureNeverEntersUseCase(t *testing.T) {
	c := singleCommandCLI(func() *cobra.Command {
		return feedbackCommand(func(context.Context, FeedbackRequest) (RepoResult, error) {
			t.Fatal("use case called")
			return RepoResult{}, nil
		}, func(string, io.Reader) (Decision, error) { return Decision{}, errors.New("invalid decision") })
	})
	var out, log bytes.Buffer
	code := c.Run(context.Background(), []string{"feedback", "--repo", "repo", "--run", "run", "--decision", "-"}, nil, &out, &log)
	if code != 2 || !strings.Contains(out.String(), "invalid decision") {
		t.Fatal(code, out.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output failed") }

func TestCLIOutputFailuresAreNotReportedAsSuccess(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"inspect", "--help"}, {"--version"}, {"version"}, {"schema", "state"}, {"completion", "fish"}} {
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
