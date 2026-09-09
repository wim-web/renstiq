package renstiq

import (
	"testing"
)

func TestUpdateTypeFilterMatchesAnyUpdate(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Policy, *CandidateFacts)
		want SelectionStatus
	}{
		{"matching update after nonmatch", func(*Policy, *CandidateFacts) {}, SelectionCandidate},
		{"matching update before nonmatch", func(_ *Policy, f *CandidateFacts) {
			f.PR.Updates[0], f.PR.Updates[1] = f.PR.Updates[1], f.PR.Updates[0]
		}, SelectionCandidate},
		{"second allowed type", func(p *Policy, _ *CandidateFacts) {
			p.Rules[0].Types = []string{"digest", "minor"}
		}, SelectionCandidate},
		{"no matching update", func(p *Policy, _ *CandidateFacts) {
			p.Rules[0].Types = []string{"patch"}
		}, SelectionExcluded},
		{"empty allowlist", func(p *Policy, _ *CandidateFacts) {
			p.Rules[0].Types = []string{}
		}, SelectionExcluded},
		{"omitted does not require metadata", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Types = nil
			f.PR.UpdatesComplete = false
			f.PR.Updates = nil
		}, SelectionCandidate},
		{"partial metadata remains unknown", func(_ *Policy, f *CandidateFacts) {
			f.PR.UpdatesComplete = false
		}, SelectionUnknown},
		{"missing updates remains unknown", func(_ *Policy, f *CandidateFacts) {
			f.PR.Updates = nil
		}, SelectionUnknown},
		{"author condition still applies", func(_ *Policy, f *CandidateFacts) {
			f.PR.Author = "human"
		}, SelectionExcluded},
		{"base condition still applies", func(_ *Policy, f *CandidateFacts) {
			f.PR.Base = "develop"
		}, SelectionExcluded},
		{"all dependency names still required", func(p *Policy, _ *CandidateFacts) {
			p.Rules[0].Dependencies = []string{"matching"}
		}, SelectionExcluded},
		{"all file paths still required", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Files = []string{"go.mod"}
			f.FilesComplete = true
			f.Files = []ChangedFile{{Filename: "go.mod"}, {Filename: "package.json"}}
		}, SelectionExcluded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testPolicy()
			p.Rules[0].Types = []string{"minor"}
			f := CandidateFacts{PR: validPR()}
			f.PR.Updates = []DependencyUpdate{{Dependency: "other", Type: "major"}, {Dependency: "matching", Type: "minor"}}
			tc.edit(&p, &f)
			got := SelectCandidate(p, f)
			if got.Status != tc.want || (got.Status == SelectionCandidate && len(got.Reasons) != 0) || (got.Status != SelectionCandidate && len(got.Reasons) == 0) {
				t.Fatalf("got %+v, want %s", got, tc.want)
			}
		})
	}
}
