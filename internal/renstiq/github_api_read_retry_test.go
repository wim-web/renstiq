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

func TestGitHubAPIReadRetryInheritance(t *testing.T) {
	for _, tc := range []struct {
		name, common, repo string
		want               GitHubAPIReadRetry
	}{
		{"no configured retry", "", "", GitHubAPIReadRetry{}},
		{"common partial completed by repo", "{max_attempts: 5}", "{interval_seconds: 0}", GitHubAPIReadRetry{MaxAttempts: ptr(5), IntervalSeconds: ptr(0.0)}},
		{"common inherited", "{max_attempts: 5, interval_seconds: 0.5}", "", GitHubAPIReadRetry{MaxAttempts: ptr(5), IntervalSeconds: ptr(0.5)}},
		{"repo attempts", "{max_attempts: 5, interval_seconds: 0.5}", "{max_attempts: 1}", GitHubAPIReadRetry{MaxAttempts: ptr(1), IntervalSeconds: ptr(0.5)}},
		{"repo zero interval", "{max_attempts: 5, interval_seconds: 0.5}", "{interval_seconds: 0}", GitHubAPIReadRetry{MaxAttempts: ptr(5), IntervalSeconds: ptr(0.0)}},
		{"empty repo object", "{max_attempts: 5, interval_seconds: 0.5}", "{}", GitHubAPIReadRetry{MaxAttempts: ptr(5), IntervalSeconds: ptr(0.5)}},
		{"repo only", "", "{max_attempts: 3, interval_seconds: 0.25}", GitHubAPIReadRetry{MaxAttempts: ptr(3), IntervalSeconds: ptr(0.25)}},
		{"single attempt", "{max_attempts: 1}", "", GitHubAPIReadRetry{MaxAttempts: ptr(1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			common := filepath.Join(dir, "config.yaml")
			body := "version: 2\n"
			if tc.common != "" {
				body += "defaults:\n  github_api_read_retry: " + tc.common + "\n"
			}
			writeFile(t, common, body)
			c, err := LoadConfig(common)
			if err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(c.Defaults)
			if err != nil {
				t.Fatal(err)
			}
			body = "version: 2\nenabled: true\n"
			if tc.repo != "" {
				body += "github_api_read_retry: " + tc.repo + "\n"
			}
			writeFile(t, filepath.Join(dir, "renstiq.yaml"), body)
			p, _, err := LoadPolicy(dir, c)
			if err != nil || !reflect.DeepEqual(p.GitHubAPIReadRetry, tc.want) {
				t.Fatalf("got %+v, err=%v; want %+v", p.GitHubAPIReadRetry, err, tc.want)
			}
			after, err := json.Marshal(c.Defaults)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("repository override mutated common defaults: %s -> %s, %v", before, after, err)
			}
		})
	}
}

func TestGitHubAPIReadRetryValidation(t *testing.T) {
	for _, value := range []string{
		"null", "[]", "{unknown: 1}", "{max_attempts: null}", "{max_attempts: 0}",
		"{max_attempts: 101}", "{max_attempts: 1.5}", "{max_attempts: '3'}",
		"{interval_seconds: null}", "{interval_seconds: -1}", "{interval_seconds: 86401}",
	} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			common := filepath.Join(dir, "config.yaml")
			writeFile(t, common, "version: 2\ndefaults:\n  github_api_read_retry: "+value+"\n")
			if _, err := LoadConfig(common); err == nil || !strings.Contains(err.Error(), "github_api_read_retry") {
				t.Fatalf("invalid common retry accepted or incorrect error: %v", err)
			}
			writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 2\ngithub_api_read_retry: "+value+"\n")
			if _, _, err := LoadPolicy(dir, DefaultConfig()); err == nil || !strings.Contains(err.Error(), "github_api_read_retry") {
				t.Fatalf("invalid repo retry accepted or incorrect error: %v", err)
			}
		})
	}
	for _, body := range []string{
		"retry: {max_attempts: 3, interval_seconds: 2}",
		"github_api_read_retry: {max_attempts: 3}",
		"defaults:\n  retry: {max_attempts: 3}",
	} {
		common := filepath.Join(t.TempDir(), "config.yaml")
		writeFile(t, common, "version: 2\n"+body+"\n")
		if _, err := LoadConfig(common); err == nil {
			t.Fatalf("misplaced or old retry key accepted: %s", body)
		}
	}
}

func TestPRListUsesResolvedGitHubAPIReadRetry(t *testing.T) {
	t.Setenv("GH_TOKEN", "test")
	dir := cliRepo(t, t.TempDir(), "repo", "https://github.com/o/r.git")
	common := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, common, "version: 2\ndefaults:\n  github_api_read_retry: {max_attempts: 3, interval_seconds: 0.25}\n")
	for _, tc := range []struct {
		name, repo string
		want       GitHubAPIReadRetry
	}{
		{"common", "", GitHubAPIReadRetry{MaxAttempts: ptr(3), IntervalSeconds: ptr(0.25)}},
		{"repo attempts", "github_api_read_retry: {max_attempts: 1}\n", GitHubAPIReadRetry{MaxAttempts: ptr(1), IntervalSeconds: ptr(0.25)}},
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
			result, err := app.PRList(context.Background(), PRListRequest{Repo: dir, ConfigPath: common})
			if err == nil || result.Complete || calls != *tc.want.MaxAttempts || sleeps != *tc.want.MaxAttempts-1 {
				t.Fatalf("result=%+v err=%v calls=%d sleeps=%d; want %+v", result, err, calls, sleeps, tc.want)
			}
			var out, log bytes.Buffer
			code := newCLI(app, nil).Run(context.Background(), []string{"config", "show", "--repo", dir, "--config", common}, nil, &out, &log)
			var shown ConfigResult
			if err := json.Unmarshal(out.Bytes(), &shown); err != nil || code != 0 || shown.Config == nil || !reflect.DeepEqual(shown.Config.GitHubAPIReadRetry, tc.want) {
				t.Fatalf("config show: code=%d out=%s log=%s err=%v", code, out.String(), log.String(), err)
			}
			assertCLIOutputSchema(t, "config-show", out.Bytes())
		})
	}
}
