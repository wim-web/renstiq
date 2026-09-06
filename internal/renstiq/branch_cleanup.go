package renstiq

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type BranchGateway interface {
	PRStateReader
	BranchHead(context.Context, string, string) (sha string, exists bool, err error)
	DeleteBranch(context.Context, string, string) error
}

type BranchCleaner struct {
	Repo    string
	Remote  BranchGateway
	Journal Journal
	Now     func() time.Time
}

func (s BranchCleaner) Cleanup(ctx context.Context, r *Run, m *MergeRecord) error {
	id := fmt.Sprintf("delete_branch:%d", m.PR)
	old := r.op(id)
	if old != nil && (old.Status == "success" || old.Status == "skipped") {
		return nil
	}
	state, err := s.Remote.PullRequestState(ctx, s.Repo, m.PR)
	if err != nil {
		return err
	}
	if state.HeadRepo != s.Repo || state.HeadBranch == state.BaseBranch {
		return errors.New("refusing to delete a fork/base branch")
	}
	// Remember prior intent before replacing its record; do not retry any earlier DELETE.
	previous := old != nil
	op := Operation{ID: id, Kind: "delete_branch", PR: m.PR, Status: "pending", Started: timestamp(s.Now())}
	if err := saveOperation(s.Journal, r, op); err != nil {
		return err
	}
	s.remove(ctx, state.HeadBranch, m.HeadSHA, previous, &op)
	op.Finished = timestamp(s.Now())
	if err := saveOperation(s.Journal, r, op); err != nil {
		return err
	}
	if op.Status == "failed" || op.Status == "unknown" {
		return errors.New(op.Error)
	}
	return nil
}

func (s BranchCleaner) remove(ctx context.Context, branch, expected string, previous bool, op *Operation) {
	head, exists, err := s.Remote.BranchHead(ctx, s.Repo, branch)
	switch {
	case err != nil:
		op.Status, op.Error = "failed", err.Error()
	case !exists:
		op.Status = "skipped"
	case head != expected:
		op.Status, op.Error = "failed", "branch head changed; branch retained"
	case previous:
		op.Status, op.Error = "unknown", "previous branch deletion is unresolved; not resending"
	default:
		err := s.Remote.DeleteBranch(ctx, s.Repo, branch)
		if err == nil {
			op.Status = "success"
			return
		}
		op.Status, op.Error = "unknown", err.Error()
		_, exists, readErr := s.Remote.BranchHead(ctx, s.Repo, branch)
		if readErr == nil && !exists {
			op.Status, op.Error = "success", ""
		}
	}
}
