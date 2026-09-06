package renstiq

import (
	"context"
	"fmt"
	"time"
)

type LabelGateway interface {
	PRStateReader
	AddLabel(context.Context, string, int, string) error
	RemoveLabel(context.Context, string, int, string) error
}

type LabelService struct {
	Repo    string
	Remote  LabelGateway
	Journal Journal
	Now     func() time.Time
}

func (s LabelService) Ensure(ctx context.Context, r *Run, number int, label string, add bool) (Operation, error) {
	id := fmt.Sprintf("label:%d:%t:%s", number, add, label)
	op := Operation{ID: id, Kind: "label", PR: number, Status: "pending", Started: timestamp(s.Now())}
	state, err := s.Remote.PullRequestState(ctx, s.Repo, number)
	switch {
	case err != nil:
		op.Status, op.Error = "failed", err.Error()
	case contains(state.Labels, label) == add:
		op.Status = "skipped"
	default:
		if old := r.op(id); old != nil && (old.Status == "pending" || old.Status == "unknown") {
			op.Status, op.Error = "unknown", "previous label request unresolved; not resending"
		} else {
			if err := saveOperation(s.Journal, r, op); err != nil {
				return op, err
			}
			s.apply(ctx, number, label, add, &op)
		}
	}
	op.Finished = timestamp(s.Now())
	return op, saveOperation(s.Journal, r, op)
}

func (s LabelService) apply(ctx context.Context, number int, label string, add bool, op *Operation) {
	var err error
	if add {
		err = s.Remote.AddLabel(ctx, s.Repo, number, label)
	} else {
		err = s.Remote.RemoveLabel(ctx, s.Repo, number, label)
	}
	op.Status = "success"
	if err == nil {
		return
	}
	op.Status, op.Error = "unknown", err.Error()
	state, readErr := s.Remote.PullRequestState(ctx, s.Repo, number)
	if readErr == nil && contains(state.Labels, label) == add {
		op.Status, op.Error = "success", ""
		return
	}
	if rejectedWrite(err) {
		op.Status = "failed"
	}
}
