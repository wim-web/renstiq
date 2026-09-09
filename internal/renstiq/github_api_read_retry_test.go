package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRepoGitHubAPIReadRetry(t *testing.T) {
	for _, value := range []string{"{}", "{max_attempts: 1}", "{max_attempts: 3, interval_seconds: 0}", "{max_attempts: 3, interval_seconds: 0.5, respect_retry_after: true}"} {
		if _, err := repoPolicy(t, "github_api_read_retry: "+value+"\n"); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"null", "[]", "{unknown: 1}", "{max_attempts: 0}", "{max_attempts: 101}", "{max_attempts: 1.5}", "{max_attempts: '3'}", "{max_attempts: 3}", "{interval_seconds: 0}", "{respect_retry_after: true}", "{max_attempts: 3, interval_seconds: -1}", "{max_attempts: 3, interval_seconds: 86401}"} {
		if _, err := repoPolicy(t, "github_api_read_retry: "+value+"\n"); err == nil || !strings.Contains(err.Error(), "github_api_read_retry") {
			t.Fatal(value, err)
		}
	}
}
func TestPRListUsesResolvedGitHubAPIReadRetry(t *testing.T) {
	t.Setenv("GH_TOKEN", "test")
	dir := cliRepo(t, t.TempDir(), "repo", "https://github.com/o/r.git")
	for _, tc := range []struct {
		name, repo string
		want       GitHubAPIReadRetry
	}{
		{"repo", "github_api_read_retry: {max_attempts: 3, interval_seconds: 0.25}\n", GitHubAPIReadRetry{MaxAttempts: ptr(3), IntervalSeconds: ptr(0.25)}},
		{"repo attempts", "github_api_read_retry: {max_attempts: 1, interval_seconds: 0.25}\n", GitHubAPIReadRetry{MaxAttempts: ptr(1), IntervalSeconds: ptr(0.25)}},
		{"repo interval", "github_api_read_retry: {max_attempts: 2, interval_seconds: 0}\n", GitHubAPIReadRetry{MaxAttempts: ptr(2), IntervalSeconds: ptr(0.0)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 2\nenabled: true\n"+tc.repo)
			calls, sleeps := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				http.Error(w, `{"message":"temporary"}`, http.StatusServiceUnavailable)
			}))
			defer server.Close()
			app := newApplication(io.Discard)
			newReader := app.Reader
			app.Reader = func(ctx context.Context, retry GitHubAPIReadRetry) (PRListReader, error) {
				reader, err := newReader(ctx, retry)
				if err != nil {
					return nil, err
				}
				g := reader.(*GitHub)
				g.BaseURL = server.URL
				g.Sleep = func(_ context.Context, delay time.Duration) error {
					sleeps++
					if want := time.Duration(*tc.want.IntervalSeconds * float64(time.Second)); delay != want {
						t.Errorf("retry delay=%v, want %v", delay, want)
					}
					return nil
				}
				return g, nil
			}
			result, err := app.PRList(context.Background(), PRListRequest{Repo: dir})
			if err == nil || result.Complete || calls != *tc.want.MaxAttempts || sleeps != *tc.want.MaxAttempts-1 {
				t.Fatalf("result=%+v err=%v calls=%d sleeps=%d; want %+v", result, err, calls, sleeps, tc.want)
			}
			var out, log bytes.Buffer
			code := newCLI(app, nil).Run(context.Background(), []string{"config", "show", "--repo", dir}, nil, &out, &log)
			var shown ConfigResult
			if err := json.Unmarshal(out.Bytes(), &shown); err != nil || code != 0 || shown.Config == nil || !reflect.DeepEqual(shown.Config.GitHubAPIReadRetry, tc.want) {
				t.Fatalf("config show: code=%d out=%s log=%s err=%v", code, out.String(), log.String(), err)
			}
			assertCLIOutputSchema(t, "config-show", out.Bytes())
		})
	}
}
