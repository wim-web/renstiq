package renstiq

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestPostExecutorEffectOrderingAndNoDuplicate(t *testing.T) {
	s, r := newMemorySession()
	calls := 0
	log := &logFake{events: &s.events}
	executor := PostExecutor{Repo: "o/r", Dir: "/checkout", Home: "/home/test", Journal: s, Now: fixedClock, Logs: logStoreFake{log: log},
		Sync: func(context.Context, string, string, []MergeRecord) error {
			s.events = append(s.events, "sync")
			return nil
		},
		Runner: runnerFunc(func(ctx context.Context, spec CommandSpec) (int, error) {
			calls++
			s.events = append(s.events, "run")
			var durable State
			if err := json.Unmarshal(s.disk, &durable); err != nil {
				t.Fatal(err)
			}
			if durable.Runs[0].op("post:1:build").Status != "running" {
				t.Fatal("child started before durable running intent")
			}
			var input PostInput
			if err := json.Unmarshal(spec.Input, &input); err != nil {
				t.Fatal(err)
			}
			if input.Repo != "o/r" || input.Run != "run" || input.Action != "build" || spec.Dir != "/checkout/frontend" {
				t.Fatal(input, spec.Dir)
			}
			return 0, nil
		}),
	}
	action := PostCommand{ID: "build", Timing: "after_each_merge", WorkingDir: "frontend", Command: []string{"build"}}
	if err := executor.Execute(context.Background(), r, action, "post:1:build", nil); err != nil {
		t.Fatal(err)
	}
	if err := executor.Execute(context.Background(), r, action, "post:1:build", nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"save:pending", "sync", "log:open", "save:running", "run", "log:sync", "save:success", "log:close"}
	if calls != 1 || !reflect.DeepEqual(s.events, want) {
		t.Fatal(calls, s.events)
	}
}

func TestPostExecutorFailureBoundaries(t *testing.T) {
	cases := []struct {
		name                             string
		failSave                         int
		syncErr, openErr, runErr, logErr bool
		cancel                           bool
		want                             string
		calls                            int
	}{
		{name: "pending save", failSave: 1, want: "", calls: 0},
		{name: "sync", syncErr: true, want: "sync_failed", calls: 0},
		{name: "open log", openErr: true, want: "failed", calls: 0},
		{name: "running save", failSave: 2, want: "pending", calls: 0},
		{name: "child exit", runErr: true, want: "failed", calls: 1},
		{name: "log durability", logErr: true, want: "unknown", calls: 1},
		{name: "cancel", cancel: true, want: "unknown", calls: 1},
		{name: "completion save", failSave: 3, want: "running", calls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, r := newMemorySession()
			s.failAt = tc.failSave
			calls := 0
			log := &logFake{events: &s.events}
			if tc.logErr {
				log.syncErr = effectError
			}
			logs := logStoreFake{log: log}
			if tc.openErr {
				logs.err = effectError
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			executor := PostExecutor{Repo: "o/r", Dir: "/checkout", Journal: s, Now: fixedClock, Logs: logs,
				Sync: func(context.Context, string, string, []MergeRecord) error {
					if tc.syncErr {
						return effectError
					}
					return nil
				},
				Runner: runnerFunc(func(context.Context, CommandSpec) (int, error) {
					calls++
					if tc.cancel {
						cancel()
					}
					if tc.runErr {
						return 9, effectError
					}
					return 0, nil
				}),
			}
			action := PostCommand{ID: "build", Command: []string{"build"}}
			if err := executor.Execute(ctx, r, action, "post:1:build", nil); err == nil {
				t.Fatal("failure accepted")
			}
			if calls != tc.calls {
				t.Fatal(calls)
			}
			r = s.reload()
			op := r.op("post:1:build")
			if tc.want == "" {
				if op != nil {
					t.Fatal(op)
				}
				return
			}
			if op == nil || op.Status != tc.want {
				t.Fatal(op)
			}
			if err := executor.Execute(context.Background(), r, action, "post:1:build", nil); err == nil {
				t.Fatal("incomplete operation accepted")
			}
			if calls != tc.calls {
				t.Fatal("child repeated", calls)
			}
			if tc.want == "running" || tc.failSave == 2 {
				if !log.closed {
					t.Fatal("log leaked")
				}
			}
		})
	}
}

func TestPostWorkingDirectoryPrecedence(t *testing.T) {
	for _, tc := range []struct{ policy, action, want string }{
		{"", "", "/repo"}, {"policy", "", "/repo/policy"}, {"policy", "action", "/repo/action"},
		{"ignored", "/absolute", "/absolute"}, {"~/src", "", "/home/test/src"},
	} {
		if got := postWorkingDir("/repo", tc.policy, tc.action, "/home/test"); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestExecRunnerRejectsEmptyCommand(t *testing.T) {
	if code, err := (ExecRunner{}).Run(context.Background(), CommandSpec{}); code != -1 || err == nil {
		t.Fatal(code, err)
	}
}

func TestPostFailurePreservesErrorCause(t *testing.T) {
	s, r := newMemorySession()
	e := PostExecutor{Journal: s, Now: fixedClock, Sync: func(context.Context, string, string, []MergeRecord) error { return effectError }}
	if err := e.Execute(context.Background(), r, PostCommand{}, "post", nil); !errors.Is(err, effectError) {
		t.Fatal(err)
	}
}
