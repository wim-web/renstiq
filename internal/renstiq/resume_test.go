package renstiq

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

type reviewRoundTripper func(*http.Request) (*http.Response, error)

func (f reviewRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reviewAPI(t *testing.T, f *fakeAPI, e *Engine, intercept func(http.ResponseWriter, *http.Request) bool) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !intercept(w, r) {
			f.serve(w, r)
		}
	}))
	t.Cleanup(server.Close)
	e.GitHub.BaseURL = server.URL
	e.GitHub.HTTP = server.Client()
}

func reviewCommentDecision(r *Run) Decision {
	d := validDecision()
	d.ConfigDigest = r.ConfigDigest
	d.Decision = "hold"
	d.ReasonType = "human_review"
	d.Feedback.Comment = "Please review the dependency change."
	return d
}

func reloadReviewRun(t *testing.T, e *Engine, id string) *Run {
	t.Helper()
	b, err := os.ReadFile(e.Store.File)
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err = json.Unmarshal(b, &state); err != nil {
		t.Fatal(err)
	}
	e.Store.State = state
	r, err := e.Store.Find(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestFeedbackRetryStoresOneOperation(t *testing.T) {
	f, e, r := testEngine(t)
	var posts atomic.Int32
	reviewAPI(t, f, e, func(w http.ResponseWriter, req *http.Request) bool {
		if req.Method == "POST" && req.URL.Path == "/repos/o/r/issues/1/comments" {
			if posts.Add(1) == 1 {
				http.Error(w, `{"message":"forbidden"}`, http.StatusForbidden)
				return true
			}
		}
		return false
	})
	d := reviewCommentDecision(r)
	if _, err := e.Feedback(context.Background(), r, d); err == nil {
		t.Fatal("first request must fail")
	}
	if len(r.Operations) != 1 || r.Operations[0].Status != "failed" {
		t.Fatalf("first failure: %+v", r.Operations)
	}
	if _, err := e.Feedback(context.Background(), r, d); err != nil {
		t.Fatal(err)
	}
	r = reloadReviewRun(t, e, r.ID)
	if posts.Load() != 2 || len(r.Operations) != 1 || r.Operations[0].Status != "success" {
		t.Fatalf("retry must replace, not append: posts=%d operations=%+v", posts.Load(), r.Operations)
	}
	if _, err := e.Feedback(context.Background(), r, d); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 2 || len(r.Operations) != 1 || r.Operations[0].Status != "skipped" {
		t.Fatal("successful comment was posted again")
	}
}

func TestFeedbackRetryCrashDoesNotResend(t *testing.T) {
	f, e, r := testEngine(t)
	var posts atomic.Int32
	reviewAPI(t, f, e, func(w http.ResponseWriter, req *http.Request) bool {
		if req.Method != "POST" || req.URL.Path != "/repos/o/r/issues/1/comments" {
			return false
		}
		if posts.Add(1) == 1 {
			http.Error(w, `{"message":"forbidden"}`, http.StatusForbidden)
		} else {
			// The write was accepted, but the comment is not visible to reads yet.
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
		}
		return true
	})
	d := reviewCommentDecision(r)
	if _, err := e.Feedback(context.Background(), r, d); err == nil {
		t.Fatal("first request must fail")
	}
	transport := e.GitHub.HTTP.Transport
	crash := errors.New("simulated exit before response persistence")
	e.GitHub.HTTP.Transport = reviewRoundTripper(func(req *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(req)
		if err == nil && req.Method == "POST" && req.URL.Path == "/repos/o/r/issues/1/comments" {
			response.Body.Close()
			panic(crash)
		}
		return response, err
	})
	func() {
		defer func() {
			if got := recover(); got != crash {
				t.Fatalf("expected simulated exit, got %v", got)
			}
		}()
		_, _ = e.Feedback(context.Background(), r, d)
	}()
	e.GitHub.HTTP.Transport = transport
	r = reloadReviewRun(t, e, r.ID)
	if len(r.Operations) != 1 || r.Operations[0].Status != "pending" {
		t.Fatalf("retry intent was not stored uniquely: %+v", r.Operations)
	}
	if _, err := e.Feedback(context.Background(), r, d); err == nil {
		t.Fatal("unresolved retry must block another POST")
	}
	if posts.Load() != 2 || len(r.Operations) != 1 || r.Operations[0].Status != "unknown" {
		t.Fatalf("crashed request resent: posts=%d operations=%+v", posts.Load(), r.Operations)
	}
}

type reviewBranch struct {
	deletes atomic.Int32
	absent  atomic.Bool
}

func reviewBranchAPI(t *testing.T, f *fakeAPI, e *Engine, sha string, failDelete bool) *reviewBranch {
	t.Helper()
	branch := &reviewBranch{}
	reviewAPI(t, f, e, func(w http.ResponseWriter, req *http.Request) bool {
		switch {
		case req.Method == "GET" && req.URL.Path == "/repos/o/r/git/ref/heads/renovate/dep":
			if branch.absent.Load() {
				http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": sha}})
			}
			return true
		case req.Method == "DELETE" && req.URL.Path == "/repos/o/r/git/refs/heads/renovate/dep":
			branch.deletes.Add(1)
			if failDelete {
				http.Error(w, `{"message":"response lost"}`, http.StatusServiceUnavailable)
			} else {
				branch.absent.Store(true)
				w.WriteHeader(http.StatusNoContent)
			}
			return true
		}
		return false
	})
	return branch
}

func TestBranchCleanupOnEveryMergeEntryPoint(t *testing.T) {
	for _, entry := range []string{"fresh", "merged", "pending", "unknown", "post-merge", "finish"} {
		t.Run(entry, func(t *testing.T) {
			f, e, r := testEngine(t)
			r.Policy.Merge.DeleteBranch = true
			branch := reviewBranchAPI(t, f, e, f.head, false)
			if entry != "fresh" {
				f.merged = true
				installMerges(t, e, r, 1)
				if entry == "pending" || entry == "unknown" {
					r.Merges[0].Status = entry
					r.Merges[0].Commit = ""
				}
				if err := e.Store.Save(); err != nil {
					t.Fatal(err)
				}
				r = reloadReviewRun(t, e, r.ID)
			}
			var err error
			if entry == "post-merge" || entry == "finish" {
				err = e.PostMerge(context.Background(), r, entry == "finish")
			} else {
				_, err = e.Merge(context.Background(), r, validDecision())
			}
			if err != nil {
				t.Fatal(err)
			}
			if branch.deletes.Load() != 1 || r.op("delete_branch:1") == nil || r.op("delete_branch:1").Status != "success" {
				t.Fatalf("cleanup skipped: deletes=%d operations=%+v", branch.deletes.Load(), r.Operations)
			}
			wantMerges := 0
			if entry == "fresh" {
				wantMerges = 1
			}
			if f.writes != wantMerges {
				t.Fatalf("unexpected merge writes: %d", f.writes)
			}
			if err = e.PostMerge(context.Background(), r, true); err != nil {
				t.Fatal(err)
			}
			if err = e.PostMerge(context.Background(), r, true); err != nil {
				t.Fatal(err)
			}
			if branch.deletes.Load() != 1 || r.Phase != "finished" {
				t.Fatal("cleanup repeated or run not finished")
			}
		})
	}
}

func TestUnresolvedBranchDeletionBlocksFinishWithoutResend(t *testing.T) {
	for _, status := range []string{"pending", "unknown", "failed"} {
		t.Run(status, func(t *testing.T) {
			f, e, r := testEngine(t)
			r.Policy.Merge.DeleteBranch = true
			f.merged = true
			installMerges(t, e, r, 1)
			r.Operations = []Operation{{ID: "delete_branch:1", Kind: "delete_branch", PR: 1, Status: status}}
			branch := reviewBranchAPI(t, f, e, f.head, false)
			if err := r.blocked(); err == nil {
				t.Fatal("unresolved deletion not considered a blocker")
			}
			if err := e.PostMerge(context.Background(), r, true); err == nil || r.Phase == "finished" {
				t.Fatal("finished with unresolved cleanup")
			}
			if branch.deletes.Load() != 0 {
				t.Fatal("uncertain DELETE was resent")
			}
			branch.absent.Store(true)
			if err := e.PostMerge(context.Background(), r, true); err != nil {
				t.Fatal(err)
			}
			if r.Phase != "finished" || branch.deletes.Load() != 0 || r.op("delete_branch:1").Status != "skipped" {
				t.Fatal("missing branch was not reconciled without another DELETE")
			}
		})
	}
}

func TestBranchCleanupFailureIsNotRetriedOnResume(t *testing.T) {
	f, e, r := testEngine(t)
	r.Policy.Merge.DeleteBranch = true
	f.merged = true
	installMerges(t, e, r, 1)
	branch := reviewBranchAPI(t, f, e, f.head, true)
	if err := e.PostMerge(context.Background(), r, true); err == nil {
		t.Fatal("failed DELETE accepted")
	}
	r = reloadReviewRun(t, e, r.ID)
	if _, err := e.Merge(context.Background(), r, validDecision()); err == nil {
		t.Fatal("resume accepted unresolved DELETE")
	}
	if branch.deletes.Load() != 1 || r.Phase == "finished" {
		t.Fatal("DELETE was repeated or unfinished cleanup was ignored")
	}
	branch.absent.Store(true)
	if err := e.PostMerge(context.Background(), r, true); err != nil {
		t.Fatal(err)
	}
	if branch.deletes.Load() != 1 || r.Phase != "finished" {
		t.Fatal("failed cleanup could not be reconciled")
	}
}

func TestResumedBranchCleanupRetainsChangedHead(t *testing.T) {
	f, e, r := testEngine(t)
	r.Policy.Merge.DeleteBranch = true
	f.merged = true
	installMerges(t, e, r, 1)
	branch := reviewBranchAPI(t, f, e, strings.Repeat("d", 40), false)
	if err := e.PostMerge(context.Background(), r, true); err == nil || !strings.Contains(err.Error(), "branch head changed") {
		t.Fatalf("changed head not protected: %v", err)
	}
	if branch.deletes.Load() != 0 || r.Phase == "finished" {
		t.Fatal("changed branch deleted or run marked finished")
	}
}

func TestAbandonedRunDoesNotStartBranchCleanup(t *testing.T) {
	f, e, r := testEngine(t)
	r.Policy.Merge.DeleteBranch = true
	f.merged = true
	installMerges(t, e, r, 1)
	branch := reviewBranchAPI(t, f, e, f.head, false)
	if err := e.Store.Abandon(r.ID, "operator will retain the branch"); err != nil {
		t.Fatal(err)
	}
	if err := e.PostMerge(context.Background(), r, true); err != nil {
		t.Fatal(err)
	}
	if branch.deletes.Load() != 0 {
		t.Fatal("cleanup started after explicit abandonment")
	}
}
