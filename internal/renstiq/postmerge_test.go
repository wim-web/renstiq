package renstiq

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func installMerges(t *testing.T, e *Engine, r *Run, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		r.Merges = append(r.Merges, MergeRecord{PR: i, HeadSHA: strings.Repeat("a", 40), Commit: strings.Repeat("c", 40), Base: "main", Files: []string{"go.mod"}, Decision: validDecision(), Status: "merged"})
	}
	if err := e.Store.Save(); err != nil {
		t.Fatal(err)
	}
}
func TestPostMergeTimingsAndNoDuplicate(t *testing.T) {
	_, e, r := testEngine(t)
	dir := t.TempDir()
	e.Dir = dir
	e.Sync = func(context.Context, string, string, []MergeRecord) error { return nil }
	script := filepath.Join(dir, "hook.sh")
	writeFile(t, script, "#!/bin/sh\ncat >> input.jsonl\nprintf '\\n' >> input.jsonl\necho child-output\n")
	r.Policy.PostMerge = []PostCommand{{ID: "each", Timing: "after_each_merge", Command: []string{"sh", script}}, {ID: "repo", Timing: "after_repo", Command: []string{"sh", script}}}
	installMerges(t, e, r, 2)
	if err := e.PostMerge(context.Background(), r, false); err != nil {
		t.Fatal(err)
	}
	if len(r.Operations) != 2 {
		t.Fatal(r.Operations)
	}
	if err := e.PostMerge(context.Background(), r, true); err != nil {
		t.Fatal(err)
	}
	if r.Phase != "finished" || len(r.Operations) != 3 {
		t.Fatal(r)
	}
	if err := e.PostMerge(context.Background(), r, true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "input.jsonl"))
	if strings.Count(string(b), "\n") != 3 {
		t.Fatal("post commands duplicated")
	}
	if !strings.Contains(string(b), `"timing":"after_repo"`) {
		t.Fatal("stdin missing")
	}
	d := validDecision()
	d.PR = 3
	if _, err := e.Merge(context.Background(), r, d); err == nil {
		t.Fatal("additional merge after finalization accepted")
	}
}
func TestPostFailureAndUnknownNeverRetry(t *testing.T) {
	for _, mode := range []string{"failed", "sync_failed", "running", "pending"} {
		t.Run(mode, func(t *testing.T) {
			_, e, r := testEngine(t)
			dir := t.TempDir()
			e.Dir = dir
			calls := 0
			e.Sync = func(context.Context, string, string, []MergeRecord) error {
				calls++
				if mode == "sync_failed" {
					return errors.New("dirty")
				}
				return nil
			}
			r.Policy.PostMerge = []PostCommand{{ID: "fail", Timing: "after_each_merge", Command: []string{"sh", "-c", "echo ran >> count; exit 9"}}, {ID: "later", Timing: "after_each_merge", Command: []string{"sh", "-c", "echo bad >> later"}}}
			installMerges(t, e, r, 1)
			if mode == "running" || mode == "pending" {
				r.Operations = append(r.Operations, Operation{ID: "post:1:fail", Kind: "post_merge", Status: mode})
			}
			if err := e.PostMerge(context.Background(), r, true); err == nil {
				t.Fatal("failure accepted")
			}
			if err := e.PostMerge(context.Background(), r, true); err == nil {
				t.Fatal("failure accepted on restart")
			}
			if calls > 1 {
				t.Fatal("retried")
			}
			if _, err := os.Stat(filepath.Join(dir, "later")); !os.IsNotExist(err) {
				t.Fatal("dependent action ran")
			}
			if mode == "failed" {
				b, _ := os.ReadFile(filepath.Join(dir, "count"))
				if string(b) != "ran\n" {
					t.Fatal("failed command retried")
				}
			}
		})
	}
}
func TestNoMergeNoCommandsAndReviewSelection(t *testing.T) {
	_, e, r := testEngine(t)
	e.Sync = func(context.Context, string, string, []MergeRecord) error {
		t.Fatal("unexpected synchronization")
		return nil
	}
	r.Policy.PostMerge = []PostCommand{{ID: "release", Timing: "after_repo", RequiresReview: true, Command: []string{"false"}}}
	if err := e.PostMerge(context.Background(), r, true); err != nil {
		t.Fatal(err)
	}
	if len(r.Operations) != 0 {
		t.Fatal(r.Operations)
	}
	m := MergeRecord{Files: []string{"go.mod"}, Decision: validDecision()}
	a := PostCommand{ID: "release", RequiresReview: true, Match: Match{Files: []string{"go.*"}}}
	if selected(a, m) {
		t.Fatal("missing decision selected")
	}
	m.Decision.PostMerge = []PostChoice{{ID: "release", Needed: true, Reason: "binary dependency"}}
	if !selected(a, m) {
		t.Fatal("needed release skipped")
	}
}
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	s, e := git(context.Background(), dir, args...)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestSynchronizeCheckoutPreservesLocalChanges(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	local := filepath.Join(root, "local")
	if err := os.MkdirAll(origin, 0755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, origin, "init", "--initial-branch=main")
	mustGit(t, origin, "config", "user.name", "test")
	mustGit(t, origin, "config", "user.email", "test@example.com")
	writeFile(t, filepath.Join(origin, "file"), "before")
	mustGit(t, origin, "add", "file")
	mustGit(t, origin, "commit", "-m", "initial")
	mustGit(t, root, "clone", origin, local)
	writeFile(t, filepath.Join(origin, "file"), "merged")
	mustGit(t, origin, "commit", "-am", "merged")
	commit := mustGit(t, origin, "rev-parse", "HEAD")
	merges := []MergeRecord{{Base: "main", Commit: commit}}
	writeFile(t, filepath.Join(local, "untracked"), "keep")
	if err := synchronizeCheckout(context.Background(), local, merges); err == nil {
		t.Fatal("dirty checkout accepted")
	}
	if err := os.Remove(filepath.Join(local, "untracked")); err != nil {
		t.Fatal(err)
	}
	if err := synchronizeCheckout(context.Background(), local, merges); err != nil {
		t.Fatal(err)
	}
	if got := mustGit(t, local, "rev-parse", "HEAD"); got != commit {
		t.Fatal(got)
	}
	mustGit(t, local, "config", "user.name", "test")
	mustGit(t, local, "config", "user.email", "test@example.com")
	writeFile(t, filepath.Join(local, "extra"), "local")
	mustGit(t, local, "add", "extra")
	mustGit(t, local, "commit", "-m", "local")
	head := mustGit(t, local, "rev-parse", "HEAD")
	if err := synchronizeCheckout(context.Background(), local, merges); err == nil {
		t.Fatal("ahead branch accepted")
	}
	if got := mustGit(t, local, "rev-parse", "HEAD"); got != head {
		t.Fatal("local commit lost")
	}
}
func TestStoreLockAndCrashPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(dir, "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if other, err := openStore(dir, "o/r"); err == nil {
		other.Close()
		t.Fatal("duplicate lock acquired")
	}
	p := defaultPolicy()
	r, err := s.Current(p, digest(p))
	if err != nil {
		t.Fatal(err)
	}
	r.Operations = append(r.Operations, Operation{ID: "post:1:a", Kind: "post_merge", Status: "running"})
	if err = s.Save(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = openStore(dir, "o/r")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err = s.Current(p, digest(p))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.blocked(); err == nil {
		t.Fatal("running action lost on restart")
	}
}

func TestExplicitAbandonKeepsFailureHistory(t *testing.T) {
	_, e, r := testEngine(t)
	id := r.ID
	r.Operations = append(r.Operations, Operation{ID: "failed", Kind: "post_merge", Status: "unknown"})
	if err := e.Store.Abandon(id, ""); err == nil {
		t.Fatal("empty reason accepted")
	}
	if err := e.Store.Abandon(id, "operator verified deployment separately"); err != nil {
		t.Fatal(err)
	}
	next, err := e.Store.Current(r.Policy, r.ConfigDigest)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == id || len(next.Operations) != 0 {
		t.Fatal("old command carried into new run")
	}
	old, _ := e.Store.Find(id)
	if old.Operations[0].Status != "unknown" {
		t.Fatal("failure changed to success")
	}
}
