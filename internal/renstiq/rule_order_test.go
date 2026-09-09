package renstiq

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRulesChooseOneInstructionInOrder(t *testing.T) {
	p, err := repoPolicy(t, `rules:
- id: major
  authors: ['renovate[bot]']
  base_branches: [main]
  labels: [major]
  instructions: skip major
- id: minor-patch
  authors: ['renovate[bot]']
  base_branches: [main]
  labels: [minor, patch]
  instructions: check CI
- id: other
  authors: ['renovate[bot]']
  base_branches: [main]
  instructions: skip other
`)
	if err != nil {
		t.Fatal(err)
	}
	for labels := 0; labels < 8; labels++ {
		pr := validPR()
		for bit, label := range []string{"major", "minor", "patch"} {
			if labels&(1<<bit) != 0 {
				pr.Labels = append(pr.Labels, label)
			}
		}
		want := ResolvedInstruction{"other", "skip other"}
		if labels&1 != 0 {
			want = ResolvedInstruction{"major", "skip major"}
		} else if labels&6 != 0 {
			want = ResolvedInstruction{"minor-patch", "check CI"}
		}
		got := SelectCandidate(p, CandidateFacts{PR: pr})
		if got.Status != SelectionCandidate || !reflect.DeepEqual(got.Review, []ResolvedInstruction{want}) || !reflect.DeepEqual(got.ReviewIDs, []string{want.ID}) {
			t.Fatal(pr.Labels, got)
		}
	}
	pr := validPR()
	pr.Labels = []string{"major", "patch"}
	p.Rules[0], p.Rules[1] = p.Rules[1], p.Rules[0]
	if got := SelectCandidate(p, CandidateFacts{PR: pr}); got.ReviewIDs[0] != "minor-patch" {
		t.Fatal("source order ignored", got)
	}
	for _, change := range []func(*PRInfo){func(pr *PRInfo) { pr.Author = "human" }, func(pr *PRInfo) { pr.Base = "develop" }} {
		pr := validPR()
		change(&pr)
		if got := SelectCandidate(p, CandidateFacts{PR: pr}); got.Status != SelectionExcluded || len(got.Review) != 0 {
			t.Fatal(got)
		}
	}
}

func TestEarlierUnknownRuleDoesNotFallThrough(t *testing.T) {
	for _, kind := range []string{"labels", "updates", "files", "commits"} {
		t.Run(kind, func(t *testing.T) {
			p := testPolicy()
			p.Rules = []Rule{{Entry: Entry{ID: "first", Enabled: true}, Instructions: "first"}, {Entry: Entry{ID: "fallback", Enabled: true}, Instructions: "fallback"}}
			f := CandidateFacts{PR: validPR()}
			switch kind {
			case "labels":
				p.Rules[0].Labels = []string{"major"}
				f.PR.LabelsKnown = false
			case "updates":
				p.Rules[0].Types = []string{"major"}
				f.PR.UpdatesComplete = false
			case "files":
				p.Rules[0].Files = []string{"go.mod"}
			case "commits":
				p.Rules[0].CommitAuthors = []string{"renovate[bot]"}
			}
			got := SelectCandidate(p, f)
			if got.Status != SelectionUnknown || len(got.Review) != 0 || !strings.HasPrefix(got.Reasons[0], "first:") {
				t.Fatal(got)
			}
			p.Rules[0].Enabled = false
			if got := SelectCandidate(p, f); got.Status != SelectionCandidate || got.ReviewIDs[0] != "fallback" {
				t.Fatal(got)
			}
			p.Rules[0].Enabled = true
			p.Rules[0].Authors = []string{"human"}
			if got := SelectCandidate(p, f); got.Status != SelectionCandidate || got.ReviewIDs[0] != "fallback" {
				t.Fatal("known mismatch did not permit later rule", got)
			}
		})
	}
}

func TestFirstMatchDoesNotReadLaterRules(t *testing.T) {
	p := testPolicy()
	p.Rules = append(p.Rules, Rule{Entry: Entry{ID: "later", Enabled: true}, Files: []string{"**"}, CommitAuthors: []string{"human"}, Types: []string{"major"}, Instructions: "later"})
	pr := validPR()
	pr.UpdatesComplete = false
	reader := listReaderStub{
		list: func() ([]PRInfo, error) { return []PRInfo{pr}, nil },
		details: func(PRInfo, bool, bool) (CandidateFacts, error) {
			t.Fatal("later rule fetched details")
			return CandidateFacts{}, nil
		},
	}
	got, err := listCandidates(context.Background(), reader, emptyPRResult(), p, false)
	if err != nil || !got.Complete || len(got.PullRequests) != 1 || !reflect.DeepEqual(got.PullRequests[0].Review, []ResolvedInstruction{{"target", "review"}}) {
		t.Fatal(got, err)
	}
}

func TestRuleSelectionFetchesDetailsInStages(t *testing.T) {
	for _, failure := range []string{"", "files", "commits", "snapshot"} {
		t.Run(failure, func(t *testing.T) {
			p := testPolicy()
			p.Rules = []Rule{
				{Entry: Entry{ID: "go", Enabled: true}, Files: []string{"go.mod"}, Instructions: "go"},
				{Entry: Entry{ID: "package", Enabled: true}, Files: []string{"package.json"}, CommitAuthors: []string{"renovate[bot]"}, Instructions: "package"},
				{Entry: Entry{ID: "fallback", Enabled: true}, Instructions: "fallback"},
			}
			calls := 0
			reader := listReaderStub{
				list: func() ([]PRInfo, error) { return []PRInfo{validPR()}, nil },
				details: func(pr PRInfo, files, commits bool) (CandidateFacts, error) {
					calls++
					f := CandidateFacts{PR: pr}
					if calls == 1 {
						if !files || commits {
							t.Fatal(files, commits)
						}
						if failure == "files" {
							return f, errors.New("files unavailable")
						}
						f.FilesComplete = true
						f.Files = []ChangedFile{{Filename: "package.json"}}
					} else if calls == 2 {
						if files || !commits {
							t.Fatal("already fetched files requested again", files, commits)
						}
						if failure == "commits" {
							return f, errors.New("commits unavailable")
						}
						if failure == "snapshot" {
							f.Changed = true
							return f, errors.New("PR changed")
						}
						f.CommitsComplete = true
						f.CommitAuthors = []string{"renovate[bot]"}
					} else {
						t.Fatal("repeated detail fetch")
					}
					return f, nil
				},
			}
			got, err := listCandidates(context.Background(), reader, emptyPRResult(), p, true)
			wantCalls := 2
			if failure == "files" {
				wantCalls = 1
			}
			if calls != wantCalls || len(got.PullRequests) != 1 {
				t.Fatal(calls, got, err)
			}
			if failure == "" {
				if err != nil || !got.Complete || !reflect.DeepEqual(got.PullRequests[0].Review, []ResolvedInstruction{{"package", "package"}}) {
					t.Fatal(got, err)
				}
			} else if err == nil || got.Complete || got.PullRequests[0].Status != SelectionUnknown || len(got.PullRequests[0].Review) != 0 {
				t.Fatal(got, err)
			}
		})
	}
}

func TestRepoCommandsIgnoreDiscoveryPolicy(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeFile(t, configPath(), "version: 2\ndefaults: {merge: {method: merge}}\n")
	dir := cliRepo(t, t.TempDir(), "repo", "https://github.com/o/r.git")
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 2\nenabled: true\nmerge: {method: squash}\nrules:\n- id: local\n  instructions: local only\n")
	var out, log bytes.Buffer
	app := newApplication(&log)
	app.LoadConfig = func(string) (Config, error) { t.Fatal("repo command loaded discovery config"); return Config{}, nil }
	app.Reader = func(context.Context, GitHubAPIReadRetry) (PRListReader, error) {
		return listReaderStub{list: func() ([]PRInfo, error) { return []PRInfo{validPR()}, nil }}, nil
	}
	for _, args := range [][]string{{"config", "show", "--repo", dir}, {"pr", "list", "--repo", dir}} {
		out.Reset()
		log.Reset()
		if code := newCLI(app, nil).Run(context.Background(), args, nil, &out, &log); code != 0 {
			t.Fatal(code, out.String(), log.String())
		}
		assertCLIOutputSchema(t, args[0]+"-"+args[1], out.Bytes())
		if !strings.Contains(out.String(), "local only") || strings.Contains(out.String(), "common") {
			t.Fatal(out.String())
		}
	}
	for _, args := range [][]string{{"config", "show", "--config", "x"}, {"pr", "list", "--config", "x"}} {
		out.Reset()
		log.Reset()
		if code := newCLI(&Application{}, nil).Run(context.Background(), args, nil, &out, &log); code != 2 {
			t.Fatal(code, out.String(), log.String())
		}
	}
}
