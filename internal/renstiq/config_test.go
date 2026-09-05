package renstiq

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0644); e != nil {
		t.Fatal(e)
	}
}
func TestStrictConfig(t *testing.T) {
	for _, s := range []string{"version: 1\nenabled: 'true'\n", "version: 1\nenabled: true\nunknown: x\n", "version: 1\nenabled: true\nchecks:\n  minimum: '1'\n", "version: 1\nenabled: true\nenabled: false\n", "version: 1\nenabled: true\n---\nversion: 1\n", "version: 1\nenabled: null\n", "version: 1\nenabled: true\npost_merge:\n- id: a\n  timing: after_repo\n  command: [echo]\n  retry: 2\n"} {
		t.Run(s, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "renstiq.yaml"), s)
			if _, _, e := LoadPolicy(dir, DefaultConfig()); e == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
func TestInheritance(t *testing.T) {
	dir := t.TempDir()
	c := DefaultConfig()
	c.Defaults = map[string]any{"checks": map[string]any{"minimum": float64(1), "all_success": true}, "post_merge": []any{map[string]any{"id": "a", "timing": "after_repo", "command": []any{"echo"}}}}
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: true\nchecks:\n  minimum: 0\npost_merge: []\n")
	p, _, e := LoadPolicy(dir, c)
	if e != nil {
		t.Fatal(e)
	}
	if p.Checks.Minimum != 0 || !p.Checks.AllSuccess || len(p.PostMerge) != 0 {
		t.Fatalf("inheritance failed: %+v", p)
	}
	cpath := filepath.Join(dir, "config.yaml")
	writeFile(t, cpath, "version: 1\ndefaults:\n  enabled: true\n")
	if _, e = LoadConfig(cpath); e == nil {
		t.Fatal("global participation accepted")
	}
}
func TestDiscovery(t *testing.T) {
	root := t.TempDir()
	root, _ = canonicalDir(root)
	for _, d := range []string{"on", "off", "invalid", "none", "on/nested", "excluded"} {
		dir := filepath.Join(root, d)
		if e := os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
		if _, e := git(context.Background(), dir, "init", "--initial-branch=main"); e != nil {
			t.Fatal(e)
		}
	}
	writeFile(t, filepath.Join(root, "on", "renstiq.yaml"), "version: 1\nenabled: true\n")
	writeFile(t, filepath.Join(root, "on/nested", "renstiq.yaml"), "version: 1\nenabled: true\n")
	writeFile(t, filepath.Join(root, "off", "renstiq.yaml"), "version: 1\nenabled: false\n")
	writeFile(t, filepath.Join(root, "invalid", "renstiq.yaml"), "version: 1\nenabled: yes\n")
	c := DefaultConfig()
	c.Discovery.Include = []string{root + "/*/", root + "/on/"}
	c.Discovery.Exclude = []string{root + "/excluded/"}
	a := Discover(c)
	if len(a) != 5 {
		t.Fatalf("got %d paths: %+v", len(a), a)
	}
	statuses := map[string]string{}
	for _, v := range a {
		statuses[filepath.Base(v.Path)] = v.Status
	}
	for k, want := range map[string]string{"on": "enabled", "off": "disabled", "invalid": "config_error", "none": "no_config", "excluded": "excluded"} {
		if statuses[k] != want {
			t.Errorf("%s: got %s", k, statuses[k])
		}
	}
	if _, ok := statuses["nested"]; ok {
		t.Fatal("single star recursively discovered nested repo")
	}
	c.Discovery.Include = []string{root + "/*/*/"}
	a = Discover(c)
	found := false
	for _, v := range a {
		if strings.HasSuffix(v.Path, "on/nested") && v.Status == "enabled" {
			found = true
		}
	}
	if !found {
		t.Fatal("two levels missing")
	}
	c.Discovery.Include = []string{root + "/**/"}
	a = Discover(c)
	found = false
	self := false
	for _, v := range a {
		if v.Path == root {
			self = true
		}
		if strings.HasSuffix(v.Path, "on/nested") && v.Status == "enabled" {
			found = true
		}
	}
	if !found || !self {
		t.Fatalf("recursive glob must include root and descendants: root=%s found=%v self=%v paths=%+v", root, found, self, a)
	}
}
func TestDecisionSchema(t *testing.T) {
	d := validDecision()
	b, _ := json.Marshal(d)
	parsed, e := ReadDecision(strings.NewReader(string(b)))
	if e != nil {
		t.Fatal(e)
	}
	if e := parsed.Validate(d.Repo, d.ConfigDigest, defaultPolicy()); e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	_ = json.Unmarshal(b, &v)
	v["command"] = []string{"rm", "x"}
	b, _ = json.Marshal(v)
	if _, e := ReadDecision(strings.NewReader(string(b))); e == nil {
		t.Fatal("arbitrary command accepted")
	}
}
func validDecision() Decision {
	d := Decision{Version: 1, Repo: "o/r", PR: 1, HeadSHA: strings.Repeat("a", 40), BaseSHA: strings.Repeat("b", 40), ConfigDigest: digest(defaultPolicy()), Decision: "merge", ReasonType: "compatible", Reason: "investigated", Evidence: []Evidence{{"https://example.com/releases", "compatible with usage"}}, Updates: []Update{{Dependency: "dep", Type: "patch", Files: []string{"go.mod"}}}, PostMerge: []PostChoice{}}
	d.Review.InstructionsFollowed = true
	d.Review.UpstreamChecked = true
	d.Review.UsageChecked = true
	d.Review.NoUnresolvedRequests = true
	d.Review.Compatible = true
	return d
}
func validPR() PullRequest {
	yes := true
	return PullRequest{Number: 1, State: "open", Author: "renovate[bot]", Base: "main", Head: "renovate/dep", HeadSHA: strings.Repeat("a", 40), BaseSHA: strings.Repeat("b", 40), Mergeable: &yes, MergeState: "CLEAN", Files: []string{"go.mod"}}
}
func TestChecksAndRules(t *testing.T) {
	p := defaultPolicy()
	pr := validPR()
	d := validDecision()
	if r := policyReasons(p, pr, d); len(r) > 0 {
		t.Fatal(r)
	}
	p.Checks.Minimum = 1
	if len(policyReasons(p, pr, d)) == 0 {
		t.Fatal("missing checks accepted")
	}
	pr.Checks = []Check{{Name: "test", Workflow: "wrong", Status: "completed", Conclusion: "success"}}
	p.Checks.Required = []CheckRequirement{{Name: "test", Workflow: "test"}}
	if len(policyReasons(p, pr, d)) == 0 {
		t.Fatal("wrong workflow accepted")
	}
	pr.Checks[0].Workflow = "test"
	if r := policyReasons(p, pr, d); len(r) > 0 {
		t.Fatal(r)
	}
	pr.Checks[0].Conclusion = "skipped"
	if len(policyReasons(p, pr, d)) == 0 {
		t.Fatal("skipped check accepted")
	}
	pr.Checks[0].Conclusion = "success"
	p.Rules = []Rule{{ID: "go", Files: []string{"go.mod"}, Types: []string{"patch", "minor"}, Dependencies: []string{"dep"}}}
	d.Updates[0].Type = "major"
	if len(policyReasons(p, pr, d)) == 0 {
		t.Fatal("major accepted")
	}
	d.Updates[0].Type = "patch"
	pr.Files = append(pr.Files, "source.go")
	if len(policyReasons(p, pr, d)) == 0 {
		t.Fatal("uninvestigated source change accepted")
	}
}

func TestEmptyRequiredChecksSurviveStateRoundTrip(t *testing.T) {
	p := defaultPolicy()
	p.Checks.Required = []CheckRequirement{{Name: "global"}}
	p.Rules = []Rule{{ID: "override", Files: []string{"go.mod"}, Types: []string{"patch"}, Checks: &ChecksPatch{Required: []CheckRequirement{}}}}
	before := digest(p)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var saved Policy
	if err = json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Rules[0].Checks.Required == nil {
		t.Fatal("empty array was lost in persistent state")
	}
	if reasons := policyReasons(saved, validPR(), validDecision()); len(reasons) > 0 {
		t.Fatal(reasons)
	}
	p.Rules[0].Checks.Required = nil
	if before == digest(p) {
		t.Fatal("nil and empty configuration have identical digests")
	}
}
