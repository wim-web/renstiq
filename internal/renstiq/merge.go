package renstiq

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type MergeReceipt struct {
	Merged          bool
	Commit, Message string
}

type MergeGateway interface {
	PRReader
	PRStateReader
	MergePullRequest(context.Context, string, int, string, string) (MergeReceipt, error)
}

type MergeService struct {
	Repo       string
	Remote     MergeGateway
	Journal    Journal
	Reconciler MergeReconciler
	AfterMerge func(context.Context, *Run, bool) error
}

func (s MergeService) Merge(ctx context.Context, r *Run, d Decision) (*MergeRecord, error) {
	if err := d.Validate(s.Repo, r.ConfigDigest, r.Policy); err != nil {
		return nil, err
	}
	if d.Decision != "merge" {
		return nil, errors.New("decision is not merge; use feedback for a hold")
	}
	for i := range r.Merges {
		if r.Merges[i].PR == d.PR {
			return s.resume(ctx, r, &r.Merges[i], d)
		}
	}
	if err := r.RequireOpen(); err != nil {
		return nil, err
	}
	// Complete prior cleanup before admitting another external merge.
	if err := s.AfterMerge(ctx, r, false); err != nil {
		return nil, err
	}
	if err := recordDecision(s.Journal, r, d); err != nil {
		return nil, err
	}
	pr, err := s.validateCurrent(ctx, r, d)
	if err != nil {
		return nil, err
	}
	// Repeat mutable checks immediately before the conditional merge request.
	pr, err = s.validateCurrent(ctx, r, d)
	if err != nil {
		return nil, err
	}
	r.Merges = append(r.Merges, MergeRecord{PR: d.PR, HeadSHA: d.HeadSHA, Base: pr.Base, Files: pr.Files, Decision: d, Status: "pending"})
	if err := s.Journal.Save(); err != nil {
		return nil, err
	}
	record := &r.Merges[len(r.Merges)-1]
	s.submit(ctx, r, record)
	if err := s.Journal.Save(); err != nil {
		return record, err
	}
	if record.Status != "merged" {
		return record, fmt.Errorf("merge %s: %s", record.Status, record.Error)
	}
	return record, s.AfterMerge(ctx, r, false)
}

func (s MergeService) resume(ctx context.Context, r *Run, m *MergeRecord, d Decision) (*MergeRecord, error) {
	if m.HeadSHA != d.HeadSHA {
		return m, errors.New("previous merge record has a different head SHA")
	}
	switch m.Status {
	case "merged":
		return m, s.AfterMerge(ctx, r, false)
	case "pending", "unknown":
		if err := s.Reconciler.Reconcile(ctx, r, m); err != nil {
			return m, err
		}
		return m, s.AfterMerge(ctx, r, false)
	case "rejected":
		return m, errors.New("merge was rejected in this run; finish the run before a new attempt")
	default:
		return m, fmt.Errorf("unexpected merge status: %s", m.Status)
	}
}

func (s MergeService) validateCurrent(ctx context.Context, r *Run, d Decision) (PullRequest, error) {
	pr, err := s.Remote.WaitPR(ctx, s.Repo, d.PR, Revision{d.HeadSHA, d.BaseSHA}, false)
	if err != nil {
		return pr, err
	}
	if reasons := policyReasons(r.Policy, pr, d); len(reasons) > 0 {
		return pr, fmt.Errorf("merge blocked: %s", strings.Join(reasons, "; "))
	}
	return pr, nil
}

func (s MergeService) submit(ctx context.Context, r *Run, m *MergeRecord) {
	receipt, err := s.Remote.MergePullRequest(ctx, s.Repo, m.PR, m.HeadSHA, r.Policy.Merge.Method)
	switch {
	case err == nil && receipt.Merged && receipt.Commit != "":
		m.Status, m.Commit = "merged", receipt.Commit
	case err == nil:
		m.Status, m.Error = "rejected", receipt.Message
	case rejectedWrite(err):
		m.Status, m.Error = "rejected", err.Error()
	default:
		m.Status, m.Error = "unknown", err.Error()
		_ = s.Reconciler.Reconcile(ctx, r, m)
	}
}

type MergeReconciler struct {
	Repo    string
	Remote  PRStateReader
	Journal Journal
}

func (s MergeReconciler) Reconcile(ctx context.Context, r *Run, m *MergeRecord) error {
	state, err := s.Remote.PullRequestState(ctx, s.Repo, m.PR)
	if err != nil {
		return err
	}
	if state.Merged && state.HeadSHA == m.HeadSHA && state.MergeCommit != "" {
		m.Status, m.Commit, m.Error = "merged", state.MergeCommit, ""
		return s.Journal.Save()
	}
	m.Status, m.Error = "unknown", "merge response unknown; current state cannot confirm completion; request will not be resent"
	if err := s.Journal.Save(); err != nil {
		return err
	}
	return errors.New(m.Error)
}
