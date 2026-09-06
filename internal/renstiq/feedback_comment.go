package renstiq

import (
	"context"
	"fmt"
	"time"
)

type CommentGateway interface {
	Comments(context.Context, string, int) ([]Comment, error)
	CreateComment(context.Context, string, int, string) (Comment, error)
	UpdateComment(context.Context, string, int64, string) (Comment, error)
}

type CommentService struct {
	Repo    string
	Remote  CommentGateway
	Journal Journal
	Now     func() time.Time
}

type commentPlan struct {
	Operation Operation
	Body      string
	UpdateID  int64
}

// planComment is pure: policy and identity checks do not require HTTP or storage.
func planComment(d Decision, comments []Comment, old *Operation) commentPlan {
	body := feedbackBody(d)
	op := Operation{ID: fmt.Sprintf("comment:%d:%s", d.PR, digest(body)), Kind: "comment", PR: d.PR, Status: "pending"}
	existing := int64(0)
	updateOwned := false
	for _, c := range comments {
		if c.ID == d.Feedback.EquivalentID || (c.Automation && c.Body == body) {
			existing, op.URL = c.ID, c.URL
		}
		if c.ID == d.Feedback.UpdateID && c.Automation {
			updateOwned = true
		}
	}
	switch {
	case d.Feedback.EquivalentID != 0 && existing == 0:
		op.Status, op.Error = "failed", "equivalent comment does not exist on this PR"
	case existing != 0:
		op.Status = "skipped"
	case d.Feedback.UpdateID != 0 && !updateOwned:
		op.Status, op.Error = "failed", "only renstiq comments owned by the current actor can be updated"
	case old != nil && (old.Status == "pending" || old.Status == "unknown"):
		op.Status, op.Error = "unknown", "previous comment request is unresolved; not resending"
	}
	return commentPlan{Operation: op, Body: body, UpdateID: d.Feedback.UpdateID}
}

func (s CommentService) Ensure(ctx context.Context, r *Run, d Decision, comments []Comment) (Operation, error) {
	id := fmt.Sprintf("comment:%d:%s", d.PR, digest(feedbackBody(d)))
	plan := planComment(d, comments, r.op(id))
	op := plan.Operation
	op.Started = timestamp(s.Now())
	if op.Status == "pending" {
		if err := saveOperation(s.Journal, r, op); err != nil {
			return op, err
		}
		created, err := s.send(ctx, d.PR, plan)
		if err == nil {
			op.Status, op.URL = "success", created.URL
		} else {
			s.reconcile(ctx, d.PR, plan.Body, &op, err)
		}
	}
	op.Finished = timestamp(s.Now())
	return op, saveOperation(s.Journal, r, op)
}

func (s CommentService) send(ctx context.Context, number int, plan commentPlan) (Comment, error) {
	if plan.UpdateID != 0 {
		return s.Remote.UpdateComment(ctx, s.Repo, plan.UpdateID, plan.Body)
	}
	return s.Remote.CreateComment(ctx, s.Repo, number, plan.Body)
}

func (s CommentService) reconcile(ctx context.Context, number int, body string, op *Operation, writeErr error) {
	op.Status, op.Error = "unknown", writeErr.Error()
	comments, err := s.Remote.Comments(ctx, s.Repo, number)
	if err == nil {
		for _, c := range comments {
			if c.Automation && c.Body == body {
				op.Status, op.Error, op.URL = "success", "", c.URL
				return
			}
		}
	}
	if rejectedWrite(writeErr) {
		op.Status = "failed"
	}
}
