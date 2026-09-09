package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReviewReferencesCollectAllApplicableInstructions(t *testing.T) {
	p := testPolicy()
	p.PullRequests.Filters = []Filter{
		{Entry: Entry{ID: "all", Enabled: true}},
		{Entry: Entry{ID: "go", Enabled: true}, Files: []string{"go.mod"}},
		{Entry: Entry{ID: "disabled"}},
		{Entry: Entry{ID: "other", Enabled: true}, Bases: []string{"develop"}},
	}
	p.Review = []Instruction{
		{Entry: Entry{ID: "common", Enabled: true}, Instructions: "common instructions"},
		{Entry: Entry{ID: "go-review", Enabled: true}, Match: Match{FilterIDs: []string{"go"}}, Instructions: "go instructions"},
		{Entry: Entry{ID: "multiple", Enabled: true}, Match: Match{FilterIDs: []string{"all", "go", "go"}, Dependencies: []string{"example"}, Types: []string{"patch"}}, Instructions: "once"},
		{Entry: Entry{ID: "minor", Enabled: true}, Match: Match{FilterIDs: []string{"go"}, Types: []string{"minor"}}, Instructions: "not patch"},
		{Entry: Entry{ID: "excluded", Enabled: true}, Match: Match{FilterIDs: []string{"go"}}, Exclude: Match{Dependencies: []string{"example"}}, Instructions: "excluded"},
		{Entry: Entry{ID: "disabled-ref", Enabled: true}, Match: Match{FilterIDs: []string{"disabled"}}, Instructions: "disabled filter"},
		{Entry: Entry{ID: "other-ref", Enabled: true}, Match: Match{FilterIDs: []string{"other"}}, Instructions: "other filter"},
		{Entry: Entry{ID: "off"}, Match: Match{FilterIDs: []string{"go"}}, Instructions: "disabled review"},
	}
	f := CandidateFacts{PR: validPR(), FilesComplete: true, Files: []ChangedFile{{Filename: "go.mod"}}}
	want := []ResolvedInstruction{{"common", "common instructions"}, {"go-review", "go instructions"}, {"multiple", "once"}}
	for order := 0; order < 2; order++ {
		got := SelectCandidate(p, f)
		if got.Status != SelectionCandidate || !reflect.DeepEqual(got.Review, want) || !reflect.DeepEqual(got.ReviewIDs, []string{"common", "go-review", "multiple"}) {
			t.Fatalf("order %d: %+v", order, got)
		}
		p.PullRequests.Filters[0], p.PullRequests.Filters[1] = p.PullRequests.Filters[1], p.PullRequests.Filters[0]
	}
	// Referenced file filters still require all changed paths to match.
	f.Files = append(f.Files, ChangedFile{Filename: "package.json"})
	got := SelectCandidate(p, f)
	if got.Status != SelectionCandidate || !reflect.DeepEqual(got.Review, []ResolvedInstruction{want[0], want[2]}) {
		t.Fatal(got)
	}
}

func TestReviewReferenceUncertainty(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Policy, *CandidateFacts)
		want SelectionStatus
		ids  []string
	}{
		{"unknown files", func(*Policy, *CandidateFacts) {}, SelectionUnknown, []string{}},
		{"unknown metadata", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[1].Files = nil
			p.PullRequests.Filters[1].Types = []string{"patch"}
			f.PR.UpdatesComplete = false
		}, SelectionUnknown, []string{}},
		{"unknown commits", func(p *Policy, _ *CandidateFacts) {
			p.PullRequests.Filters[1].Files = nil
			p.PullRequests.Filters[1].CommitAuthors = []string{"renovate[bot]"}
		}, SelectionUnknown, []string{}},
		{"another reference matches", func(p *Policy, _ *CandidateFacts) {
			p.Review[1].Match.FilterIDs = []string{"details", "all"}
		}, SelectionCandidate, []string{"common", "scoped"}},
		{"other review conditions rule out unknown reference", func(p *Policy, _ *CandidateFacts) {
			p.Review[1].Match.Types = []string{"minor"}
		}, SelectionCandidate, []string{"common"}},
		{"exclusion rules out unknown reference", func(p *Policy, _ *CandidateFacts) {
			p.Review[1].Exclude.Dependencies = []string{"example"}
		}, SelectionCandidate, []string{"common"}},
		{"empty references are unrestricted", func(p *Policy, _ *CandidateFacts) {
			p.Review[1].Match.FilterIDs = []string{}
		}, SelectionCandidate, []string{"common", "scoped"}},
		{"disabled reference skips its review inputs", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[1].Enabled = false
			p.Review[1].Match.Files = []string{"**"}
			p.Review[1].Match.Types = []string{"patch"}
			f.PR.UpdatesComplete = false
		}, SelectionCandidate, []string{"common"}},
		{"mismatched reference skips its review inputs", func(p *Policy, _ *CandidateFacts) {
			p.PullRequests.Filters[1].Bases = []string{"develop"}
			p.Review[1].Match.Files = []string{"**"}
		}, SelectionCandidate, []string{"common"}},
		{"disabled review needs no reference data", func(p *Policy, _ *CandidateFacts) {
			p.Review[1].Enabled = false
		}, SelectionCandidate, []string{"common"}},
		{"closed PR has no resolved instructions", func(_ *Policy, f *CandidateFacts) {
			f.PR.State = "closed"
		}, SelectionExcluded, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testPolicy()
			p.PullRequests.Filters = []Filter{{Entry: Entry{ID: "all", Enabled: true}}, {Entry: Entry{ID: "details", Enabled: true}, Files: []string{"go.mod"}}}
			p.Review = []Instruction{{Entry: Entry{ID: "common", Enabled: true}, Instructions: "common"}, {Entry: Entry{ID: "scoped", Enabled: true}, Match: Match{FilterIDs: []string{"details"}}, Instructions: "scoped"}}
			f := CandidateFacts{PR: validPR()}
			tc.edit(&p, &f)
			got := SelectCandidate(p, f)
			if got.Status != tc.want || !reflect.DeepEqual(got.ReviewIDs, tc.ids) || len(got.Review) != len(tc.ids) || got.Review == nil {
				t.Fatal(got)
			}
			if got.Status == SelectionUnknown && !strings.Contains(strings.Join(got.Reasons, " "), "review scoped: details:") {
				t.Fatal("missing review and filter IDs in reason", got)
			}
		})
	}
}

func TestPRListFetchesReferencedFilterDetails(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		files, commits, fails bool
	}{
		{"files", true, false, false},
		{"commits", false, true, false},
		{"both", true, true, false},
		{"read failure", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testPolicy()
			rule := Filter{Entry: Entry{ID: "details", Enabled: true}}
			if tc.files {
				rule.Files = []string{"go.mod"}
			}
			if tc.commits {
				rule.CommitAuthors = []string{"renovate[bot]"}
			}
			p.PullRequests.Filters = []Filter{{Entry: Entry{ID: "all", Enabled: true}}, rule}
			p.Review = []Instruction{{Entry: Entry{ID: "scoped", Enabled: true}, Match: Match{FilterIDs: []string{"details"}}, Instructions: "inspect"}}
			calls := 0
			reader := listReaderStub{
				list: func() ([]PRInfo, error) { return []PRInfo{validPR()}, nil },
				details: func(pr PRInfo, files, commits bool) (CandidateFacts, error) {
					calls++
					if files != tc.files || commits != tc.commits {
						t.Fatal("wrong requested details", files, commits)
					}
					if tc.fails {
						return CandidateFacts{PR: pr}, errors.New("details failed")
					}
					return CandidateFacts{PR: pr, FilesComplete: files, Files: []ChangedFile{{Filename: "go.mod"}}, CommitsComplete: commits, CommitAuthors: []string{"renovate[bot]"}}, nil
				},
			}
			result, err := listCandidates(context.Background(), reader, emptyPRResult(), p, true)
			if calls != 1 || len(result.PullRequests) != 1 || (err != nil) != tc.fails || result.Complete == tc.fails {
				t.Fatal(result, err, calls)
			}
			pr := result.PullRequests[0]
			if tc.fails {
				if pr.Status != SelectionUnknown || len(pr.Review) != 0 || len(result.Errors) == 0 {
					t.Fatal(result)
				}
			} else if pr.Status != SelectionCandidate || !reflect.DeepEqual(pr.Review, []ResolvedInstruction{{"scoped", "inspect"}}) {
				t.Fatal(pr)
			}
		})
	}
}

func TestReviewReferenceConfigValidation(t *testing.T) {
	filters := "pull_requests:\n  filters:\n  - id: all\n"
	for _, tc := range []struct{ name, entry string }{
		{"missing filter", "review:\n- id: r\n  match: {filter_ids: [missing]}\n  instructions: inspect\n"},
		{"null", "review:\n- id: r\n  match: {filter_ids: null}\n  instructions: inspect\n"},
		{"scalar", "review:\n- id: r\n  match: {filter_ids: all}\n  instructions: inspect\n"},
		{"invalid ID", "review:\n- id: r\n  match: {filter_ids: ['bad id']}\n  instructions: inspect\n"},
		{"exclude is not extended", "review:\n- id: r\n  exclude: {filter_ids: [all]}\n  instructions: inspect\n"},
		{"post merge is not extended", "after_merge:\n- id: r\n  match: {filter_ids: [all]}\n  instructions: inspect\n"},
		{"post repo is not extended", "after_repo:\n- id: r\n  match: {filter_ids: [all]}\n  instructions: inspect\n"},
		{"on blocked is not extended", "on_blocked:\n- id: r\n  match: {filter_ids: [all]}\n  instructions: inspect\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, root := range []bool{false, true} {
				common, repo := filters, tc.entry
				if root {
					common, repo = filters+tc.entry, ""
				}
				if _, err := policyFiles(t, common, repo); err == nil || !strings.Contains(err.Error(), "filter_ids") {
					t.Fatalf("root=%v: %v", root, err)
				}
			}
		})
	}
	// Resolve references after inheritance, including repo-defined targets.
	p, err := policyFiles(t, "review:\n- id: r\n  match: {filter_ids: [local]}\n  instructions: common\n", "pull_requests:\n  filters:\n  - id: local\n    enabled: false\n")
	if err != nil || len(SelectCandidate(p, CandidateFacts{PR: validPR()}).Review) != 0 {
		t.Fatal(p, err)
	}
}

func TestPRListCLIEmitsMergedReviewText(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := cliRepo(t, t.TempDir(), "repo", "https://github.com/o/r.git")
	config := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, config, "version: 2\ndefaults:\n  pull_requests:\n    filters:\n    - id: all\n  review:\n  - id: r\n    match: {filter_ids: [all]}\n    instructions: common\n")
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 2\nenabled: true\npull_requests:\n  filters:\n  - id: local\nreview:\n- id: r\n  inherit: merge\n  match: {filter_ids: [all, local]}\n  instructions: repository\n")
	var out, log bytes.Buffer
	app := newApplication(&log)
	app.Reader = func(context.Context, GitHubAPIReadRetry) (PRListReader, error) {
		return listReaderStub{list: func() ([]PRInfo, error) { return []PRInfo{validPR()}, nil }}, nil
	}
	code := newCLI(app, nil).Run(context.Background(), []string{"pr", "list", "--repo", dir, "--config", config}, nil, &out, &log)
	if code != 0 {
		t.Fatal(code, out.String(), log.String())
	}
	assertCLIOutputSchema(t, "pr-list", out.Bytes())
	var result PRListResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.PullRequests) != 1 || !reflect.DeepEqual(result.PullRequests[0].Review, []ResolvedInstruction{{"r", "common\n\nrepository"}}) {
		t.Fatal(result)
	}
	// The output is sufficient without looking instructions up in config show.
	var wire map[string]any
	if err := json.Unmarshal(out.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	pr := wire["pull_requests"].([]any)[0].(map[string]any)
	review := pr["review"].([]any)[0].(map[string]any)
	if len(review) != 2 || review["id"] != "r" || review["instructions"] != "common\n\nrepository" || pr["url"] != validPR().URL {
		t.Fatal(pr)
	}
}
