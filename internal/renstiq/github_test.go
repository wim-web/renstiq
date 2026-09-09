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
	p.Labels = &[]struct {
		Name string `json:"name"`
	}{}
	p.Body = "| Package | Update | Change |\n| --- | --- | --- |\n| example | patch | 1.0.0 → 1.0.1 |\n"
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
	return &GitHub{BaseURL: server.URL, Token: "test", HTTP: server.Client(), ReadRetry: GitHubAPIReadRetry{MaxAttempts: 1}, Sleep: func(context.Context, time.Duration) error { t.Error("unexpected waiting"); return nil }}
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
	p := testPolicy()
	p.PullRequests.Filters[0].Authors = append(p.PullRequests.Filters[0].Authors, "human")
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
	return PRListResult{Version: configVersion, Repo: "o/r", Path: "/repo", PullRequests: []PRListItem{}, Errors: []ReadError{}}
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
		result, err := listCandidates(context.Background(), g, emptyPRResult(), testPolicy(), false)
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
	result, err := listCandidates(context.Background(), g, emptyPRResult(), testPolicy(), false)
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
			result, err := listCandidates(context.Background(), g, emptyPRResult(), testPolicy(), false)
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
			policy := testPolicy()
			policy.PullRequests.Filters[0].Files = []string{"**"}
			policy.PullRequests.Filters[0].Types = []string{"patch"}
			policy.PullRequests.Filters[0].CommitAuthors = []string{"renovate[bot]"}
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
		facts, err := g.CandidateDetails(context.Background(), "o/r", rawFixture(1).info(), files, !files)
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
	g.ReadRetry = GitHubAPIReadRetry{MaxAttempts: 3}
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
	g.ReadRetry.MaxAttempts = 2
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
		policy := testPolicy()
		result, err := listCandidates(context.Background(), g, emptyPRResult(), policy, false)
		if err != nil || !result.Complete || len(result.PullRequests) != 1 || result.PullRequests[0].Status != SelectionCandidate || !result.PullRequests[0].Draft {
			t.Fatal(status, result, err)
		}
	}
}

func TestV2ListFiltersUpdatesAndLockBeforeAIReview(t *testing.T) {
	rows := []rawPR{}
	for n := 1; n <= 5; n++ {
		rows = append(rows, rawFixture(n))
	}
	rows[0].Body = "| Package | Update |\n|---|---|\n| example | minor |"
	rows[1].Body = "| Package | Update |\n|---|---|\n| example | patch |\n| other | major |"
	rows[2].Labels = &[]struct {
		Name string `json:"name"`
	}{{Name: "renstiq-locked"}}
	rows[3].Body = "| Package | Change |\n|---|---|\n| example | 1.0.0 → 1.0.1 |"
	rows[4].Title = "Major update (title is not classification data)"
	g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/pulls" {
			t.Error("unnecessary request", r.URL)
			http.Error(w, "unexpected", 500)
			return
		}
		respond(t, w, rows)
	})
	policy := testPolicy()
	policy.PullRequests.Filters[0].Types = []string{"patch", "minor"}
	policy.Review = []Instruction{{Entry: Entry{ID: "all", Enabled: true}, Instructions: "review"}, {Entry: Entry{ID: "dep", Enabled: true}, Match: Match{Dependencies: []string{"example"}}, Instructions: "extra review"}}
	for _, all := range []bool{false, true} {
		result, err := listCandidates(context.Background(), g, emptyPRResult(), policy, all)
		if err == nil || result.Complete || len(result.Errors) != 1 || result.Errors[0].PR != 4 {
			t.Fatal(result, err)
		}
		if result.OpenRenovateCount == nil || *result.OpenRenovateCount != 5 {
			t.Fatal(result)
		}
		if all {
			want := []SelectionStatus{SelectionCandidate, SelectionExcluded, SelectionExcluded, SelectionUnknown, SelectionCandidate}
			if len(result.PullRequests) != len(want) {
				t.Fatal(result)
			}
			for i, item := range result.PullRequests {
				if item.Status != want[i] {
					t.Fatal(item, want[i])
				}
			}
		} else {
			if len(result.PullRequests) != 2 || result.PullRequests[0].Number != 1 || result.PullRequests[1].Number != 5 {
				t.Fatal(result)
			}
			for _, item := range result.PullRequests {
				if len(item.ReviewIDs) != 2 {
					t.Fatal(item)
				}
			}
		}
		if err := validateSchema("pr-list", asMap(result)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestLabelAndBodyChangesInvalidateSelectionSnapshot(t *testing.T) {
	for _, field := range []string{"labels", "body", "draft"} {
		t.Run(field, func(t *testing.T) {
			initial := rawFixture(1)
			reads := 0
			g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/repos/o/r/pulls/1/files" {
					respond(t, w, []ChangedFile{{Filename: "go.mod", Status: "modified"}})
					return
				}
				reads++
				current := initial
				if reads == 2 {
					switch field {
					case "labels":
						current.Labels = &[]struct {
							Name string `json:"name"`
						}{{Name: "renstiq-locked"}}
					case "body":
						current.Body += "\nchanged"
					case "draft":
						current.Draft = true
					}
				}
				respond(t, w, current)
			})
			facts, err := g.CandidateDetails(context.Background(), "o/r", initial.info(), true, false)
			if err == nil || !facts.Changed || SelectCandidate(testPolicy(), facts).Status != SelectionUnknown {
				t.Fatal(facts, err)
			}
		})
	}
}

func TestAlternativeFiltersHandleMissingUpdateColumn(t *testing.T) {
	rows := []rawPR{}
	for n := 1; n <= 5; n++ {
		rows = append(rows, rawFixture(n))
	}
	for _, i := range []int{0, 1} {
		rows[i].Body = "| Package | Change |\n|---|---|\n| example | 1.0.0 → 1.1.0 |"
	}
	for _, i := range []int{3, 4} {
		rows[i].Body = "| Package | Update |\n|---|---|\n| example | major |"
	}
	fileCalls := map[int]int{}
	g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r/pulls" {
			respond(t, w, rows)
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/repos/o/r/pulls/"), "/")
		n, err := strconv.Atoi(parts[0])
		if err != nil || n < 1 || n > len(rows) {
			t.Fatal("unexpected request", r.URL)
		}
		if len(parts) == 1 {
			respond(t, w, rows[n-1])
		} else if len(parts) == 2 && parts[1] == "files" {
			fileCalls[n]++
			file := "go.mod"
			if n == 2 || n == 5 {
				file = "aqua.yaml"
			}
			respond(t, w, []ChangedFile{{Filename: file, Status: "modified"}})
		} else {
			t.Fatal("unexpected request", r.URL)
		}
	})
	policy := testPolicy()
	policy.PullRequests.Filters = []Filter{
		{Entry: Entry{ID: "updates", Enabled: true}, Types: []string{"patch", "minor"}},
		{Entry: Entry{ID: "go", Enabled: true}, Files: []string{"go.mod", "go.sum"}},
	}
	policy.Review = []Instruction{{Entry: Entry{ID: "all", Enabled: true}, Instructions: "review"}}
	result, err := listCandidates(context.Background(), g, emptyPRResult(), policy, true)
	if err == nil || result.Complete || len(result.Errors) != 1 || result.Errors[0].PR != 2 || *result.OpenRenovateCount != 5 {
		t.Fatal(result, err)
	}
	want := []SelectionStatus{SelectionCandidate, SelectionUnknown, SelectionCandidate, SelectionCandidate, SelectionExcluded}
	if len(result.PullRequests) != len(want) {
		t.Fatal(result)
	}
	for i, pr := range result.PullRequests {
		if pr.Status != want[i] {
			t.Fatal(pr, want[i])
		}
		if pr.Status == SelectionCandidate && (len(pr.ReviewIDs) != 1 || pr.ReviewIDs[0] != "all" || len(pr.Reasons) != 0) {
			t.Fatal(pr)
		}
		wantCalls := 1
		if pr.Number == 3 {
			wantCalls = 0 // The update filter already matched; the file filter needs no evaluation.
		}
		if fileCalls[pr.Number] != wantCalls {
			t.Fatal("unnecessary or missing file request", pr.Number, fileCalls)
		}
	}
	if result.PullRequests[0].UpdatesComplete {
		t.Fatal("invented update metadata", result.PullRequests[0])
	}
}

func TestAlternativeFiltersPreserveSuccessfulDetailReads(t *testing.T) {
	for _, kind := range []string{"files fail", "commits fail", "snapshot fails", "file count changed", "commit count changed", "head changed"} {
		t.Run(kind, func(t *testing.T) {
			rawCalls := 0
			g := readGitHub(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/o/r/pulls":
					respond(t, w, []rawPR{rawFixture(1)})
				case "/repos/o/r/pulls/1":
					rawCalls++
					if rawCalls == 2 && kind == "snapshot fails" {
						http.Error(w, `{"message":"snapshot unavailable"}`, 503)
						return
					}
					current := rawFixture(1)
					if rawCalls == 2 {
						switch kind {
						case "file count changed":
							current.ChangedFiles = ptr(2)
						case "commit count changed":
							current.Commits = ptr(2)
						case "head changed":
							current.Head.SHA = "updated"
						}
					}
					respond(t, w, current)
				case "/repos/o/r/pulls/1/files":
					if kind == "files fail" {
						http.Error(w, `{"message":"files unavailable"}`, 503)
						return
					}
					respond(t, w, []ChangedFile{{Filename: "go.mod", Status: "modified"}})
				case "/repos/o/r/pulls/1/commits":
					if kind == "commits fail" {
						http.Error(w, `{"message":"commits unavailable"}`, 503)
						return
					}
					respond(t, w, []map[string]any{{"sha": "head", "author": map[string]any{"login": "renovate[bot]"}}})
				default:
					t.Error("unexpected endpoint", r.URL)
				}
			})
			policy := testPolicy()
			policy.PullRequests.Filters = []Filter{
				{Entry: Entry{ID: "files", Enabled: true}, Files: []string{"go.mod"}},
				{Entry: Entry{ID: "commits", Enabled: true}, CommitAuthors: []string{"renovate[bot]"}},
			}
			result, err := listCandidates(context.Background(), g, emptyPRResult(), policy, true)
			want := SelectionCandidate
			if kind != "files fail" && kind != "commits fail" {
				want = SelectionUnknown
			}
			if err == nil || result.Complete || len(result.Errors) != 1 || result.Errors[0].Stage != "details" || len(result.PullRequests) != 1 || result.PullRequests[0].Status != want {
				t.Fatal(result, err)
			}
		})
	}
}
