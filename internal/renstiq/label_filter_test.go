package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLabelFilterSelection(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Policy, *CandidateFacts)
		want SelectionStatus
	}{
		{"one allowed label", func(*Policy, *CandidateFacts) {}, SelectionCandidate},
		{"second allowed label", func(_ *Policy, f *CandidateFacts) { f.PR.Labels = []string{"security"} }, SelectionCandidate},
		{"unrelated labels also allowed", func(_ *Policy, f *CandidateFacts) { f.PR.Labels = []string{"triaged", "security"} }, SelectionCandidate},
		{"no matching label", func(_ *Policy, f *CandidateFacts) { f.PR.Labels = []string{"triaged"} }, SelectionExcluded},
		{"no labels", func(_ *Policy, f *CandidateFacts) { f.PR.Labels = []string{} }, SelectionExcluded},
		{"case sensitive", func(_ *Policy, f *CandidateFacts) { f.PR.Labels = []string{"Dependencies"} }, SelectionExcluded},
		{"literal not glob", func(p *Policy, _ *CandidateFacts) { p.PullRequests.Filters[0].Labels = []string{"depend*"} }, SelectionExcluded},
		{"omitted condition", func(p *Policy, f *CandidateFacts) { p.PullRequests.Filters[0].Labels = nil; f.PR.LabelsKnown = false }, SelectionCandidate},
		{"empty condition", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Labels = []string{}
			f.PR.LabelsKnown = false
		}, SelectionExcluded},
		{"missing labels", func(_ *Policy, f *CandidateFacts) { f.PR.LabelsKnown = false }, SelectionUnknown},
		{"other conditions are AND", func(_ *Policy, f *CandidateFacts) { f.PR.Base = "develop" }, SelectionExcluded},
		{"mismatch rules out missing labels", func(_ *Policy, f *CandidateFacts) { f.PR.Base = "develop"; f.PR.LabelsKnown = false }, SelectionExcluded},
		{"disabled filter", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Enabled = false
			f.PR.LabelsKnown = false
		}, SelectionCandidate},
		{"another filter may match", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters = append(p.PullRequests.Filters, Filter{Entry: Entry{ID: "all", Enabled: true}})
			f.PR.LabelsKnown = false
		}, SelectionCandidate},
		{"review reference requires labels", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters = append(p.PullRequests.Filters, Filter{Entry: Entry{ID: "all", Enabled: true}})
			p.Review = []Instruction{{Entry: Entry{ID: "label-review", Enabled: true}, Match: Match{FilterIDs: []string{"target"}}, Instructions: "inspect"}}
			f.PR.LabelsKnown = false
		}, SelectionUnknown},
		{"former lock label does not exclude matching labels", func(_ *Policy, f *CandidateFacts) {
			f.PR.Labels = append(f.PR.Labels, "renstiq-locked")
		}, SelectionCandidate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testPolicy()
			p.PullRequests.Filters[0].Labels = []string{"dependencies", "security"}
			f := CandidateFacts{PR: validPR()}
			f.PR.Labels = []string{"dependencies"}
			tc.edit(&p, &f)
			got := SelectCandidate(p, f)
			if got.Status != tc.want || (got.Status != SelectionCandidate && len(got.Reasons) == 0) {
				t.Fatal(got)
			}
			if got.Status == SelectionUnknown && !strings.Contains(strings.Join(got.Reasons, " "), "target: PR label list") {
				t.Fatal("missing filter ID and label diagnostic", got)
			}
		})
	}
}

func TestLabelFilterConfigInheritance(t *testing.T) {
	common := "pull_requests:\n  filters:\n  - id: labeled\n    labels: [dependencies]\n"
	for _, tc := range []struct {
		name, repo string
		labels     []string
		want       SelectionStatus
	}{
		{"inherit", "", []string{"dependencies"}, SelectionExcluded},
		{"merge", "    inherit: merge\n    labels: [security, dependencies]\n", []string{"dependencies", "security"}, SelectionCandidate},
		{"override", "    labels: [security]\n", []string{"security"}, SelectionCandidate},
		{"empty", "    labels: []\n", []string{}, SelectionExcluded},
		{"omitted by override", "    base_branches: [main]\n", nil, SelectionCandidate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := tc.repo
			if repo != "" {
				repo = "pull_requests:\n  filters:\n  - id: labeled\n" + repo
			}
			p, err := policyFiles(t, common, repo)
			if err != nil || !reflect.DeepEqual(p.PullRequests.Filters[0].Labels, tc.labels) {
				t.Fatal(p, err)
			}
			f := CandidateFacts{PR: validPR()}
			f.PR.Labels = []string{"security"}
			if got := SelectCandidate(p, f); got.Status != tc.want {
				t.Fatal(got)
			}
		})
	}
	for _, value := range []string{"null", "dependencies", "[null]", "[123]", "['']", "['  ']"} {
		entry := "pull_requests:\n  filters:\n  - id: labeled\n    labels: " + value + "\n"
		for _, root := range []bool{true, false} {
			common, repo := entry, ""
			if !root {
				common, repo = "", entry
			}
			if _, err := policyFiles(t, common, repo); err == nil || !strings.Contains(err.Error(), "labels") {
				t.Fatalf("root=%v labels=%s: %v", root, value, err)
			}
		}
	}
}

func TestLabelFilterCLISelectsReviewsFromGitHubLabels(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := cliRepo(t, t.TempDir(), "repo", "https://github.com/o/r.git")
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 2\nenabled: true\npull_requests:\n  filters:\n  - id: labeled\n    labels: [dependencies, security]\nreview:\n- id: label-review\n  match: {filter_ids: [labeled]}\n  instructions: inspect labeled PR\n")
	labelSets := [][]string{{"dependencies", "triaged"}, {"security"}, {"triaged"}, {}, nil, {""}, {"dependencies", ""}}
	rows := []map[string]any{}
	for i, labels := range labelSets {
		row := asMap(rawFixture(i + 1))
		if labels == nil {
			delete(row, "labels")
		} else {
			objects := []map[string]string{}
			for _, label := range labels {
				objects = append(objects, map[string]string{"name": label})
			}
			row["labels"] = objects
		}
		rows = append(rows, row)
	}
	calls := 0
	g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/repos/o/r/pulls" {
			t.Error("label filter should not fetch extra details", r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		respond(t, w, rows)
	})
	for _, all := range []bool{false, true} {
		var out, log bytes.Buffer
		app := newApplication(&log)
		app.Reader = func(context.Context, GitHubAPIReadRetry) (PRListReader, error) { return g, nil }
		args := []string{"pr", "list", "--repo", dir}
		if all {
			args = append(args, "--all")
		}
		code := newCLI(app, nil).Run(context.Background(), args, nil, &out, &log)
		if code != 1 {
			t.Fatal("missing or malformed labels must be reported", code, out.String(), log.String())
		}
		assertCLIOutputSchema(t, "pr-list", out.Bytes())
		var result PRListResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		want := []SelectionStatus{SelectionCandidate, SelectionCandidate}
		if all {
			want = append(want, SelectionExcluded, SelectionExcluded, SelectionUnknown, SelectionUnknown, SelectionUnknown)
		}
		if result.Complete || len(result.Errors) != 3 || len(result.PullRequests) != len(want) || result.OpenPRCount == nil || *result.OpenPRCount != len(rows) {
			t.Fatal(result)
		}
		for i, pr := range result.PullRequests {
			if pr.Number != i+1 || pr.Status != want[i] {
				t.Fatal(pr)
			}
			if pr.Status == SelectionCandidate {
				if !reflect.DeepEqual(pr.Review, []ResolvedInstruction{{"label-review", "inspect labeled PR"}}) {
					t.Fatal(pr)
				}
			} else if len(pr.Review) != 0 {
				t.Fatal("noncandidate returned reviews", pr)
			}
		}
	}
	if calls != 2 {
		t.Fatal("unexpected requests", calls)
	}
}
