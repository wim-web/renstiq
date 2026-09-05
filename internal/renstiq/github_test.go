package renstiq

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeAPI struct {
	head, base, conclusion string
	pending                int
	snapshots              int
	reads                  int
	readFailures           int
	writes                 int
	merged                 bool
	mergeUnknown           bool
	writeFailure           bool
	comments               []map[string]any
	labels                 []string
	review                 string
	wrongWorkflow          bool
}

func newFake(t *testing.T) (*fakeAPI, *GitHub) {
	t.Helper()
	f := &fakeAPI{head: strings.Repeat("a", 40), base: strings.Repeat("b", 40), conclusion: "success"}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	g := &GitHub{BaseURL: server.URL, HTTP: server.Client(), Token: "test", Retry: Retry{3, 0}, Poll: time.Millisecond, Log: io.Discard, Sleep: func(context.Context, time.Duration) error { return nil }}
	return f, g
}
func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	if r.Method == "GET" {
		f.reads++
		if f.readFailures > 0 {
			f.readFailures--
			http.Error(w, `{"message":"temporarily unavailable"}`, 503)
			return
		}
	}
	switch {
	case r.URL.Path == "/user":
		reply(map[string]string{"login": "operator"})
	case r.URL.Path == "/graphql":
		reply(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{"headRefOid": f.head, "baseRefOid": f.base, "reviewDecision": f.review, "mergeStateStatus": "CLEAN", "reviewThreads": map[string]any{"nodes": []any{}, "pageInfo": map[string]any{"hasNextPage": false}}}}}})
	case r.URL.Path == "/repos/o/r/pulls/1/merge":
		f.writes++
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["sha"] != f.head {
			http.Error(w, `{"message":"sha mismatch"}`, 409)
			return
		}
		if f.writeFailure {
			http.Error(w, `{"message":"unknown"}`, 503)
			return
		}
		f.merged = true
		if f.mergeUnknown {
			http.Error(w, `{"message":"response lost"}`, 503)
			return
		}
		reply(map[string]any{"merged": true, "sha": strings.Repeat("c", 40)})
	case r.URL.Path == "/repos/o/r/pulls/1":
		if r.Header.Get("Accept") == "application/vnd.github.diff" {
			fmt.Fprint(w, "diff --git a/go.mod b/go.mod\n-old\n+new\n")
			return
		}
		state := "open"
		if f.merged {
			state = "closed"
		}
		labels := []map[string]string{}
		for _, l := range f.labels {
			labels = append(labels, map[string]string{"name": l})
		}
		reply(map[string]any{"number": 1, "title": "Update dependency", "body": "release notes", "state": state, "merged": f.merged, "merge_commit_sha": strings.Repeat("c", 40), "mergeable": true, "user": map[string]string{"login": "renovate[bot]"}, "head": map[string]any{"sha": f.head, "ref": "renovate/dep", "repo": map[string]string{"full_name": "o/r"}}, "base": map[string]string{"sha": f.base, "ref": "main"}, "changed_files": 1, "commits": 1, "labels": labels})
	case r.URL.Path == "/repos/o/r/pulls":
		reply([]any{map[string]any{"number": 1, "user": map[string]string{"login": "renovate[bot]"}}})
	case strings.HasSuffix(r.URL.Path, "/files"):
		reply([]any{map[string]any{"filename": "go.mod", "status": "modified", "patch": "-old\n+new"}})
	case strings.HasSuffix(r.URL.Path, "/commits"):
		reply([]any{map[string]any{"author": map[string]string{"login": "renovate[bot]"}}})
	case strings.HasSuffix(r.URL.Path, "/check-runs"):
		f.snapshots++
		status := "completed"
		conclusion := f.conclusion
		if f.pending > 0 {
			f.pending--
			status = "in_progress"
			conclusion = ""
		}
		reply(map[string]any{"check_runs": []any{map[string]any{"name": "test", "status": status, "conclusion": conclusion, "app": map[string]any{"id": 1, "slug": "custom"}}}})
	case strings.HasSuffix(r.URL.Path, "/statuses"), strings.HasSuffix(r.URL.Path, "/reviews"), r.URL.Path == "/repos/o/r/pulls/1/comments":
		reply([]any{})
	case r.URL.Path == "/repos/o/r/issues/1/comments":
		if r.Method == "POST" {
			f.writes++
			var input map[string]string
			_ = json.NewDecoder(r.Body).Decode(&input)
			c := map[string]any{"id": len(f.comments) + 1, "body": input["body"], "html_url": "https://github.com/o/r/pull/1#comment", "user": map[string]string{"login": "operator"}}
			if !f.writeFailure {
				f.comments = append(f.comments, c)
			}
			if f.writeFailure || f.mergeUnknown {
				http.Error(w, `{"message":"response lost"}`, 503)
				return
			}
			reply(c)
		} else {
			if f.comments == nil {
				reply([]any{})
			} else {
				reply(f.comments)
			}
		}
	case r.URL.Path == "/repos/o/r/issues/1/labels" && r.Method == "POST":
		f.writes++
		if f.writeFailure {
			http.Error(w, `{"message":"failed"}`, 403)
			return
		}
		var input struct {
			Labels []string `json:"labels"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		f.labels = append(f.labels, input.Labels...)
		reply([]any{})
	case strings.HasPrefix(r.URL.Path, "/repos/o/r/issues/1/labels/") && r.Method == "DELETE":
		f.writes++
		f.labels = nil
		w.WriteHeader(204)
	default:
		http.Error(w, "unhandled "+r.Method+" "+r.URL.Path, 404)
	}
}
func TestReadRetryAndCIWait(t *testing.T) {
	f, g := newFake(t)
	f.readFailures = 2
	f.pending = 4
	p, e := g.waitPR(context.Background(), "o/r", 1, f.head, f.base, true)
	if e != nil {
		t.Fatal(e)
	}
	if f.snapshots != 5 || pending(p.Checks) || f.writes != 0 {
		t.Fatalf("bad wait: %+v", f)
	}
	f.readFailures = 4
	var raw rawPR
	if e = g.get(context.Background(), "/repos/o/r/pulls/1", &raw); e == nil {
		t.Fatal("exhausted retries accepted")
	}
	if f.readFailures != 1 {
		t.Fatal("wrong retry count")
	}
}
func TestHeadChangeWhileWaiting(t *testing.T) {
	f, g := newFake(t)
	f.pending = 1
	g.Sleep = func(context.Context, time.Duration) error { f.head = strings.Repeat("d", 40); return nil }
	_, e := g.waitPR(context.Background(), "o/r", 1, f.head, f.base, false)
	if e == nil || !strings.Contains(e.Error(), "review_required") {
		t.Fatal(e)
	}
	if f.writes != 0 {
		t.Fatal("mutated while waiting")
	}
}
func TestCIWaitCancellation(t *testing.T) {
	f, g := newFake(t)
	f.pending = 100
	ctx, cancel := context.WithCancel(context.Background())
	g.Sleep = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
	if _, e := g.waitPR(ctx, "o/r", 1, f.head, f.base, false); e == nil {
		t.Fatal("expected cancellation")
	}
}
func testEngine(t *testing.T) (*fakeAPI, *Engine, *Run) {
	t.Helper()
	f, g := newFake(t)
	s, e := openStore(t.TempDir(), "o/r")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	p := defaultPolicy()
	r, e := s.Current(p, digest(p))
	if e != nil {
		t.Fatal(e)
	}
	return f, &Engine{GitHub: g, Store: s, Repo: "o/r"}, r
}
func TestMergeUnknownReconciledOnce(t *testing.T) {
	f, e, r := testEngine(t)
	f.mergeUnknown = true
	d := validDecision()
	m, err := e.Merge(context.Background(), r, d)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "merged" || f.writes != 1 {
		t.Fatal(m, f.writes)
	}
	if _, err = e.Merge(context.Background(), r, d); err != nil {
		t.Fatal(err)
	}
	if f.writes != 1 {
		t.Fatal("duplicate merge")
	}
}
func TestUnresolvedWriteNotRetried(t *testing.T) {
	f, e, r := testEngine(t)
	f.writeFailure = true
	d := validDecision()
	if _, err := e.Merge(context.Background(), r, d); err == nil {
		t.Fatal("unknown merge succeeded")
	}
	if _, err := e.Merge(context.Background(), r, d); err == nil {
		t.Fatal("unresolved merge succeeded")
	}
	if f.writes != 1 {
		t.Fatalf("mutation retried %d times", f.writes)
	}
}
func TestMergeRejectsChangedChecksAndSHA(t *testing.T) {
	for _, kind := range []string{"head", "base", "checks", "review"} {
		t.Run(kind, func(t *testing.T) {
			f, e, r := testEngine(t)
			d := validDecision()
			switch kind {
			case "head":
				f.head = strings.Repeat("f", 40)
			case "base":
				f.base = strings.Repeat("f", 40)
			case "checks":
				f.conclusion = "failure"
			case "review":
				f.review = "CHANGES_REQUESTED"
			}
			if _, err := e.Merge(context.Background(), r, d); err == nil {
				t.Fatal("unsafe merge")
			}
			if f.writes != 0 {
				t.Fatal("merge attempted")
			}
		})
	}
}
func TestFeedbackDedupAndLabels(t *testing.T) {
	f, e, r := testEngine(t)
	d := validDecision()
	d.Decision = "hold"
	d.ReasonType = "compatibility"
	d.Review.Compatible = false
	d.Feedback.Add = []string{"renovate-needs-manual-review"}
	ops, err := e.Feedback(context.Background(), r, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 || len(f.comments) != 1 || len(f.labels) != 1 {
		t.Fatal(ops)
	}
	if _, err = e.Feedback(context.Background(), r, d); err != nil {
		t.Fatal(err)
	}
	if f.writes != 2 {
		t.Fatalf("duplicate feedback: %d", f.writes)
	}
	d.Decision = "merge"
	d.ReasonType = "resolved"
	d.Review.Compatible = true
	d.Feedback.Add = nil
	d.Feedback.Remove = []string{"renovate-needs-manual-review"}
	if _, err = e.Feedback(context.Background(), r, d); err != nil {
		t.Fatal(err)
	}
	if len(f.labels) != 0 {
		t.Fatal("label not removed")
	}
}
func TestCommentUnknownReconcileAndNoResend(t *testing.T) {
	for _, persist := range []bool{true, false} {
		t.Run(fmt.Sprint(persist), func(t *testing.T) {
			f, e, r := testEngine(t)
			f.mergeUnknown = true
			f.writeFailure = !persist
			d := validDecision()
			d.Decision = "hold"
			d.ReasonType = "compatibility"
			d.Review.Compatible = false
			_, err := e.Feedback(context.Background(), r, d)
			if persist && err != nil {
				t.Fatal(err)
			}
			if !persist && err == nil {
				t.Fatal("unknown success")
			}
			_, _ = e.Feedback(context.Background(), r, d)
			if f.writes != 1 {
				t.Fatal("comment resent")
			}
		})
	}
}
func TestFeedbackFailureSeparation(t *testing.T) {
	f, e, r := testEngine(t)
	f.writeFailure = true
	d := validDecision()
	d.Decision = "hold"
	d.ReasonType = "compatibility"
	d.Review.Compatible = false
	d.Feedback.Add = []string{"renovate-needs-manual-review"}
	ops, err := e.Feedback(context.Background(), r, d)
	if err == nil || len(ops) != 2 || ops[0].Kind != "comment" || ops[1].Kind != "label" {
		t.Fatal(ops, err)
	}
}

func TestHoldWithoutCommentIsRecorded(t *testing.T) {
	f, e, r := testEngine(t)
	d := validDecision()
	d.Decision = "hold"
	d.ReasonType = "checks"
	d.Reason = "completed CI failed"
	ops, err := e.Feedback(context.Background(), r, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 || f.writes != 0 || len(r.Decisions) != 1 {
		t.Fatal("hold was lost or unnecessarily posted")
	}
}
func TestPagesAndWorkflowRequirements(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		count := 100
		if r.URL.Query().Get("page") == "2" {
			count = 2
		}
		rows := make([]map[string]int, count)
		for i := range rows {
			rows[i] = map[string]int{"id": i}
		}
		_ = json.NewEncoder(w).Encode(rows)
	}))
	defer server.Close()
	g := &GitHub{BaseURL: server.URL, HTTP: server.Client(), Retry: Retry{1, 0}}
	a, err := pages[map[string]int](context.Background(), g, "/comments")
	if err != nil || len(a) != 102 || calls != 2 {
		t.Fatal(len(a), calls, err)
	}
}
