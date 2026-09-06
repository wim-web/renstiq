package renstiq

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCompletionScriptsWithoutDependencies(t *testing.T) {
	for _, shell := range []string{"fish", "bash", "zsh", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			var out, log bytes.Buffer
			// No application dependencies or updater are available during completion.
			code := newCLI(&Application{}, nil).Run(context.Background(), []string{"completion", shell}, nil, &out, &log)
			if code != 0 || log.Len() != 0 || !strings.Contains(out.String(), "renstiq") || !strings.Contains(out.String(), "__complete") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), log.String())
			}
			if shell == "fish" && !strings.Contains(out.String(), "complete -c renstiq") {
				t.Fatal("missing fish completion registration", out.String())
			}
		})
	}
}

func TestCompletionCandidatesWithoutDependencies(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		want      []string
		absent    []string
		directive cobra.ShellCompDirective
	}{
		{"commands", []string{""}, []string{"discover", "config", "pr", "completion", "update"}, []string{"inspect", "merge", "status", "run", "view"}, cobra.ShellCompDirectiveNoFileComp},
		{"pr flags", []string{"pr", "list", "--"}, []string{"--repo"}, []string{"--run", "--decision", "--finish", "--state-dir"}, cobra.ShellCompDirectiveNoFileComp},
		{"config flags", []string{"config", "show", "--repo", "repo", "--"}, []string{"--config"}, []string{"--all"}, cobra.ShellCompDirectiveNoFileComp},
		{"used flag", []string{"pr", "list", "--repo", "repo", "--"}, []string{"--all"}, []string{"--repo"}, cobra.ShellCompDirectiveNoFileComp},
		{"schema", []string{"schema", ""}, []string{"config", "repo", "config-show", "pr-list", "discover", "result"}, []string{"decision", "state", "post-input"}, cobra.ShellCompDirectiveNoFileComp},
		{"schema prefix", []string{"schema", "pr"}, []string{"pr-list"}, []string{"config"}, cobra.ShellCompDirectiveNoFileComp},
		{"repo directories", []string{"pr", "list", "--repo", ""}, nil, nil, cobra.ShellCompDirectiveFilterDirs},
		{"config files", []string{"discover", "--config", ""}, nil, nil, cobra.ShellCompDirectiveDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, log bytes.Buffer
			args := append([]string{"__complete"}, tc.args...)
			code := newCLI(&Application{}, nil).Run(context.Background(), args, nil, &out, &log)
			if code != 0 {
				t.Fatal(code, out.String(), log.String())
			}
			lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
			if got := lines[len(lines)-1]; got != fmt.Sprintf(":%d", tc.directive) {
				t.Fatalf("directive=%q want=%d stdout=%q stderr=%q", got, tc.directive, out.String(), log.String())
			}
			candidates := map[string]bool{}
			for _, line := range lines[:len(lines)-1] {
				candidate, _, _ := strings.Cut(line, "\t")
				candidates[candidate] = true
			}
			for _, want := range tc.want {
				if !candidates[want] {
					t.Errorf("missing %q in %q", want, out.String())
				}
			}
			for _, absent := range tc.absent {
				if candidates[absent] {
					t.Errorf("unexpected %q in %q", absent, out.String())
				}
			}
		})
	}
}

func TestCompletionRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"completion", "fish", "extra"}, {"completion", "fish", "--unknown"}} {
		var out, log bytes.Buffer
		if code := newCLI(&Application{}, nil).Run(context.Background(), args, nil, &out, &log); code != 2 || out.Len() != 0 || log.Len() == 0 {
			t.Fatal(args, code, out.String(), log.String())
		}
	}
}
