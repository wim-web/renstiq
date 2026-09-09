package renstiq

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func policyFiles(t *testing.T, common, repo string) (Policy, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "common.yaml")
	writeFile(t, path, "version: 2\ndefaults:\n"+indent(common, 2))
	c, err := LoadConfig(path)
	if err != nil {
		return Policy{}, err
	}
	before, _ := json.Marshal(c.Defaults)
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 2\nenabled: true\n"+repo)
	p, _, err := LoadPolicy(dir, c)
	// Resolving repeatedly and resolving a second repo must not mutate defaults.
	again, _, secondErr := LoadPolicy(dir, c)
	after, _ := json.Marshal(c.Defaults)
	if string(before) != string(after) || !reflect.DeepEqual(p, again) || (err == nil) != (secondErr == nil) {
		t.Fatal("resolution mutated or duplicated inherited state")
	}
	return p, err
}
func indent(s string, n int) string {
	if s == "" {
		return strings.Repeat(" ", n) + "{}\n"
	}
	return strings.Repeat(" ", n) + strings.ReplaceAll(strings.TrimSuffix(s, "\n"), "\n", "\n"+strings.Repeat(" ", n)) + "\n"
}
func instructionGroup(p Policy, section string) []Instruction {
	switch section {
	case "review":
		return p.Review
	case "on_blocked":
		return p.OnBlocked
	case "after_merge":
		return p.AfterMerge
	default:
		return p.AfterRepo
	}
}
func TestNamedInstructionsInheritanceAllPhases(t *testing.T) {
	for _, section := range []string{"review", "on_blocked", "after_merge", "after_repo"} {
		for _, mode := range []string{"", "override", "merge", "disabled"} {
			t.Run(section+"/"+mode, func(t *testing.T) {
				common := section + ":\n- id: a\n  match:\n    dependencies: [one]\n    update_types: [patch]\n  instructions: common\n- id: b\n  instructions: second\n"
				repo := section + ":\n- id: a\n"
				switch mode {
				case "disabled":
					repo += "  enabled: false\n"
				default:
					if mode != "" {
						repo += "  inherit: " + mode + "\n"
					}
					repo += "  match:\n    update_types: [minor, patch]\n  instructions: repo\n"
				}
				repo += "- id: c\n  instructions: third\n"
				p, err := policyFiles(t, common, repo)
				if err != nil {
					t.Fatal(err)
				}
				group := instructionGroup(p, section)
				if len(group) != 3 || group[0].ID != "a" || group[1].ID != "b" || group[2].ID != "c" {
					t.Fatalf("lost ID order: %+v", group)
				}
				a := group[0]
				switch mode {
				case "disabled":
					if a.Enabled {
						t.Fatal(a)
					}
				case "merge":
					if a.Instructions != "common\n\nrepo" || !reflect.DeepEqual(a.Match.Types, []string{"patch", "minor"}) || !reflect.DeepEqual(a.Match.Dependencies, []string{"one"}) {
						t.Fatal(a)
					}
				default:
					if a.Instructions != "repo" || len(a.Match.Dependencies) != 0 || !reflect.DeepEqual(a.Match.Types, []string{"minor", "patch"}) {
						t.Fatal("override leaked previous fields", a)
					}
				}
			})
		}
	}
}
func TestFilterIDMergeOverrideDisable(t *testing.T) {
	common := "pull_requests:\n  filters:\n  - id: updates\n    files: [go.mod]\n    update_types: [patch]\n"
	for _, tc := range []struct {
		mode         string
		enabled      bool
		types, files []string
	}{
		{"merge", true, []string{"patch", "minor"}, []string{"go.mod"}},
		{"override", true, []string{"minor", "patch"}, nil},
		{"disabled", false, []string{"patch"}, []string{"go.mod"}},
	} {
		repo := "pull_requests:\n  filters:\n  - id: updates\n"
		if tc.mode == "disabled" {
			repo += "    enabled: false\n"
		} else {
			repo += "    inherit: " + tc.mode + "\n    update_types: [minor, patch]\n"
		}
		p, err := policyFiles(t, common, repo)
		if err != nil {
			t.Fatal(err)
		}
		f := p.PullRequests.Filters[0]
		if f.Enabled != tc.enabled || !reflect.DeepEqual(f.Types, tc.types) || !reflect.DeepEqual(f.Files, tc.files) {
			t.Fatalf("%s: %+v", tc.mode, f)
		}
	}
}
func TestEmptyNamedListsInheritAndScopesAreIndependent(t *testing.T) {
	p, err := policyFiles(t, "review:\n- id: a\n  instructions: review\nafter_repo:\n- id: a\n  instructions: task\n", "review: []\nafter_repo:\n- id: a\n  enabled: false\n")
	if err != nil || len(p.Review) != 1 || !p.Review[0].Enabled || p.AfterRepo[0].Enabled {
		t.Fatal(p, err)
	}
}
func TestV2ConfigValidation(t *testing.T) {
	for _, body := range []string{
		"version: 1\n", "version: 2\nreview: null\n", "version: 2\nreview: {instructions: old}\n",
		"version: 2\npost_merge: []\n", "version: 2\nrules: []\n", "version: 2\nfeedback: {}\n",
		"version: 2\nreview:\n- id: a\n  instructions: ok\n- id: a\n  instructions: duplicate\n",
		"version: 2\nreview:\n- id: a\n  inherit: append\n  instructions: ok\n",
		"version: 2\nafter_repo:\n- id: a\n  instructions: ' '\n",
		"version: 2\nafter_merge:\n- id: a\n  command: [echo]\n",
		"version: 2\nreview:\n- id: a\n  instructions: ok\n  match: {changed_files_any: ['[']}\n",
		"version: 2\npull_requests:\n  filters:\n  - id: a\n    update_types: [unknown]\n",
		"version: 2\npull_requests:\n  filters:\n  - id: a\n    authors: null\n",
	} {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "renstiq.yaml"), body)
			if _, _, err := LoadPolicy(dir, DefaultConfig()); err == nil {
				t.Fatal("accepted", body)
			}
		})
	}
	for _, section := range []string{"review", "on_blocked", "after_merge", "after_repo"} {
		if _, err := policyFiles(t, "", section+":\n- id: disabled\n  enabled: false\n"); err != nil {
			t.Fatal(err)
		}
		if _, err := policyFiles(t, "", section+":\n- id: incomplete\n"); err == nil {
			t.Fatal("enabled task without instructions accepted", section)
		}
	}
}

func TestPublishedExamplesResolve(t *testing.T) {
	c, err := LoadConfig("../../docs/config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("../../docs/renstiq.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), string(body))
	p, enabled, err := LoadPolicy(dir, c)
	if err != nil || !enabled {
		t.Fatal(p, err)
	}
	if len(p.AfterRepo) != 2 || p.AfterRepo[0].ID != "summarize" || p.AfterRepo[1].ID != "check-follow-up" || p.AfterMerge[0].Enabled {
		t.Fatal(p)
	}
	if len(p.Review) != 1 || !strings.Contains(p.Review[0].Instructions, "\n\n公開インターフェース") {
		t.Fatal(p.Review)
	}
	if len(p.PullRequests.Filters) != 2 || !reflect.DeepEqual(p.PullRequests.Filters[0].Types, []string{"patch", "minor"}) {
		t.Fatal(p.PullRequests)
	}
	pr := validPR()
	pr.UpdatesComplete = false
	if status, reasons := selectFilters(p, CandidateFacts{PR: pr, FilesComplete: true, Files: []ChangedFile{{Filename: "go.mod"}}}); status != SelectionCandidate {
		t.Fatal("example's file alternative must work without update metadata", status, reasons)
	}
}
func TestDuplicateCommonIDsAndReenable(t *testing.T) {
	for _, section := range []string{"review", "on_blocked", "after_merge", "after_repo"} {
		if _, err := policyFiles(t, section+":\n- id: a\n  instructions: first\n- id: a\n  instructions: second\n", ""); err == nil {
			t.Fatal("duplicate accepted", section)
		}
	}
	common := "review:\n- id: a\n  enabled: false\n  instructions: retained\n"
	p, err := policyFiles(t, common, "review:\n- id: a\n  inherit: merge\n  enabled: true\n")
	if err != nil || !p.Review[0].Enabled || p.Review[0].Instructions != "retained" {
		t.Fatal(p, err)
	}
}

func TestRootPolicyDoesNotInsertBuiltins(t *testing.T) {
	for _, common := range []string{"", "review:\n- id: only-explicit\n  instructions: inspect\n"} {
		p, err := policyFiles(t, common, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(p.PullRequests.Filters) != 0 || len(p.OnBlocked) != 0 || len(p.AfterMerge) != 0 || len(p.AfterRepo) != 0 {
			t.Fatalf("implicit policy entries: %+v", p)
		}
		if p.PullRequests.LockLabel != "" || p.Merge.Method != "" || p.GitHubAPIReadRetry != (GitHubAPIReadRetry{}) {
			t.Fatalf("implicit policy values: %+v", p)
		}
		want := 0
		if common != "" {
			want = 1
		}
		if len(p.Review) != want {
			t.Fatalf("unexpected review entries: %+v", p.Review)
		}
	}
}

func TestRootRejectsInheritInEveryNamedSection(t *testing.T) {
	sections := []string{"pull_requests.filters", "review", "on_blocked", "after_merge", "after_repo"}
	for _, section := range sections {
		for _, mode := range []string{"merge", "override"} {
			t.Run(section+"/"+mode, func(t *testing.T) {
				entry := map[string]any{"id": "a", "enabled": false, "inherit": mode}
				common := map[string]any{section: []any{entry}}
				if section == "pull_requests.filters" {
					common = map[string]any{"pull_requests": map[string]any{"filters": []any{entry}}}
				}
				input := map[string]any{"version": configVersion, "defaults": common}
				if err := validateSchema("config", input); err == nil || !strings.Contains(err.Error(), "inherit") {
					t.Fatalf("root schema accepted inherit: %v", err)
				}
				if _, err := resolvePolicy(common, nil); err == nil || !strings.Contains(err.Error(), "inherit") {
					t.Fatalf("root resolver accepted inherit: %v", err)
				}
				repo := map[string]any{"version": configVersion}
				for key, value := range common {
					repo[key] = value
				}
				if err := validateSchema("repo", repo); err != nil {
					t.Fatalf("repo rejected inherit: %v", err)
				}
				if _, err := resolvePolicy(nil, common); err != nil {
					t.Fatalf("repo resolution rejected inherit: %v", err)
				}
			})
		}
	}
}

func TestRootHasNoSpecialIDsAndRepoCanMergeThem(t *testing.T) {
	common := "pull_requests:\n  filters:\n  - id: renovate\n    update_types: [patch]\non_blocked:\n- id: feedback\n  instructions: explicit feedback\n- id: long-term-lock\n  instructions: explicit lock\n"
	p, err := policyFiles(t, common, "pull_requests:\n  filters:\n  - id: renovate\n    inherit: merge\n    update_types: [minor]\non_blocked:\n- id: feedback\n  inherit: merge\n  instructions: repo feedback\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.PullRequests.Filters) != 1 || p.PullRequests.Filters[0].Authors != nil || p.PullRequests.Filters[0].Bases != nil || !reflect.DeepEqual(p.PullRequests.Filters[0].Types, []string{"patch", "minor"}) {
		t.Fatalf("unexpected inherited filter: %+v", p.PullRequests)
	}
	if len(p.OnBlocked) != 2 || p.OnBlocked[0].Instructions != "explicit feedback\n\nrepo feedback" || p.OnBlocked[1].Instructions != "explicit lock" {
		t.Fatalf("unexpected inherited instructions: %+v", p.OnBlocked)
	}
}

func TestInheritedFilterEntriesAreAlternatives(t *testing.T) {
	common := "pull_requests:\n  filters:\n  - id: stable\n    authors: ['renovate[bot]']\n    base_branches: [main]\n    update_types: [patch]\n"
	repo := "pull_requests:\n  filters:\n  - id: preview\n    authors: ['renovate[bot]']\n    base_branches: [develop]\n    update_types: [minor]\n"
	policy, err := policyFiles(t, common, repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		base, typ string
		want      SelectionStatus
	}{
		{"main", "patch", SelectionCandidate},
		{"develop", "minor", SelectionCandidate},
		{"main", "minor", SelectionExcluded},
		{"develop", "patch", SelectionExcluded},
	} {
		pr := validPR()
		pr.Base = tc.base
		pr.Updates[0].Type = tc.typ
		if got := SelectCandidate(policy, CandidateFacts{PR: pr}); got.Status != tc.want {
			t.Fatalf("%s %s: got %+v want %s", tc.base, tc.typ, got, tc.want)
		}
	}
}
