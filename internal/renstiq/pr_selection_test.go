package renstiq

import (
	"reflect"
	"testing"
)

func TestSelectCandidateV2(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Policy, *CandidateFacts)
		want SelectionStatus
	}{
		{"defaults", func(*Policy, *CandidateFacts) {}, SelectionCandidate},
		{"draft still reviewed", func(_ *Policy, f *CandidateFacts) { f.PR.Draft = true }, SelectionCandidate},
		{"renovate population cannot expand", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Authors = []string{"human"}
			f.PR.Author = "human"
		}, SelectionExcluded},
		{"base branch", func(_ *Policy, f *CandidateFacts) { f.PR.Base = "develop" }, SelectionExcluded},
		{"empty allowlist", func(p *Policy, _ *CandidateFacts) { p.PullRequests.Filters[0].Authors = []string{} }, SelectionExcluded},
		{"disabled filter", func(p *Policy, f *CandidateFacts) { p.PullRequests.Filters[0].Enabled = false; f.PR.Base = "develop" }, SelectionCandidate},
		{"locked", func(p *Policy, f *CandidateFacts) { f.PR.Labels = []string{p.PullRequests.LockLabel} }, SelectionExcluded},
		{"lock persists after update", func(p *Policy, f *CandidateFacts) {
			f.PR.Labels = []string{p.PullRequests.LockLabel}
			f.PR.HeadSHA = "updated"
		}, SelectionExcluded},
		{"missing labels", func(_ *Policy, f *CandidateFacts) { f.PR.LabelsKnown = false }, SelectionUnknown},
		{"partial files", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Files = []string{"**"}
			f.FilesComplete = false
		}, SelectionUnknown},
		{"unallowed file", func(p *Policy, _ *CandidateFacts) { p.PullRequests.Filters[0].Files = []string{"package.json"} }, SelectionExcluded},
		{"rename all paths", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Files = []string{"go.mod"}
			f.Files[0].Previous = "other"
		}, SelectionExcluded},
		{"commit author", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].CommitAuthors = []string{"renovate[bot]"}
			f.CommitAuthors = []string{"human"}
		}, SelectionExcluded},
		{"partial commits not excluded", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].CommitAuthors = []string{"renovate[bot]"}
			f.CommitsComplete = false
			f.CommitAuthors = []string{"human"}
		}, SelectionUnknown},
		{"unknown commit author", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].CommitAuthors = []string{"renovate[bot]"}
			f.CommitAuthors = []string{""}
		}, SelectionUnknown},
		{"major excluded by list", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Types = []string{"patch", "minor"}
			f.PR.Updates[0].Type = "major"
		}, SelectionExcluded},
		{"all group updates must pass", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Types = []string{"patch", "minor"}
			f.PR.Updates = append(f.PR.Updates, DependencyUpdate{"other", "major"})
		}, SelectionExcluded},
		{"dependency selection is mechanical", func(p *Policy, _ *CandidateFacts) { p.PullRequests.Filters[0].Dependencies = []string{"other"} }, SelectionExcluded},
		{"no classification from title", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Types = []string{"patch"}
			f.PR.Title = "Update to v1.0.1 (patch)"
			f.PR.UpdatesComplete = false
		}, SelectionUnknown},
		{"changed PR", func(_ *Policy, f *CandidateFacts) { f.Changed = true }, SelectionUnknown},
		{"missing identity", func(_ *Policy, f *CandidateFacts) { f.PR.HeadSHA = "" }, SelectionUnknown},
		{"read failure", func(_ *Policy, f *CandidateFacts) { f.Problems = []string{"failed"} }, SelectionUnknown},
		{"empty file allowlist", func(p *Policy, _ *CandidateFacts) { p.PullRequests.Filters[0].Files = []string{} }, SelectionExcluded},
		{"empty types allowlist", func(p *Policy, _ *CandidateFacts) { p.PullRequests.Filters[0].Types = []string{} }, SelectionExcluded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testPolicy()
			f := CandidateFacts{PR: validPR(), Files: []ChangedFile{{Filename: "go.mod", Status: "modified"}}, FilesComplete: true, CommitsComplete: true, CommitAuthors: []string{"renovate[bot]"}}
			tc.edit(&p, &f)
			got := SelectCandidate(p, f)
			if got.Status != tc.want {
				t.Fatalf("got %+v want %s", got, tc.want)
			}
			if got.Status != SelectionCandidate && len(got.Reasons) == 0 {
				t.Fatal("missing reason")
			}
		})
	}
}
func TestAllMatchingReviewInstructionsApply(t *testing.T) {
	p := testPolicy()
	p.Review = []Instruction{
		{Entry: Entry{ID: "common", Enabled: true}, Instructions: "common"},
		{Entry: Entry{ID: "file", Enabled: true}, Match: Match{Files: []string{"go.*"}}, Instructions: "file"},
		{Entry: Entry{ID: "dependency", Enabled: true}, Match: Match{Dependencies: []string{"example"}}, Instructions: "dependency"},
		{Entry: Entry{ID: "disabled", Enabled: false}, Instructions: "disabled"},
		{Entry: Entry{ID: "other", Enabled: true}, Match: Match{Dependencies: []string{"other"}}, Instructions: "other"},
	}
	f := CandidateFacts{PR: validPR(), Files: []ChangedFile{{Filename: "go.mod"}}, FilesComplete: true}
	result := SelectCandidate(p, f)
	if result.Status != SelectionCandidate || !reflect.DeepEqual(result.ReviewIDs, []string{"common", "file", "dependency"}) {
		t.Fatal(result)
	}
	// Adding review rules must not change which file paths are allowed by filters.
	f.Files = append(f.Files, ChangedFile{Filename: "src/main.go"})
	if result := SelectCandidate(p, f); result.Status != SelectionCandidate {
		t.Fatal(result)
	}
}
func TestEnabledFiltersAreAllRequired(t *testing.T) {
	p := testPolicy()
	p.PullRequests.Filters = append(p.PullRequests.Filters, Filter{Entry: Entry{ID: "patch", Enabled: true}, Types: []string{"patch"}}, Filter{Entry: Entry{ID: "minor", Enabled: true}, Types: []string{"minor"}})
	if result := SelectCandidate(p, CandidateFacts{PR: validPR()}); result.Status != SelectionExcluded {
		t.Fatal(result)
	}
}
