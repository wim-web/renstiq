package renstiq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type PostInput struct {
	Version int           `json:"version"`
	Repo    string        `json:"repo"`
	Run     string        `json:"run"`
	Action  string        `json:"action"`
	Timing  string        `json:"timing"`
	Merges  []MergeRecord `json:"merges"`
}

func (e *Engine) PostMerge(ctx context.Context, r *Run, finish bool) error {
	for i := range r.Merges {
		m := &r.Merges[i]
		if m.Status == "pending" || m.Status == "unknown" {
			if err := e.reconcileMerge(ctx, r, m); err != nil {
				return err
			}
		}
	}
	if r.Phase == "finished" {
		return r.blocked()
	}
	// Every entry point (merge, resume, and finish) must complete branch cleanup.
	// deleteBranch reconciles uncertain requests without resending a DELETE.
	if r.Policy.Merge.DeleteBranch {
		for i := range r.Merges {
			m := &r.Merges[i]
			if m.Status == "merged" {
				if err := e.deleteBranch(ctx, r, m); err != nil {
					return err
				}
			}
		}
	}
	if err := r.blocked(); err != nil {
		return err
	}
	for _, m := range r.Merges {
		if m.Status != "merged" {
			continue
		}
		for _, a := range r.Policy.PostMerge {
			if a.Timing != "after_each_merge" || !selected(a, m) {
				continue
			}
			id := fmt.Sprintf("post:%d:%s", m.PR, a.ID)
			if err := e.executePost(ctx, r, a, id, []MergeRecord{m}); err != nil {
				return err
			}
		}
	}
	if !finish {
		return nil
	}
	r.Phase = "finalizing"
	if err := e.Store.Save(); err != nil {
		return err
	}
	for _, a := range r.Policy.PostMerge {
		if a.Timing != "after_repo" {
			continue
		}
		merges := []MergeRecord{}
		for _, m := range r.Merges {
			if m.Status == "merged" && selected(a, m) {
				merges = append(merges, m)
			}
		}
		if len(merges) == 0 {
			continue
		}
		if err := e.executePost(ctx, r, a, "post:repo:"+a.ID, merges); err != nil {
			return err
		}
	}
	r.Phase = "finished"
	return e.Store.Save()
}
func (e *Engine) executePost(ctx context.Context, r *Run, a PostCommand, id string, merges []MergeRecord) error {
	if old := r.op(id); old != nil {
		if old.Status == "success" {
			return nil
		}
		return fmt.Errorf("%s is %s; command will not be retried", id, old.Status)
	}
	dir := a.WorkingDir
	if dir == "" {
		dir = r.Policy.WorkingDir
	}
	if dir == "" {
		dir = e.Dir
	}
	dir = expandHome(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(e.Dir, dir)
	}
	op := Operation{ID: id, Kind: "post_merge", Status: "pending", Started: now()}
	upsertOperation(r, op)
	if err := e.Store.Save(); err != nil {
		return err
	}
	finish := func(status string, err error) error {
		op.Status = status
		op.Finished = now()
		if err != nil {
			op.Error = err.Error()
		}
		upsertOperation(r, op)
		if saveErr := e.Store.Save(); saveErr != nil {
			return saveErr
		}
		return err
	}
	syncFn := e.Sync
	if syncFn == nil {
		syncFn = synchronize
	}
	if err := syncFn(ctx, dir, e.Repo, merges); err != nil {
		return finish("sync_failed", err)
	}
	logDir := strings.TrimSuffix(e.Store.File, ".json") + "-logs"
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return finish("failed", err)
	}
	op.Log = filepath.Join(logDir, r.ID+"-"+digest(id)+".log")
	log, err := os.OpenFile(op.Log, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return finish("failed", err)
	}
	defer log.Close()
	input := PostInput{Version: 1, Repo: e.Repo, Run: r.ID, Action: a.ID, Timing: a.Timing, Merges: merges}
	payload, err := json.Marshal(input)
	if err != nil {
		return finish("failed", err)
	}
	// Persist the running intent before starting the child. A crash now is unknown, never retryable.
	op.Status = "running"
	upsertOperation(r, op)
	if err = e.Store.Save(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, a.Command[0], a.Command[1:]...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(string(payload))
	output := io.Writer(log)
	if e.GitHub.Log != nil {
		output = io.MultiWriter(log, e.GitHub.Log)
	}
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()
	code := 0
	if err != nil {
		code = -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
	}
	op.ExitCode = &code
	if syncErr := log.Sync(); syncErr != nil {
		return finish("unknown", syncErr)
	}
	if ctx.Err() != nil {
		return finish("unknown", ctx.Err())
	}
	if err != nil {
		return finish("failed", fmt.Errorf("command %s: %w", a.ID, err))
	}
	return finish("success", nil)
}
func synchronize(ctx context.Context, dir, repo string, merges []MergeRecord) error {
	if len(merges) == 0 {
		return errors.New("cannot synchronize without a merged PR")
	}
	// working_dir is the command's directory, not necessarily the checkout root.
	// Keep repository/config discovery strict while synchronizing the whole tree.
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	name, err := repository(ctx, root)
	if err != nil {
		return err
	}
	if name != repo {
		return errors.New("post-merge working directory belongs to another repository")
	}
	return synchronizeCheckout(ctx, root, merges)
}

func synchronizeCheckout(ctx context.Context, dir string, merges []MergeRecord) error {
	branch := merges[len(merges)-1].Base
	for _, m := range merges {
		if m.Base != branch {
			return errors.New("cannot synchronize merges for different base branches in one action")
		}
	}
	status, err := git(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return errors.New("working tree has local changes; synchronization stopped")
	}
	current, err := git(ctx, dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return err
	}
	if current != branch {
		return fmt.Errorf("working directory is on %s; expected %s", current, branch)
	}
	if _, err = git(ctx, dir, "fetch", "--no-tags", "origin", "refs/heads/"+branch); err != nil {
		return err
	}
	target, err := git(ctx, dir, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return err
	}
	if _, err = git(ctx, dir, "merge-base", "--is-ancestor", "HEAD", target); err != nil {
		return errors.New("local branch has diverged or is ahead; refusing synchronization")
	}
	for _, m := range merges {
		if m.Commit == "" {
			return errors.New("merged commit is unknown")
		}
		if _, err = git(ctx, dir, "merge-base", "--is-ancestor", m.Commit, target); err != nil {
			return fmt.Errorf("merged commit %s is not in fetched base", m.Commit)
		}
	}
	if _, err = git(ctx, dir, "merge", "--ff-only", target); err != nil {
		return err
	}
	head, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != target {
		return errors.New("working directory did not reach fetched base")
	}
	status, err = git(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return errors.New("working tree changed during synchronization")
	}
	return nil
}
