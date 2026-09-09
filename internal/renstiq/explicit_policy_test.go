package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestPRListAuthorsComeOnlyFromConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, filter string
		want         []int
	}{
		{"no rules", "", []int{}},
		{"no author restriction", "rules:\n- id: all\n  instructions: inspect\n", []int{1, 2, 3}},
		{"human configured", "rules:\n- id: human\n  authors: [alice]\n  instructions: inspect\n", []int{2}},
		{"Renovate configured", "rules:\n- id: renovate\n  authors: ['renovate[bot]']\n  instructions: inspect\n", []int{1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			dir := cliRepo(t, t.TempDir(), "repo", "https://github.com/o/r.git")
			writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 2\nenabled: true\n"+tc.filter)
			rows := []rawPR{}
			for i, author := range []string{"renovate[bot]", "alice", "dependabot[bot]"} {
				pr := rawFixture(i + 1)
				pr.User.Login = author
				pr.Body = "No dependency update table."
				rows = append(rows, pr)
			}
			g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/o/r/pulls" {
					t.Error("unconfigured details fetched", r.URL)
					http.Error(w, "unexpected request", 500)
					return
				}
				respond(t, w, rows)
			})
			for _, all := range []bool{false, true} {
				var out, log bytes.Buffer
				app := newApplication(&log)
				app.Reader = func(context.Context, GitHubAPIReadRetry) (PRListReader, error) { return g, nil }
				args := []string{"pr", "list", "--repo", dir}
				if all {
					args = append(args, "--all")
				}
				if code := newCLI(app, nil).Run(context.Background(), args, nil, &out, &log); code != 0 {
					t.Fatal(code, out.String(), log.String())
				}
				assertCLIOutputSchema(t, "pr-list", out.Bytes())
				var result PRListResult
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if !result.Complete || result.OpenPRCount == nil || *result.OpenPRCount != 3 {
					t.Fatal(result)
				}
				got := []int{}
				for _, pr := range result.PullRequests {
					if pr.Status == SelectionCandidate {
						got = append(got, pr.Number)
					}
					wantReviews := 0
					if pr.Status == SelectionCandidate {
						wantReviews = 1
					}
					if pr.Review == nil || len(pr.Review) != wantReviews || len(pr.ReviewIDs) != wantReviews {
						t.Fatal("unconfigured review inserted", pr)
					}
				}
				if !reflect.DeepEqual(got, tc.want) || (all && len(result.PullRequests) != 3) || (!all && len(result.PullRequests) != len(tc.want)) {
					t.Fatal(all, result)
				}
				var wire map[string]any
				if err := json.Unmarshal(out.Bytes(), &wire); err != nil {
					t.Fatal(err)
				}
				if _, exists := wire["open_renovate_count"]; exists {
					t.Fatal("misleading legacy count retained", wire)
				}
				for _, row := range wire["pull_requests"].([]any) {
					if _, exists := row.(map[string]any)["review_required"]; exists {
						t.Fatal("fixed review requirements retained", row)
					}
				}
			}
		})
	}
}

func TestGitHubReadRetriesRequireExplicitPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy GitHubAPIReadRetry
		calls  int
		delays []time.Duration
	}{
		{"omitted", GitHubAPIReadRetry{}, 1, nil},
		{"one attempt", GitHubAPIReadRetry{MaxAttempts: ptr(1)}, 1, nil},
		{"missing interval", GitHubAPIReadRetry{MaxAttempts: ptr(2)}, 0, nil},
		{"missing attempts", GitHubAPIReadRetry{IntervalSeconds: ptr(0.0)}, 0, nil},
		{"explicit immediate retry", GitHubAPIReadRetry{MaxAttempts: ptr(3), IntervalSeconds: ptr(0.0)}, 3, []time.Duration{0, 0}},
		{"fixed interval ignores Retry-After", GitHubAPIReadRetry{MaxAttempts: ptr(2), IntervalSeconds: ptr(0.25)}, 2, []time.Duration{250 * time.Millisecond}},
		{"explicit Retry-After", GitHubAPIReadRetry{MaxAttempts: ptr(2), IntervalSeconds: ptr(0.25), RespectRetryAfter: true}, 2, []time.Duration{10 * time.Second}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var delays []time.Duration
			g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Retry-After", "10")
				http.Error(w, `{"message":"retry later"}`, http.StatusTooManyRequests)
			})
			g.ReadRetry = tc.policy
			g.Sleep = func(_ context.Context, delay time.Duration) error { delays = append(delays, delay); return nil }
			_, err := g.OpenPullRequests(context.Background(), "o/r")
			if err == nil || calls != tc.calls || !reflect.DeepEqual(delays, tc.delays) {
				t.Fatal(err, calls, delays)
			}
		})
	}
}

func TestConfigShowDistinguishesOmittedAndZeroRetryInterval(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := cliRepo(t, t.TempDir(), "repo", "https://github.com/o/r.git")
	for _, retry := range []string{"", "github_api_read_retry: {max_attempts: 2, interval_seconds: 0}\n"} {
		writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 2\nenabled: true\n"+retry)
		var out, log bytes.Buffer
		if code := newCLI(newApplication(&log), nil).Run(context.Background(), []string{"config", "show", "--repo", dir}, nil, &out, &log); code != 0 {
			t.Fatal(out.String(), log.String())
		}
		assertCLIOutputSchema(t, "config-show", out.Bytes())
		var wire map[string]any
		if err := json.Unmarshal(out.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		shown := wire["config"].(map[string]any)["github_api_read_retry"].(map[string]any)
		if retry == "" {
			if len(shown) != 0 {
				t.Fatal("retry defaults inserted", shown)
			}
		} else if interval, exists := shown["interval_seconds"]; !exists || interval != float64(0) {
			t.Fatal("explicit zero omitted from output", shown)
		}
	}
}
