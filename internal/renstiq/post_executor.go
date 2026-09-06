package renstiq

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

type CommandSpec struct {
	Args   []string
	Dir    string
	Input  []byte
	Output io.Writer
}

type CommandRunner interface {
	Run(context.Context, CommandSpec) (exitCode int, err error)
}

type OperationLog interface {
	io.WriteCloser
	Sync() error
	Path() string
}

type LogStore interface {
	Open(runID, operationID string) (OperationLog, error)
}

type PostExecutor struct {
	Repo, Dir, Home string
	Journal         Journal
	Sync            func(context.Context, string, string, []MergeRecord) error
	Runner          CommandRunner
	Logs            LogStore
	Output          io.Writer
	Now             func() time.Time
}

func (s PostExecutor) Execute(ctx context.Context, r *Run, a PostCommand, id string, merges []MergeRecord) error {
	if old := r.op(id); old != nil {
		if old.Status == "success" {
			return nil
		}
		return fmt.Errorf("%s is %s; command will not be retried", id, old.Status)
	}
	dir := postWorkingDir(s.Dir, r.Policy.WorkingDir, a.WorkingDir, s.Home)
	op := Operation{ID: id, Kind: "post_merge", Status: "pending", Started: timestamp(s.Now())}
	if err := saveOperation(s.Journal, r, op); err != nil {
		return err
	}
	if err := s.Sync(ctx, dir, s.Repo, merges); err != nil {
		return s.finish(r, &op, "sync_failed", err)
	}
	log, err := s.Logs.Open(r.ID, id)
	if err != nil {
		return s.finish(r, &op, "failed", err)
	}
	defer log.Close()
	op.Log = log.Path()
	input := PostInput{Version: 1, Repo: s.Repo, Run: r.ID, Action: a.ID, Timing: a.Timing, Merges: merges}
	payload, err := json.Marshal(input)
	if err != nil {
		return s.finish(r, &op, "failed", err)
	}
	// A crash after durable running intent is unknown: never repeat the child.
	op.Status = "running"
	if err := saveOperation(s.Journal, r, op); err != nil {
		return err
	}
	output := io.Writer(log)
	if s.Output != nil {
		output = io.MultiWriter(log, s.Output)
	}
	code, runErr := s.Runner.Run(ctx, CommandSpec{Args: a.Command, Dir: dir, Input: payload, Output: output})
	op.ExitCode = &code
	if err := log.Sync(); err != nil {
		return s.finish(r, &op, "unknown", err)
	}
	if err := ctx.Err(); err != nil {
		return s.finish(r, &op, "unknown", err)
	}
	if runErr != nil {
		return s.finish(r, &op, "failed", fmt.Errorf("command %s: %w", a.ID, runErr))
	}
	return s.finish(r, &op, "success", nil)
}

func (s PostExecutor) finish(r *Run, op *Operation, status string, err error) error {
	op.Status, op.Finished = status, timestamp(s.Now())
	if err != nil {
		op.Error = err.Error()
	}
	if saveErr := saveOperation(s.Journal, r, *op); saveErr != nil {
		return saveErr
	}
	return err
}

// Directory precedence is a pure value transformation; home lookup is external.
func postWorkingDir(root, policy, action, home string) string {
	dir := action
	if dir == "" {
		dir = policy
	}
	if dir == "" {
		dir = root
	}
	if strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(home, dir[2:])
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return dir
}
