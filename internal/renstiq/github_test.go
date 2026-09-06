package renstiq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func rawFixture(n int) rawPR {
	p := rawPR{Number: n, Title: "Update dependency to v99", URL: fmt.Sprintf("https://github.com/o/r/pull/%d", n), State: "open", ChangedFiles: ptr(1), Commits: ptr(1)}
	p.User.Login = "renovate[bot]"
	p.Base.Ref = "main"
	p.Base.SHA = "base"
	p.Head.Ref = "renovate/dep"
	p.Head.SHA = "head"
	return p
}
func readGitHub(t *testing.T, handler http.HandlerFunc) *GitHub {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected write", 500)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/repos/o/r/pulls") {
			t.Errorf("unexpected endpoint: %s", r.URL)
			http.Error(w, "unexpected endpoint", 500)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return &GitHub{BaseURL: server.URL, Token: "test", HTTP: server.Client(), Retry: Retry{MaxAttempts: 1}, Sleep: func(context.Context, time.Duration) error { t.Error("unexpected waiting"); return nil }}
}
func respond(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Error(err)
	}
}
func pageSlice[T any](t *testing.T, r *http.Request, rows []T) []T {
	t.Helper()
	if r.URL.Query().Get("per_page") != "100" {
		t.Error("wrong page size", r.URL)
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		t.Fatal("missing page", r.URL)
	}
	start := (page - 1) * 100
	if start >= len(rows) {
		return []T{}
	}
	end := start + 100
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end]
}
func TestPRListPaginationPopulationAndNoDetails(t *testing.T) {
	rows := []rawPR{}
	for n := 1; n <= 103; n++ {
		p := rawFixture(n)
		p.Draft = n == 3
		rows = append(rows, p)
	}
	rows[0].User.Login = "human"
	rows[1].User.Login = "dependabot[bot]"
	rows[3].Base.Ref = "develop"
	pages := 0
	g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/pulls" || r.URL.Query().Get("state") != "open" {
			t.Error("unnecessary details", r.URL)
			http.Error(w, "unexpected", 500)
			return
		}
		pages++
		respond(t, w, pageSlice(t, r, rows))
	})
	p := defaultPolicy()
	p.PullRequests.Authors = append(p.PullRequests.Authors, "human")
	for _, all := range []bool{false, true} {
		result, err := listCandidates(context.Background(), g, emptyPRResult(), p, all)
		want := 100
		if all {
			want = 101
		}
		if err != nil || !result.Complete || result.OpenRenovateCount == nil || *result.OpenRenovateCount != 101 || len(result.PullRequests) != want {
			t.Fatal(result, err)
		}
		if !result.PullRequests[0].Draft || result.PullRequests[0].Status != "candidate" {
			t.Fatal("draft hidden", result.PullRequests[0])
		}
		if err := validateSchema("pr-list", asMap(result)); err != nil {
			t.Fatal(err)
		}
	}
	if pages != 4 {
		t.Fatal("pagination calls", pages)
	}
}
func emptyPRResult() PRListResult {
	return PRListResult{Version: 1, Repo: "o/r", Path: "/repo", PullRequests: []PRListItem{}, Errors: []ReadError{}}
}
func TestInitialListPartialFailureAndUnknownCount(t *testing.T) {
	for _, failPage := range []int{1, 2} {
		g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page == failPage {
				http.Error(w, `{"message":"unavailable"}`, 500)
				return
			}
			rows := []rawPR{}
			for n := 1; n <= 100; n++ {
				rows = append(rows, rawFixture(n))
			}
			respond(t, w, rows)
		})
		result, err := listCandidates(context.Background(), g, emptyPRResult(), defaultPolicy(), false)
		if err == nil || result.Complete || result.OpenRenovateCount != nil || len(result.Errors) != 1 || len(result.PullRequests) != (failPage-1)*100 {
			t.Fatal(result, err)
		}
	}
}
func TestShortPageWithNextLink(t *testing.T) {
	calls := 0
	g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Link", `<https://api.github.com/repos/o/r/pulls?page=2>; rel="next"`)
		}
		respond(t, w, []rawPR{rawFixture(calls)})
	})
	result, err := listCandidates(context.Background(), g, emptyPRResult(), defaultPolicy(), false)
	if err != nil || calls != 2 || result.OpenRenovateCount == nil || *result.OpenRenovateCount != 2 {
		t.Fatal(result, calls, err)
	}
}
func TestMalformedAndRepeatedListPages(t *testing.T) {
	for _, kind := range []string{"null", "bad author", "duplicate", "repeated"} {
		t.Run(kind, func(t *testing.T) {
			g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
				p := rawFixture(1)
				switch kind {
				case "null":
					respond(t, w, nil)
				case "bad author":
					p.User.Login = ""
					respond(t, w, []rawPR{p})
				case "duplicate":
					respond(t, w, []rawPR{p, p})
				case "repeated":
					w.Header().Set("Link", `<https://api.github.com/repos/o/r/pulls?page=2>; rel="next"`)
					respond(t, w, []rawPR{p})
				}
			})
			result, err := listCandidates(context.Background(), g, emptyPRResult(), defaultPolicy(), false)
			if err == nil || result.Complete || result.OpenRenovateCount != nil {
				t.Fatal(result, err)
			}
		})
	}
}
func TestDetailPagingCountsChangesAndFailures(t *testing.T) {
	cases := []string{"complete", "file mismatch", "file limit", "commit mismatch", "commit limit", "unknown author", "empty login", "missing file count", "missing commit count", "duplicate file", "duplicate commit", "missing file status", "rename without old name", "files fail", "commits fail", "head changed", "base changed", "state changed", "branch changed", "changed before details", "final read fails", "file count changed", "commit count changed"}
	for _, kind := range cases {
		t.Run(kind, func(t *testing.T) {
			p := rawFixture(1)
			p.ChangedFiles = ptr(101)
			p.Commits = ptr(101)
			fileCount, commitCount := 101, 101
			switch kind {
			case "file mismatch":
				fileCount = 100
			case "file limit":
				p.ChangedFiles = ptr(3001)
				fileCount = 3000
			case "commit mismatch":
				commitCount = 100
			case "commit limit":
				p.Commits = ptr(251)
				commitCount = 250
			case "missing file count":
				p.ChangedFiles = nil
			case "missing commit count":
				p.Commits = nil
			}
			fileRows := []ChangedFile{}
			for n := 0; n < fileCount; n++ {
				fileRows = append(fileRows, ChangedFile{Filename: fmt.Sprintf("files/%d", n), Status: "modified"})
			}
			if kind == "duplicate file" {
				fileRows[1] = fileRows[0]
			}
			if kind == "missing file status" {
				fileRows[0].Status = ""
			}
			if kind == "rename without old name" {
				fileRows[0].Status = "renamed"
			}
			commitRows := []map[string]any{}
			for n := 0; n < commitCount; n++ {
				commitRows = append(commitRows, map[string]any{"sha": fmt.Sprint(n), "author": map[string]any{"login": "renovate[bot]"}})
			}
			if kind == "unknown author" {
				commitRows[0]["author"] = nil
			}
			if kind == "empty login" {
				commitRows[0]["author"] = map[string]any{"login": ""}
			}
			if kind == "duplicate commit" {
				commitRows[1] = commitRows[0]
			}
			rawCalls, fileCalls, commitCalls := 0, 0, 0
			g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/o/r/pulls/1":
					rawCalls++
					current := p
					if rawCalls == 2 || kind == "changed before details" {
						switch kind {
						case "head changed", "changed before details":
							current.Head.SHA = "new-head"
						case "base changed":
							current.Base.SHA = "new-base"
						case "state changed":
							current.State = "closed"
						case "branch changed":
							current.Base.Ref = "other"
						case "file count changed":
							current.ChangedFiles = ptr(102)
						case "commit count changed":
							current.Commits = ptr(102)
						case "final read fails":
							http.Error(w, `{"message":"failed"}`, 500)
							return
						}
					}
					respond(t, w, current)
				case "/repos/o/r/pulls/1/files":
					fileCalls++
					if kind == "files fail" {
						http.Error(w, `{"message":"failed"}`, 500)
						return
					}
					respond(t, w, pageSlice(t, r, fileRows))
				case "/repos/o/r/pulls/1/commits":
					commitCalls++
					if kind == "commits fail" {
						http.Error(w, `{"message":"failed"}`, 500)
						return
					}
					respond(t, w, pageSlice(t, r, commitRows))
				default:
					t.Error("unexpected endpoint", r.URL)
					http.Error(w, "unexpected", 500)
				}
			})
			facts, err := g.CandidateDetails(context.Background(), "o/r", p.info(), true, true)
			policy := defaultPolicy()
			policy.PullRequests.Files = []string{"**"}
			policy.PullRequests.CommitAuthors = []string{"renovate[bot]"}
			if err != nil {
				facts.Problems = append(facts.Problems, err.Error())
			}
			selected := SelectCandidate(policy, facts)
			if kind == "complete" {
				if err != nil || selected.Status != "candidate" || fileCalls != 2 || commitCalls != 2 || rawCalls != 2 {
					t.Fatal(selected, err, fileCalls, commitCalls, rawCalls)
				}
			} else if err == nil || selected.Status != "unknown" {
				t.Fatal(kind, selected, err)
			}
			if kind == "changed before details" && (fileCalls != 0 || commitCalls != 0 || rawCalls != 1) {
				t.Fatal("retrieved stale details")
			}
		})
	}
}
func TestOnlyRequiredDetailsFetched(t *testing.T) {
	for _, files := range []bool{false, true} {
		g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/repos/o/r/pulls/1":
				respond(t, w, rawFixture(1))
			case "/repos/o/r/pulls/1/files":
				if !files {
					t.Error("unnecessary files")
				}
				respond(t, w, []ChangedFile{{Filename: "go.mod", Status: "modified"}})
			case "/repos/o/r/pulls/1/commits":
				if files {
					t.Error("unnecessary commits")
				}
				respond(t, w, []map[string]any{{"sha": "head", "author": map[string]any{"login": "renovate[bot]"}}})
			default:
				t.Error(r.URL)
			}
		})
		facts, err := g.CandidateDetails(context.Background(), "o/r", validPR(), files, !files)
		if err != nil || facts.FilesComplete != files || facts.CommitsComplete == files {
			t.Fatal(facts, err)
		}
	}
}
func TestReadRetryAndCancellation(t *testing.T) {
	calls, sleeps := 0, 0
	g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			http.Error(w, `{"message":"temporary"}`, 503)
			return
		}
		respond(t, w, []rawPR{})
	})
	g.Retry = Retry{MaxAttempts: 3}
	g.Sleep = func(context.Context, time.Duration) error { sleeps++; return nil }
	g.Log = io.Discard
	rows, err := g.OpenPullRequests(context.Background(), "o/r")
	if err != nil || len(rows) != 0 || calls != 3 || sleeps != 2 {
		t.Fatal(rows, err, calls, sleeps)
	}
	calls = 0
	g.Sleep = func(context.Context, time.Duration) error { return context.Canceled }
	if _, err := g.OpenPullRequests(context.Background(), "o/r"); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal(err, calls)
	}
}

func TestRetryDoesNotReusePartiallyDecodedFields(t *testing.T) {
	calls := 0
	g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_, _ = io.WriteString(w, `{"number":1,"changed_files":1,"commits":"wrong-type"}`)
		} else {
			_, _ = io.WriteString(w, `{"number":1}`)
		}
	})
	g.Retry.MaxAttempts = 2
	g.Sleep = func(context.Context, time.Duration) error { return nil }
	result, err := g.raw(context.Background(), "o/r", 1)
	if err != nil || calls != 2 || result.ChangedFiles != nil {
		t.Fatal("fields leaked across retries", result, err)
	}
}

func TestCIAndMergeBlockersAreLeftForAIReview(t *testing.T) {
	for _, status := range []string{"pending", "failure"} {
		g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/repos/o/r/pulls" {
				t.Error("fetched review details", r.URL)
			}
			payload := asMap(rawFixture(1))
			payload["draft"] = true
			payload["mergeable"] = false
			payload["mergeable_state"] = "blocked"
			payload["statusCheckRollup"] = []any{map[string]any{"state": status}}
			respond(t, w, []any{payload})
		})
		policy := defaultPolicy()
		policy.Checks.Minimum = 2
		policy.Merge.RequireClean = true
		result, err := listCandidates(context.Background(), g, emptyPRResult(), policy, false)
		if err != nil || !result.Complete || len(result.PullRequests) != 1 || result.PullRequests[0].Status != SelectionCandidate || !result.PullRequests[0].Draft {
			t.Fatal(status, result, err)
		}
	}
}
