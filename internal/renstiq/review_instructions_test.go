package renstiq

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoPolicy(t *testing.T, body string) (Policy, error) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 2\nenabled: true\n"+body)
	p, _, err := LoadPolicy(dir)
	return p, err
}

func TestRepositoryPolicyValidation(t *testing.T) {
	for _, body := range []string{
		"rules: [{id: a}]", "rules: [{id: a, instructions: ' '}]",
		"rules: [{id: a, instructions: one}, {id: a, instructions: two}]",
		"rules: [{id: a, instructions: inspect, inherit: merge}]",
		"rules: [{id: a, instructions: inspect, filter_ids: [a]}]",
		"rules: [{id: a, instructions: inspect, filter_ids_mode: exact}]",
		"rules: [{id: a, instructions: inspect, labels: null}]",
		"rules: [{id: a, instructions: inspect, labels: minor}]",
		"rules: [{id: a, instructions: inspect, authors: [123]}]",
		"rules: [{id: a, instructions: inspect, files: ['[']}]",
		"rules: [{id: a, instructions: inspect, head_branches: ['[']}]",
		"rules: [{id: a, instructions: inspect, update_types: [unknown]}]",
		"review: []", "pull_requests: {filters: []}", "defaults: {}",
	} {
		t.Run(body, func(t *testing.T) {
			if _, err := repoPolicy(t, body+"\n"); err == nil {
				t.Fatal("obsolete or invalid policy accepted", body)
			}
		})
	}
	for _, section := range []string{"rules", "on_blocked", "after_merge", "after_repo"} {
		for _, body := range []string{
			section + ": [{id: a, instructions: one}, {id: a, instructions: two}]",
			section + ": [{id: a, inherit: override, instructions: inspect}]",
			section + ": [{id: a, instructions: ' '}]",
		} {
			if _, err := repoPolicy(t, body+"\n"); err == nil {
				t.Fatal(body)
			}
		}
		if _, err := repoPolicy(t, section+": [{id: disabled, enabled: false}]\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRepositoryPolicyDoesNotInsertInstructions(t *testing.T) {
	p, err := repoPolicy(t, "")
	if err != nil || len(p.Rules) != 0 || len(p.OnBlocked) != 0 || len(p.AfterMerge) != 0 || len(p.AfterRepo) != 0 || p.Merge.Method != "" || p.GitHubAPIReadRetry != (GitHubAPIReadRetry{}) {
		t.Fatal(p, err)
	}
	if got := SelectCandidate(p, CandidateFacts{PR: validPR()}); got.Status != SelectionExcluded || len(got.Review) != 0 {
		t.Fatal(got)
	}
}

func TestPublishedExamplesResolve(t *testing.T) {
	if _, err := LoadConfig("../../docs/config.example.yaml"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("../../docs/renstiq.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), string(body))
	p, enabled, err := LoadPolicy(dir)
	if err != nil || !enabled || len(p.Rules) == 0 {
		t.Fatal(p, err)
	}
}

func TestDiscoveryConfigRejectsSharedPolicy(t *testing.T) {
	for _, body := range []string{"defaults: {}", "rules: []", "merge: {method: squash}", "github_api_read_retry: {max_attempts: 3, interval_seconds: 2}"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		writeFile(t, path, "version: 2\n"+body+"\n")
		if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), strings.Split(body, ":")[0]) {
			t.Fatal(body, err)
		}
	}
}
