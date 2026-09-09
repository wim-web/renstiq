package renstiq

import (
	"reflect"
	"strings"
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
func TestFilterEntriesUseOR(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Policy, *CandidateFacts)
		want SelectionStatus
	}{
		{"first entry matches", func(*Policy, *CandidateFacts) {}, SelectionCandidate},
		{"second entry matches", func(_ *Policy, f *CandidateFacts) { f.PR.Updates[0].Type = "minor" }, SelectionCandidate},
		{"neither matches", func(_ *Policy, f *CandidateFacts) { f.PR.Updates[0].Type = "major" }, SelectionExcluded},
		{"entries cannot each allow part of a group", func(_ *Policy, f *CandidateFacts) {
			f.PR.Updates = append(f.PR.Updates, DependencyUpdate{Dependency: "other", Type: "minor"})
		}, SelectionExcluded},
		{"disabled match does not pass", func(p *Policy, _ *CandidateFacts) { p.PullRequests.Filters[0].Enabled = false }, SelectionExcluded},
		{"all disabled imposes no restriction", func(p *Policy, _ *CandidateFacts) {
			p.PullRequests.Filters[0].Enabled = false
			p.PullRequests.Filters[1].Enabled = false
		}, SelectionCandidate},
		{"no entries imposes no restriction", func(p *Policy, _ *CandidateFacts) { p.PullRequests.Filters = nil }, SelectionCandidate},
		{"conditions in one entry are ANDed", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Bases = []string{"develop"}
			p.PullRequests.Filters[1].Bases = []string{"main"}
		}, SelectionExcluded},
		{"true wins over unknown metadata", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Types = nil
			p.PullRequests.Filters[0].Files = []string{"go.mod"}
			f.PR.UpdatesComplete = false
			f.PR.Updates = nil
			f.PR.MetadataError = "Renovate Package table has no Update column"
		}, SelectionCandidate},
		{"true wins over incomplete files", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[1].Types = nil
			p.PullRequests.Filters[1].Files = []string{"go.mod"}
			f.FilesComplete = false
		}, SelectionCandidate},
		{"true wins over incomplete commits", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[1].Types = nil
			p.PullRequests.Filters[1].CommitAuthors = []string{"renovate[bot]"}
			f.CommitsComplete = false
		}, SelectionCandidate},
		{"false and unknown remains unknown", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Types = []string{"major"}
			p.PullRequests.Filters[1].Types = nil
			p.PullRequests.Filters[1].Files = []string{"go.mod"}
			f.FilesComplete = false
		}, SelectionUnknown},
		{"all unknown remains unknown", func(_ *Policy, f *CandidateFacts) { f.PR.UpdatesComplete = false }, SelectionUnknown},
		{"known file mismatch rules out unknown metadata", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Files = []string{"aqua.yaml"}
			p.PullRequests.Filters[1].Bases = []string{"develop"}
			f.PR.UpdatesComplete = false
		}, SelectionExcluded},
		{"empty allowlist rules out unknown metadata", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Authors = []string{}
			p.PullRequests.Filters[1].Files = []string{}
			f.PR.UpdatesComplete = false
		}, SelectionExcluded},
		{"matching filter cannot bypass lock", func(p *Policy, f *CandidateFacts) { f.PR.Labels = []string{p.PullRequests.LockLabel} }, SelectionExcluded},
		{"matching filter cannot expand Renovate population", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Authors = []string{"human"}
			f.PR.Author = "human"
		}, SelectionExcluded},
		{"matching filter cannot admit closed PR", func(_ *Policy, f *CandidateFacts) { f.PR.State = "closed" }, SelectionExcluded},
		{"matching filter cannot bypass changed PR", func(_ *Policy, f *CandidateFacts) { f.Changed = true }, SelectionUnknown},
		{"matching filter cannot bypass identity", func(_ *Policy, f *CandidateFacts) { f.PR.HeadSHA = "" }, SelectionUnknown},
		{"matching filter cannot bypass label integrity", func(_ *Policy, f *CandidateFacts) { f.PR.LabelsKnown = false }, SelectionUnknown},
		{"matching filter cannot bypass snapshot failure", func(_ *Policy, f *CandidateFacts) { f.Problems = []string{"snapshot failed"} }, SelectionUnknown},
		{"review metadata is still required", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Types = nil
			f.PR.UpdatesComplete = false
			p.Review = []Instruction{{Entry: Entry{ID: "dependency", Enabled: true}, Match: Match{Dependencies: []string{"example"}}, Instructions: "review"}}
		}, SelectionUnknown},
		{"review files are still required", func(p *Policy, f *CandidateFacts) {
			f.FilesComplete = false
			p.Review = []Instruction{{Entry: Entry{ID: "files", Enabled: true}, Match: Match{Files: []string{"go.mod"}}, Instructions: "review"}}
		}, SelectionUnknown},
		{"excluded PR does not need review inputs", func(p *Policy, f *CandidateFacts) {
			f.PR.Updates[0].Type = "major"
			f.FilesComplete = false
			p.Review = []Instruction{{Entry: Entry{ID: "files", Enabled: true}, Match: Match{Files: []string{"go.mod"}}, Instructions: "review"}}
		}, SelectionExcluded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testPolicy()
			p.PullRequests.Filters = []Filter{
				{Entry: Entry{ID: "patch", Enabled: true}, Types: []string{"patch"}},
				{Entry: Entry{ID: "minor", Enabled: true}, Types: []string{"minor"}},
			}
			f := CandidateFacts{PR: validPR(), Files: []ChangedFile{{Filename: "go.mod"}}, FilesComplete: true, CommitsComplete: true, CommitAuthors: []string{"renovate[bot]"}}
			tc.edit(&p, &f)
			for order := 0; order < 2; order++ {
				got := SelectCandidate(p, f)
				if got.Status != tc.want {
					t.Fatalf("order %d: got %+v want %s", order, got, tc.want)
				}
				if got.Status == SelectionCandidate && len(got.Reasons) != 0 {
					t.Fatal("a matching alternative retained rejected/unknown reasons", got)
				}
				if got.Status != SelectionCandidate && len(got.Reasons) == 0 {
					t.Fatal("missing reason", got)
				}
				if len(p.PullRequests.Filters) == 2 {
					p.PullRequests.Filters[0], p.PullRequests.Filters[1] = p.PullRequests.Filters[1], p.PullRequests.Filters[0]
				}
			}
		})
	}
}

func TestFilterFailureReasonsIdentifyAlternatives(t *testing.T) {
	p := testPolicy()
	p.PullRequests.Filters = []Filter{
		{Entry: Entry{ID: "updates", Enabled: true}, Types: []string{"patch"}},
		{Entry: Entry{ID: "go", Enabled: true}, Files: []string{"go.mod"}},
	}
	f := CandidateFacts{PR: validPR(), FilesComplete: true, Files: []ChangedFile{{Filename: "aqua.yaml"}}}
	f.PR.UpdatesComplete = false
	f.PR.MetadataError = "Renovate Package table has no Update column"
	got := SelectCandidate(p, f)
	if got.Status != SelectionUnknown || len(got.Reasons) != 2 || !strings.HasPrefix(got.Reasons[0], "updates: ") || !strings.HasPrefix(got.Reasons[1], "go: ") {
		t.Fatal(got)
	}
}
