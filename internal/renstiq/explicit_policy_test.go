package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPRListAuthorsComeOnlyFromConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, filter string
		want         []int
	}{
		{"no author restriction", "", []int{1, 2, 3}},
		{"human configured", "pull_requests:\n  filters:\n  - id: human\n    authors: [alice]\n", []int{2}},
		{"Renovate configured", "pull_requests:\n  filters:\n  - id: renovate\n    authors: ['renovate[bot]']\n", []int{1}},
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
					if pr.Review == nil || len(pr.Review) != 0 || len(pr.ReviewIDs) != 0 {
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
