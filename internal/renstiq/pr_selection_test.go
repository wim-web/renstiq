package renstiq

import (
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
		{"configured human author is allowed", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Authors = []string{"human"}
			f.PR.Author = "human"
		}, SelectionCandidate},
		{"base branch", func(_ *Policy, f *CandidateFacts) { f.PR.Base = "develop" }, SelectionExcluded},
		{"empty allowlist", func(p *Policy, _ *CandidateFacts) { p.Rules[0].Authors = []string{} }, SelectionExcluded},
		{"disabled filter", func(p *Policy, f *CandidateFacts) { p.Rules[0].Enabled = false; f.PR.Base = "develop" }, SelectionExcluded},
		{"former lock label is an ordinary label", func(_ *Policy, f *CandidateFacts) { f.PR.Labels = []string{"renstiq-locked"} }, SelectionCandidate},
		{"updated PR with former lock label remains a candidate", func(_ *Policy, f *CandidateFacts) {
			f.PR.Labels = []string{"renstiq-locked"}
			f.PR.HeadSHA = "updated"
		}, SelectionCandidate},
		{"missing labels without label conditions", func(_ *Policy, f *CandidateFacts) { f.PR.LabelsKnown = false }, SelectionCandidate},
		{"partial files", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Files = []string{"**"}
			f.FilesComplete = false
		}, SelectionUnknown},
		{"unallowed file", func(p *Policy, _ *CandidateFacts) { p.Rules[0].Files = []string{"package.json"} }, SelectionExcluded},
		{"rename all paths", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Files = []string{"go.mod"}
			f.Files[0].Previous = "other"
		}, SelectionExcluded},
		{"commit author", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].CommitAuthors = []string{"renovate[bot]"}
			f.CommitAuthors = []string{"human"}
		}, SelectionExcluded},
		{"partial commits not excluded", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].CommitAuthors = []string{"renovate[bot]"}
			f.CommitsComplete = false
			f.CommitAuthors = []string{"human"}
		}, SelectionUnknown},
		{"unknown commit author", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].CommitAuthors = []string{"renovate[bot]"}
			f.CommitAuthors = []string{""}
		}, SelectionUnknown},
		{"major excluded by list", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Types = []string{"patch", "minor"}
			f.PR.Updates[0].Type = "major"
		}, SelectionExcluded},
		{"one matching update type admits a mixed group", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Types = []string{"patch", "minor"}
			f.PR.Updates = append(f.PR.Updates, DependencyUpdate{"other", "major"})
		}, SelectionCandidate},
		{"dependency selection is mechanical", func(p *Policy, _ *CandidateFacts) { p.Rules[0].Dependencies = []string{"other"} }, SelectionExcluded},
		{"no classification from title", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Types = []string{"patch"}
			f.PR.Title = "Update to v1.0.1 (patch)"
			f.PR.UpdatesComplete = false
		}, SelectionUnknown},
		{"changed PR", func(_ *Policy, f *CandidateFacts) { f.Changed = true }, SelectionUnknown},
		{"missing identity", func(_ *Policy, f *CandidateFacts) { f.PR.HeadSHA = "" }, SelectionUnknown},
		{"read failure", func(_ *Policy, f *CandidateFacts) { f.Problems = []string{"failed"} }, SelectionUnknown},
		{"empty file allowlist", func(p *Policy, _ *CandidateFacts) { p.Rules[0].Files = []string{} }, SelectionExcluded},
		{"empty types allowlist", func(p *Policy, _ *CandidateFacts) { p.Rules[0].Types = []string{} }, SelectionExcluded},
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
func TestRulesUseSourceOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Policy, *CandidateFacts)
		want SelectionStatus
	}{
		{"first entry matches", func(*Policy, *CandidateFacts) {}, SelectionCandidate},
		{"second entry matches", func(_ *Policy, f *CandidateFacts) { f.PR.Updates[0].Type = "minor" }, SelectionCandidate},
		{"neither matches", func(_ *Policy, f *CandidateFacts) { f.PR.Updates[0].Type = "major" }, SelectionExcluded},
		{"each update type filter can match a mixed group", func(_ *Policy, f *CandidateFacts) {
			f.PR.Updates = append(f.PR.Updates, DependencyUpdate{Dependency: "other", Type: "minor"})
		}, SelectionCandidate},
		{"disabled match does not pass", func(p *Policy, _ *CandidateFacts) { p.Rules[0].Enabled = false }, SelectionExcluded},
		{"all disabled excludes PR", func(p *Policy, _ *CandidateFacts) {
			p.Rules[0].Enabled = false
			p.Rules[1].Enabled = false
		}, SelectionExcluded},
		{"no rules excludes PR", func(p *Policy, _ *CandidateFacts) { p.Rules = nil }, SelectionExcluded},
		{"conditions in one entry are ANDed", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Bases = []string{"develop"}
			p.Rules[1].Bases = []string{"main"}
		}, SelectionExcluded},
		{"true wins over unknown metadata", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Types = nil
			p.Rules[0].Files = []string{"go.mod"}
			f.PR.UpdatesComplete = false
			f.PR.Updates = nil
			f.PR.MetadataError = "Renovate Package table has no Update column"
		}, SelectionCandidate},
		{"true wins over incomplete files", func(p *Policy, f *CandidateFacts) {
			p.Rules[1].Types = nil
			p.Rules[1].Files = []string{"go.mod"}
			f.FilesComplete = false
		}, SelectionCandidate},
		{"true wins over incomplete commits", func(p *Policy, f *CandidateFacts) {
			p.Rules[1].Types = nil
			p.Rules[1].CommitAuthors = []string{"renovate[bot]"}
			f.CommitsComplete = false
		}, SelectionCandidate},
		{"false and unknown remains unknown", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Types = []string{"major"}
			p.Rules[1].Types = nil
			p.Rules[1].Files = []string{"go.mod"}
			f.FilesComplete = false
		}, SelectionUnknown},
		{"all unknown remains unknown", func(_ *Policy, f *CandidateFacts) { f.PR.UpdatesComplete = false }, SelectionUnknown},
		{"known file mismatch rules out unknown metadata", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Files = []string{"aqua.yaml"}
			p.Rules[1].Bases = []string{"develop"}
			f.PR.UpdatesComplete = false
		}, SelectionExcluded},
		{"empty allowlist rules out unknown metadata", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Authors = []string{}
			p.Rules[1].Files = []string{}
			f.PR.UpdatesComplete = false
		}, SelectionExcluded},
		{"matching filter can allow another author", func(p *Policy, f *CandidateFacts) {
			p.Rules[0].Authors = []string{"human"}
			f.PR.Author = "human"
		}, SelectionCandidate},
		{"matching filter cannot admit closed PR", func(_ *Policy, f *CandidateFacts) { f.PR.State = "closed" }, SelectionExcluded},
		{"matching filter cannot bypass changed PR", func(_ *Policy, f *CandidateFacts) { f.Changed = true }, SelectionUnknown},
		{"matching filter cannot bypass identity", func(_ *Policy, f *CandidateFacts) { f.PR.HeadSHA = "" }, SelectionUnknown},
		{"matching filter without label conditions does not require labels", func(_ *Policy, f *CandidateFacts) { f.PR.LabelsKnown = false }, SelectionCandidate},
		{"matching filter cannot bypass snapshot failure", func(_ *Policy, f *CandidateFacts) { f.Problems = []string{"snapshot failed"} }, SelectionUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testPolicy()
			p.Rules = []Rule{
				{Entry: Entry{ID: "patch", Enabled: true}, Types: []string{"patch"}},
				{Entry: Entry{ID: "minor", Enabled: true}, Types: []string{"minor"}},
			}
			f := CandidateFacts{PR: validPR(), Files: []ChangedFile{{Filename: "go.mod"}}, FilesComplete: true, CommitsComplete: true, CommitAuthors: []string{"renovate[bot]"}}
			tc.edit(&p, &f)
			{
				got := SelectCandidate(p, f)
				if got.Status != tc.want {
					t.Fatalf("got %+v want %s", got, tc.want)
				}
				if got.Status == SelectionCandidate && len(got.Reasons) != 0 {
					t.Fatal("a matching alternative retained rejected/unknown reasons", got)
				}
				if got.Status != SelectionCandidate && len(got.Reasons) == 0 {
					t.Fatal("missing reason", got)
				}
			}
		})
	}
}

func TestFirstUnknownRuleReportsOnlyItsReasons(t *testing.T) {
	p := testPolicy()
	p.Rules = []Rule{
		{Entry: Entry{ID: "updates", Enabled: true}, Types: []string{"patch"}},
		{Entry: Entry{ID: "go", Enabled: true}, Files: []string{"go.mod"}},
	}
	f := CandidateFacts{PR: validPR(), FilesComplete: true, Files: []ChangedFile{{Filename: "aqua.yaml"}}}
	f.PR.UpdatesComplete = false
	f.PR.MetadataError = "Renovate Package table has no Update column"
	got := SelectCandidate(p, f)
	if got.Status != SelectionUnknown || len(got.Reasons) != 1 || !strings.HasPrefix(got.Reasons[0], "updates: ") {
		t.Fatal(got)
	}
}
