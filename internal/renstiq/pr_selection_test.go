package renstiq

import (
	"reflect"
	"testing"
)

func TestSelectCandidate(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Policy, *CandidateFacts)
		want string
	}{
		{"defaults", func(*Policy, *CandidateFacts) {}, "candidate"},
		{"draft", func(_ *Policy, f *CandidateFacts) { f.PR.Draft = true }, "candidate"},
		{"title does not classify", func(p *Policy, f *CandidateFacts) {
			p.Rules = []Rule{{ID: "minor", Files: []string{"**"}, Types: []string{"minor"}, Dependencies: []string{"other"}}}
			f.PR.Title = "Update dep to v99 (major)"
		}, "candidate"},
		{"author", func(p *Policy, _ *CandidateFacts) { p.PullRequests.Authors = []string{"app/renovate"} }, "excluded"},
		{"non Renovate cannot opt in", func(p *Policy, f *CandidateFacts) { p.PullRequests.Authors = []string{"human"}; f.PR.Author = "human" }, "excluded"},
		{"base", func(_ *Policy, f *CandidateFacts) { f.PR.Base = "develop" }, "excluded"},
		{"head", func(p *Policy, _ *CandidateFacts) { p.PullRequests.Heads = []string{"renovate/npm/**"} }, "excluded"},
		{"file", func(p *Policy, _ *CandidateFacts) { p.PullRequests.Files = []string{"package.json"} }, "excluded"},
		{"rename old name", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Files = []string{"go.mod"}
			f.Files[0].Previous = "outside"
			f.Files[0].Status = "renamed"
		}, "excluded"},
		{"commit author", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.CommitAuthors = []string{"renovate[bot]"}
			f.CommitAuthors = []string{"human"}
		}, "excluded"},
		{"unknown author", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.CommitAuthors = []string{"unknown"}
			f.CommitAuthors = []string{""}
		}, "unknown"},
		{"partial commits", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.CommitAuthors = []string{"renovate[bot]"}
			f.CommitsComplete = false
			f.CommitAuthors = []string{"human"}
		}, "unknown"},
		{"partial files", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Files = []string{"package.json"}
			f.FilesComplete = false
		}, "unknown"},
		{"retrieval failure", func(_ *Policy, f *CandidateFacts) { f.Problems = []string{"HTTP 500"} }, "unknown"},
		{"changed revision", func(_ *Policy, f *CandidateFacts) { f.Changed = true }, "unknown"},
		{"changed closed", func(_ *Policy, f *CandidateFacts) { f.Changed = true; f.PR.State = "closed" }, "unknown"},
		{"missing sha", func(_ *Policy, f *CandidateFacts) { f.PR.HeadSHA = "" }, "unknown"},
		{"obvious exclusion needs no details", func(p *Policy, f *CandidateFacts) {
			p.PullRequests.Files = []string{"**"}
			p.PullRequests.CommitAuthors = []string{"renovate[bot]"}
			f.PR.Base = "develop"
			f.FilesComplete = false
			f.CommitsComplete = false
		}, "excluded"},
		{"empty authors denies all", func(p *Policy, _ *CandidateFacts) { p.PullRequests.Authors = []string{} }, "excluded"},
		{"empty bases denies all", func(p *Policy, _ *CandidateFacts) { p.PullRequests.Bases = []string{} }, "excluded"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := defaultPolicy()
			f := CandidateFacts{PR: validPR(), Files: []ChangedFile{{Filename: "go.mod", Status: "modified"}}, FilesComplete: true, CommitsComplete: true, CommitAuthors: []string{"renovate[bot]"}}
			tc.edit(&p, &f)
			beforeP, beforeF := asMap(p), asMap(f)
			s := SelectCandidate(p, f)
			if string(s.Status) != tc.want {
				t.Fatalf("got %+v want %s", s, tc.want)
			}
			if !reflect.DeepEqual(beforeP, asMap(p)) || !reflect.DeepEqual(beforeF, asMap(f)) {
				t.Fatal("selection mutated inputs")
			}
			if s.Status != "candidate" && len(s.Reasons) == 0 {
				t.Fatal("missing reason")
			}
			if s.Status == "candidate" && len(s.ReviewRequired) == 0 {
				t.Fatal("candidate must still require review")
			}
		})
	}
}
func TestGroupPRAndRenameCoveredByMultipleRules(t *testing.T) {
	p := defaultPolicy()
	p.Rules = []Rule{{ID: "go", Files: []string{"go.*"}, Types: []string{"patch"}}, {ID: "npm", Files: []string{"package*"}, Types: []string{"minor"}}, {ID: "all", Files: []string{"**"}, Types: []string{"major"}}, {ID: "unrelated", Files: []string{"other/**"}, Types: []string{"patch"}}}
	f := CandidateFacts{PR: validPR(), FilesComplete: true, Files: []ChangedFile{{Filename: "go.mod", Status: "modified"}, {Filename: "package.json", Previous: "package-old.json", Status: "renamed"}}}
	s := SelectCandidate(p, f)
	if s.Status != "candidate" || !reflect.DeepEqual(s.CandidateRuleIDs, []string{"go", "npm", "all"}) {
		t.Fatal(s)
	}
	p.Rules = p.Rules[:2]
	f.Files[1].Previous = "uncovered.json"
	if s := SelectCandidate(p, f); s.Status != "excluded" {
		t.Fatal(s)
	}
}
