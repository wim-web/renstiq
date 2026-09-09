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
		{"literal not glob", func(p *Policy, _ *CandidateFacts) { p.Rules[0].Labels = []string{"depend*"} }, SelectionExcluded},
		{"omitted condition", func(p *Policy, f *CandidateFacts) { p.Rules[0].Labels = nil; f.PR.LabelsKnown = false }, SelectionCandidate},
		{"empty condition", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Labels = []string{}
			f.PR.LabelsKnown = false
		}, SelectionExcluded},
		{"missing labels", func(_ *Policy, f *CandidateFacts) { f.PR.LabelsKnown = false }, SelectionUnknown},
		{"other conditions are AND", func(_ *Policy, f *CandidateFacts) { f.PR.Base = "develop" }, SelectionExcluded},
		{"mismatch rules out missing labels", func(_ *Policy, f *CandidateFacts) { f.PR.Base = "develop"; f.PR.LabelsKnown = false }, SelectionExcluded},
		{"disabled filter", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Enabled = false
			f.PR.LabelsKnown = false
		}, SelectionExcluded},
		{"another filter may match", func(p *Policy, f *CandidateFacts) {
			p.Rules = append(p.Rules, Rule{Entry: Entry{ID: "all", Enabled: true}})
			f.PR.LabelsKnown = false
		}, SelectionUnknown},
		{"former lock label does not exclude matching labels", func(_ *Policy, f *CandidateFacts) {
			f.PR.Labels = append(f.PR.Labels, "renstiq-locked")
		}, SelectionCandidate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testPolicy()
			p.Rules[0].Labels = []string{"dependencies", "security"}
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

func TestLabelFilterCLISelectsReviewsFromGitHubLabels(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := cliRepo(t, t.TempDir(), "repo", "https://github.com/o/r.git")
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 2\nenabled: true\nrules:\n- id: label-review\n  labels: [dependencies, security]\n  instructions: inspect labeled PR\n")
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
