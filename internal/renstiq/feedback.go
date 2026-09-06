package renstiq

import (
	"context"
	"errors"
)

// FeedbackService coordinates validation and independent feedback operations.
// Comment and label services own their distinct reconciliation rules.
type FeedbackService struct {
	Repo     string
	Reader   PRReader
	Journal  Journal
	Comments CommentService
	Labels   LabelService
}

func (s FeedbackService) Feedback(ctx context.Context, r *Run, d Decision) ([]Operation, error) {
	if err := d.Validate(s.Repo, r.ConfigDigest, r.Policy); err != nil {
		return nil, err
	}
	if err := recordDecision(s.Journal, r, d); err != nil {
		return nil, err
	}
	pr, err := s.Reader.WaitPR(ctx, s.Repo, d.PR, Revision{d.HeadSHA, d.BaseSHA}, false)
	if err != nil {
		return nil, err
	}
	if !contains(r.Policy.PullRequests.Authors, pr.Author) {
		return nil, errors.New("PR author not allowed for feedback")
	}
	results := []Operation{}
	if commentRequested(r.Policy, d) {
		op, err := s.Comments.Ensure(ctx, r, d, pr.Comments)
		if err != nil {
			return results, err
		}
		results = append(results, op)
	}
	for _, group := range []struct {
		labels []string
		add    bool
	}{{d.Feedback.Add, true}, {d.Feedback.Remove, false}} {
		for _, label := range group.labels {
			op, err := s.Labels.Ensure(ctx, r, d.PR, label, group.add)
			if err != nil {
				return results, err
			}
			results = append(results, op)
		}
	}
	for _, op := range results {
		if op.Status == "failed" || op.Status == "unknown" {
			return results, errors.New("one or more feedback operations failed or remain unknown")
		}
	}
	return results, nil
}

func commentRequested(p Policy, d Decision) bool {
	allowed := d.ReasonType == "compatibility" || contains(p.Feedback.CommentOn, d.ReasonType)
	return allowed && (d.Feedback.Comment != "" || d.ReasonType == "compatibility" || d.Feedback.EquivalentID != 0)
}

func recordDecision(j Journal, r *Run, d Decision) error {
	if !r.RecordDecision(d) {
		return nil
	}
	return j.Save()
}

func saveOperation(j Journal, r *Run, op Operation) error {
	r.PutOperation(op)
	return j.Save()
}
