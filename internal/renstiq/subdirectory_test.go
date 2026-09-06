package renstiq

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func reviewSubdirectoryCheckout(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	local := filepath.Join(root, "local")
	if err := os.MkdirAll(filepath.Join(origin, "frontend"), 0755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, origin, "init", "--initial-branch=main")
	mustGit(t, origin, "config", "user.name", "test")
	mustGit(t, origin, "config", "user.email", "test@example.com")
	writeFile(t, filepath.Join(origin, "frontend", "version.txt"), "before\n")
	mustGit(t, origin, "add", ".")
	mustGit(t, origin, "commit", "-m", "initial")
	mustGit(t, root, "clone", origin, local)
	mustGit(t, local, "remote", "set-url", "origin", "https://github.com/o/r.git")
	writeFile(t, filepath.Join(origin, "frontend", "version.txt"), "merged\n")
	mustGit(t, origin, "commit", "-am", "merged")
	commit := mustGit(t, origin, "rev-parse", "HEAD")

	// Only redirect the network fetch to a local remote. Repository discovery,
	// status checks, ancestry checks, and fast-forwarding all use real Git.
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err = os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(bin, "git")
	writeFile(t, wrapper, `#!/bin/sh
if [ "$3" = "fetch" ]; then
  exec "$RENSTIQ_TEST_GIT" -C "$2" fetch --no-tags "$RENSTIQ_TEST_ORIGIN" "$6"
fi
exec "$RENSTIQ_TEST_GIT" "$@"
`)
	if err = os.Chmod(wrapper, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RENSTIQ_TEST_GIT", realGit)
	t.Setenv("RENSTIQ_TEST_ORIGIN", origin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return local, commit
}

func TestPostMergeRunsInSynchronizedSubdirectory(t *testing.T) {
	for _, source := range []string{"action-relative", "action-absolute", "policy-relative", "policy-absolute"} {
		t.Run(source, func(t *testing.T) {
			local, commit := reviewSubdirectoryCheckout(t)
			_, e, r := testEngine(t)
			e.Executor.Dir = local
			dir := "frontend"
			if strings.HasSuffix(source, "absolute") {
				dir = filepath.Join(local, dir)
			}
			a := PostCommand{ID: "build", Timing: "after_each_merge", Command: []string{"sh", "-c", "pwd -P; cat version.txt"}}
			if strings.HasPrefix(source, "action") {
				a.WorkingDir = dir
			} else {
				r.Policy.WorkingDir = dir
			}
			r.Policy.PostMerge = []PostCommand{a}
			r.Merges = []MergeRecord{{PR: 1, Base: "main", Commit: commit, Status: "merged"}}
			if err := e.PostMerge(context.Background(), r, true); err != nil {
				t.Fatal(err)
			}
			op := r.op("post:1:build")
			if op == nil || op.Status != "success" {
				t.Fatalf("hook failed: %+v", op)
			}
			log, err := os.ReadFile(op.Log)
			if err != nil {
				t.Fatal(err)
			}
			wantDir, err := canonicalDir(filepath.Join(local, "frontend"))
			if err != nil {
				t.Fatal(err)
			}
			if string(log) != wantDir+"\nmerged\n" {
				t.Fatalf("hook ran in wrong directory or before synchronization: %q", log)
			}
			if head := mustGit(t, local, "rev-parse", "HEAD"); head != commit {
				t.Fatalf("checkout not synchronized: %s", head)
			}
			if _, err := repository(context.Background(), filepath.Join(local, "frontend")); err == nil {
				t.Fatal("configuration discovery must still require a repository root")
			}
		})
	}
}

func TestSubdirectorySynchronizationPreservesSafetyChecks(t *testing.T) {
	for _, mode := range []string{"different-repository", "dirty-outside-subdirectory"} {
		t.Run(mode, func(t *testing.T) {
			local, commit := reviewSubdirectoryCheckout(t)
			before := mustGit(t, local, "rev-parse", "HEAD")
			repo := "o/r"
			wantError := "working tree has local changes"
			if mode == "different-repository" {
				repo = "other/repo"
				wantError = "belongs to another repository"
			} else {
				writeFile(t, filepath.Join(local, "outside.txt"), "keep")
			}
			err := synchronize(context.Background(), filepath.Join(local, "frontend"), repo, []MergeRecord{{Base: "main", Commit: commit}})
			if err == nil || !strings.Contains(err.Error(), wantError) {
				t.Fatalf("safety check not preserved: %v", err)
			}
			if got := mustGit(t, local, "rev-parse", "HEAD"); got != before {
				t.Fatal("checkout changed despite synchronization failure")
			}
			if mode == "dirty-outside-subdirectory" {
				b, err := os.ReadFile(filepath.Join(local, "outside.txt"))
				if err != nil || string(b) != "keep" {
					t.Fatal("local changes were lost")
				}
			}
		})
	}
}
