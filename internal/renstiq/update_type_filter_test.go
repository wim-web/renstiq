package renstiq

import (
	"context"
	"reflect"
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
			p.PullRequests.Filters[0].Types = []string{"digest", "minor"}
		}, SelectionCandidate},
		{"no matching update", func(p *Policy, _ *CandidateFacts) {
			p.PullRequests.Filters[0].Types = []string{"patch"}
		}, SelectionExcluded},
		{"empty allowlist", func(p *Policy, _ *CandidateFacts) {
			p.PullRequests.Filters[0].Types = []string{}
		}, SelectionExcluded},
		{"omitted does not require metadata", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Types = nil
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
			p.PullRequests.Filters[0].Dependencies = []string{"matching"}
		}, SelectionExcluded},
		{"all file paths still required", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[0].Files = []string{"go.mod"}
			f.FilesComplete = true
			f.Files = []ChangedFile{{Filename: "go.mod"}, {Filename: "package.json"}}
		}, SelectionExcluded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testPolicy()
			p.PullRequests.Filters[0].Types = []string{"minor"}
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

func TestUpdateTypeFiltersResolveReviewsForMixedPRs(t *testing.T) {
	for _, catchAll := range []bool{false, true} {
		p := testPolicy()
		p.PullRequests.Filters = nil
		if catchAll {
			p.PullRequests.Filters = append(p.PullRequests.Filters, Filter{Entry: Entry{ID: "renovate", Enabled: true}})
		}
		for _, typ := range []string{"major", "minor", "patch"} {
			p.PullRequests.Filters = append(p.PullRequests.Filters, Filter{Entry: Entry{ID: typ, Enabled: true}, Authors: []string{"renovate[bot]"}, Bases: []string{"main"}, Types: []string{typ}})
		}
		p.Review = []Instruction{
			{Entry: Entry{ID: "major-review", Enabled: true}, Match: Match{FilterIDs: []string{"major"}}, Instructions: "Skip major updates"},
			{Entry: Entry{ID: "minor-patch-review", Enabled: true}, Match: Match{FilterIDs: []string{"minor", "patch"}}, Instructions: "Check CI and human changes"},
		}
		for _, tc := range []struct {
			types []string
			ids   []string
		}{
			{[]string{"minor", "patch"}, []string{"minor-patch-review"}},
			{[]string{"patch", "minor"}, []string{"minor-patch-review"}},
			{[]string{"major", "patch"}, []string{"major-review", "minor-patch-review"}},
			{[]string{"major", "minor", "patch"}, []string{"major-review", "minor-patch-review"}},
		} {
			pr := validPR()
			pr.Updates = nil
			for _, typ := range tc.types {
				pr.Updates = append(pr.Updates, DependencyUpdate{Dependency: "example-" + typ, Type: typ})
			}
			reader := listReaderStub{list: func() ([]PRInfo, error) { return []PRInfo{pr}, nil }}
			result, err := listCandidates(context.Background(), reader, emptyPRResult(), p, false)
			if err != nil || !result.Complete || len(result.PullRequests) != 1 {
				t.Fatalf("catchAll=%v types=%v result=%+v err=%v", catchAll, tc.types, result, err)
			}
			got := result.PullRequests[0]
			if !reflect.DeepEqual(got.ReviewIDs, tc.ids) || len(got.Review) != len(tc.ids) {
				t.Fatalf("catchAll=%v types=%v got=%+v want reviews=%v", catchAll, tc.types, got, tc.ids)
			}
			for i, review := range got.Review {
				if review.ID != tc.ids[i] || review.Instructions == "" {
					t.Fatal("missing resolved review", got)
				}
			}
		}
	}
}
