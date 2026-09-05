package renstiq

import (
	"path/filepath"
	"testing"
)

func TestRepositoryPolicyProfiles(t *testing.T) {
	checkProfiles := []struct {
		name   string
		config string
		checks []Check
	}{
		{"no_checks", "checks:\n  minimum: 0\n", nil},
		{"minimum_checks", "checks:\n  minimum: 1\n", []Check{{Name: "unit", Status: "completed", Conclusion: "success"}}},
		{"named_checks", "checks:\n  required:\n    - workflow: ci\n      name: unit\n", []Check{{Name: "unit", Workflow: "ci", Status: "completed", Conclusion: "success"}}},
	}
	postProfiles := []struct {
		name   string
		config string
		needed bool
		want   int
	}{
		{"none", "post_merge: []\n", false, 0},
		{"each", "post_merge:\n  - id: refresh\n    timing: after_each_merge\n    match:\n      changed_files_any: [go.mod]\n    command: [./scripts/refresh]\n", false, 1},
		{"batch_needed", "post_merge:\n  - id: release\n    timing: after_repo\n    requires_review: true\n    command: [./scripts/release]\n", true, 1},
		{"batch_unneeded", "post_merge:\n  - id: release\n    timing: after_repo\n    requires_review: true\n    command: [./scripts/release]\n", false, 0},
	}
	for _, checks := range checkProfiles {
		for _, post := range postProfiles {
			t.Run(checks.name+"/"+post.name, func(t *testing.T) {
				dir := t.TempDir()
				writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: true\nrules:\n  - id: modules\n    files: [go.mod]\n    dependencies: [example.com/library]\n    update_types: [patch, minor]\n"+checks.config+post.config)
				p, hash, err := LoadPolicy(dir, DefaultConfig())
				if err != nil {
					t.Fatal(err)
				}
				pr := validPR()
				pr.Checks = checks.checks
				d := validDecision()
				d.ConfigDigest = hash
				d.Updates[0].Dependency = "example.com/library"
				for _, action := range p.PostMerge {
					if action.RequiresReview {
						d.PostMerge = append(d.PostMerge, PostChoice{ID: action.ID, Needed: post.needed, Reason: "synthetic assessment"})
					}
				}
				if err := d.Validate(d.Repo, hash, p); err != nil {
					t.Fatal(err)
				}
				if reasons := policyReasons(p, pr, d); len(reasons) != 0 {
					t.Fatal(reasons)
				}
				selectedCount := 0
				for _, action := range p.PostMerge {
					if selected(action, MergeRecord{PR: pr.Number, Files: pr.Files, Decision: d}) {
						selectedCount++
					}
				}
				if selectedCount != post.want {
					t.Fatalf("selected %d commands, want %d", selectedCount, post.want)
				}
				d.Updates[0].Type = "major"
				if len(policyReasons(p, pr, d)) == 0 {
					t.Fatal("major update accepted")
				}
				d.Updates[0].Type = "patch"
				pr.Files = append(pr.Files, "source.go")
				if len(policyReasons(p, pr, d)) == 0 {
					t.Fatal("uninvestigated source change accepted")
				}
				pr.Files = []string{"go.mod"}
				pr.Checks = nil
				if checks.name != "no_checks" && len(policyReasons(p, pr, d)) == 0 {
					t.Fatal("missing checks accepted")
				}
			})
		}
	}
}

func TestOutputSchemas(t *testing.T) {
	for name, v := range map[string]any{"result": Result{Version: 1, Command: "inspect", Results: []RepoResult{{Config: ptr(defaultPolicy()), State: &State{Version: 1, Repo: "o/r"}}}}, "post-input": PostInput{Version: 1, Repo: "o/r", Merges: []MergeRecord{{Decision: validDecision()}}}, "state": State{Version: 1, Repo: "o/r"}} {
		if err := validateSchema(name, v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
func ptr[T any](v T) *T { return &v }
func TestConditionalCheckOverride(t *testing.T) {
	p := defaultPolicy()
	p.Checks.Minimum = 1
	zero := 0
	p.Rules = []Rule{{ID: "no-ci", Files: []string{"go.mod"}, Types: []string{"patch"}, Checks: &ChecksPatch{Minimum: &zero}}}
	if r := policyReasons(p, validPR(), validDecision()); len(r) != 0 {
		t.Fatal(r)
	}
}
