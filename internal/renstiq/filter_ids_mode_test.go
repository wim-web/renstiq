package renstiq

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func filterModePolicy() Policy {
	p := testPolicy()
	p.PullRequests.Filters[0].ID = "renovate"
	for _, label := range []string{"major", "minor", "patch"} {
		p.PullRequests.Filters = append(p.PullRequests.Filters, Filter{Entry: Entry{ID: label, Enabled: true}, Labels: []string{label}})
	}
	p.Review = []Instruction{{Entry: Entry{ID: "selected", Enabled: true}, Match: Match{FilterIDs: []string{"renovate"}, FilterIDsMode: "exact"}, Instructions: "review"}}
	return p
}

func TestFilterIDsModeSetComparison(t *testing.T) {
	for _, tc := range []struct {
		name, mode  string
		ids, labels []string
		want        bool
	}{
		{"default contains any requested ID", "", []string{"minor", "patch"}, []string{"minor"}, true},
		{"contains accepts extra IDs", "contains", []string{"minor", "patch"}, []string{"major", "patch"}, true},
		{"contains no intersection", "contains", []string{"minor", "patch"}, []string{"major"}, false},
		{"contains empty is unrestricted", "contains", []string{}, []string{"major"}, true},
		{"exact only general filter", "exact", []string{"renovate"}, nil, true},
		{"exact rejects extra ID", "exact", []string{"renovate"}, []string{"major"}, false},
		{"exact rejects missing ID", "exact", []string{"renovate", "minor", "patch"}, []string{"minor"}, false},
		{"exact includes general filter", "exact", []string{"minor", "patch"}, []string{"minor", "patch"}, false},
		{"exact ignores order and duplicates", "exact", []string{"patch", "renovate", "minor", "minor"}, []string{"minor", "patch"}, true},
		{"exact empty rejects matching filters", "exact", []string{}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filterModePolicy()
			p.Review[0].Match.FilterIDs, p.Review[0].Match.FilterIDsMode = tc.ids, tc.mode
			f := CandidateFacts{PR: validPR()}
			f.PR.Labels = tc.labels
			for order := 0; order < 2; order++ {
				got := SelectCandidate(p, f)
				if got.Status != SelectionCandidate || (len(got.Review) == 1) != tc.want {
					t.Fatalf("order=%d got=%+v want review=%v", order, got, tc.want)
				}
				p.PullRequests.Filters[0], p.PullRequests.Filters[3] = p.PullRequests.Filters[3], p.PullRequests.Filters[0]
			}
		})
	}
}

func TestExactFilterIDsOnlyReviewsOtherwiseUnmatchedPRs(t *testing.T) {
	p := filterModePolicy()
	p.Review = []Instruction{
		{Entry: Entry{ID: "major-review", Enabled: true}, Match: Match{FilterIDs: []string{"major"}}, Instructions: "skip"},
		{Entry: Entry{ID: "minor-patch-review", Enabled: true}, Match: Match{FilterIDs: []string{"minor", "patch"}}, Instructions: "check CI"},
		p.Review[0],
	}
	for labels := 0; labels < 8; labels++ {
		f := CandidateFacts{PR: validPR()}
		for bit, label := range []string{"major", "minor", "patch"} {
			if labels&(1<<bit) != 0 {
				f.PR.Labels = append(f.PR.Labels, label)
			}
		}
		want := []string{}
		if labels&1 != 0 {
			want = append(want, "major-review")
		}
		if labels&6 != 0 {
			want = append(want, "minor-patch-review")
		}
		if labels == 0 {
			want = append(want, "selected")
		}
		got := SelectCandidate(p, f)
		if got.Status != SelectionCandidate || !reflect.DeepEqual(got.ReviewIDs, want) {
			t.Fatalf("labels=%v got=%+v want=%v", f.PR.Labels, got, want)
		}
	}
	// New matching filters are excluded from the exact set automatically.
	p.PullRequests.Filters = append(p.PullRequests.Filters, Filter{Entry: Entry{ID: "security", Enabled: true}, Labels: []string{"security"}})
	f := CandidateFacts{PR: validPR()}
	f.PR.Labels = []string{"security"}
	got := SelectCandidate(p, f)
	if got.Status != SelectionCandidate || len(got.Review) != 0 {
		t.Fatal(got)
	}
}

func TestExactFilterIDsUnknownAndDisabledFilters(t *testing.T) {
	for _, tc := range []struct {
		name       string
		edit       func(*Policy, *CandidateFacts)
		status     SelectionStatus
		wantReview bool
	}{
		{"unknown outside expected set", func(_ *Policy, f *CandidateFacts) { f.PR.LabelsKnown = false }, SelectionUnknown, false},
		{"unknown expected ID", func(p *Policy, f *CandidateFacts) {
			p.Review[0].Match.FilterIDs = []string{"renovate", "major"}
			f.PR.LabelsKnown = false
		}, SelectionUnknown, false},
		{"known extra ID defeats unknown", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters = append(p.PullRequests.Filters, Filter{Entry: Entry{ID: "extra", Enabled: true}})
			f.PR.LabelsKnown = false
		}, SelectionCandidate, false},
		{"known missing ID defeats unknown", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Filters[1].Enabled = false
			p.Review[0].Match.FilterIDs = []string{"renovate", "major"}
			f.PR.LabelsKnown = false
		}, SelectionCandidate, false},
		{"disabled extras do not require data", func(p *Policy, f *CandidateFacts) {
			for i := 1; i < len(p.PullRequests.Filters); i++ {
				p.PullRequests.Filters[i].Enabled = false
			}
			f.PR.LabelsKnown = false
		}, SelectionCandidate, true},
		{"empty exact set matches no enabled filters", func(p *Policy, _ *CandidateFacts) {
			p.PullRequests.Filters = nil
			p.Review[0].Match.FilterIDs = nil
		}, SelectionCandidate, true},
		{"other match condition rules out unknown", func(p *Policy, f *CandidateFacts) {
			p.Review[0].Match.Types = []string{"major"}
			f.PR.LabelsKnown = false
		}, SelectionCandidate, false},
		{"exclude rules out unknown", func(p *Policy, f *CandidateFacts) {
			p.Review[0].Exclude.Types = []string{"patch"}
			f.PR.LabelsKnown = false
		}, SelectionCandidate, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filterModePolicy()
			f := CandidateFacts{PR: validPR()}
			tc.edit(&p, &f)
			got := SelectCandidate(p, f)
			if got.Status != tc.status || (len(got.Review) == 1) != tc.wantReview {
				t.Fatal(got)
			}
			if got.Status == SelectionUnknown && !strings.Contains(strings.Join(got.Reasons, " "), "review selected:") {
				t.Fatal(got)
			}
		})
	}
}

func TestExactFilterIDsFetchUnlistedFilterDetails(t *testing.T) {
	for _, kind := range []string{"files", "commits", "failure", "known mismatch"} {
		t.Run(kind, func(t *testing.T) {
			p := filterModePolicy()
			rule := Filter{Entry: Entry{ID: "details", Enabled: true}, Files: []string{"go.mod"}}
			if kind == "commits" {
				rule.Files = nil
				rule.CommitAuthors = []string{"renovate[bot]"}
			}
			p.PullRequests.Filters = append(p.PullRequests.Filters, rule)
			pr := validPR()
			if kind == "known mismatch" {
				pr.Labels = []string{"major"}
			}
			calls := 0
			reader := listReaderStub{
				list: func() ([]PRInfo, error) { return []PRInfo{pr}, nil },
				details: func(pr PRInfo, files, commits bool) (CandidateFacts, error) {
					calls++
					if files != (kind != "commits") || commits != (kind == "commits") {
						t.Fatal(files, commits)
					}
					if kind == "failure" {
						return CandidateFacts{PR: pr}, errors.New("unavailable")
					}
					return CandidateFacts{PR: pr, FilesComplete: files, Files: []ChangedFile{{Filename: "package.json"}}, CommitsComplete: commits, CommitAuthors: []string{"human"}}, nil
				},
			}
			result, err := listCandidates(context.Background(), reader, emptyPRResult(), p, true)
			wantCalls := 1
			if kind == "known mismatch" {
				wantCalls = 0
			}
			if calls != wantCalls || len(result.PullRequests) != 1 {
				t.Fatal(calls, result, err)
			}
			got := result.PullRequests[0]
			if kind == "failure" {
				if err == nil || result.Complete || got.Status != SelectionUnknown || len(got.Review) != 0 {
					t.Fatal(result, err)
				}
			} else if err != nil || !result.Complete || got.Status != SelectionCandidate || (len(got.Review) == 1) != (kind != "known mismatch") {
				t.Fatal(result, err)
			}
		})
	}
}

func TestFilterIDsModeConfig(t *testing.T) {
	for _, value := range []string{"exact", "contains", "unknown", "null", "true", "1"} {
		for _, common := range []bool{false, true} {
			body := "pull_requests:\n  filters:\n  - id: all\nreview:\n- id: r\n  match: {filter_ids: [all], filter_ids_mode: " + value + "}\n  instructions: inspect\n"
			root, repo := "", body
			if common {
				root, repo = body, ""
			}
			p, err := policyFiles(t, root, repo)
			if value == "exact" || value == "contains" {
				if err != nil || p.Review[0].Match.FilterIDsMode != value {
					t.Fatal(p, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "filter_ids_mode") {
				t.Fatal(value, common, err)
			}
		}
	}
	common := "pull_requests:\n  filters:\n  - id: all\nreview:\n- id: r\n  match: {filter_ids: [all], filter_ids_mode: exact}\n  instructions: inspect\n"
	for _, tc := range []struct{ repo, want string }{
		{"review:\n- id: r\n  inherit: merge\n  instructions: more\n", "exact"},
		{"review:\n- id: r\n  inherit: merge\n  match: {filter_ids_mode: contains}\n", "contains"},
		{"review:\n- id: r\n  match: {filter_ids: [all]}\n  instructions: replace\n", ""},
	} {
		p, err := policyFiles(t, common, tc.repo)
		if err != nil || p.Review[0].Match.FilterIDsMode != tc.want {
			t.Fatal(p, err)
		}
	}
	for _, section := range []string{"on_blocked", "after_merge", "after_repo"} {
		for _, field := range []string{"match", "exclude"} {
			body := section + ":\n- id: r\n  " + field + ": {filter_ids_mode: exact}\n  instructions: inspect\n"
			if _, err := policyFiles(t, body, ""); err == nil {
				t.Fatal("mode allowed outside review.match", section, field)
			}
		}
	}
	if _, err := policyFiles(t, "review:\n- id: r\n  exclude: {filter_ids_mode: exact}\n  instructions: inspect\n", ""); err == nil {
		t.Fatal("mode allowed in review.exclude")
	}
}
