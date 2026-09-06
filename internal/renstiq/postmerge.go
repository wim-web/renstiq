package renstiq

import (
	"context"
	"fmt"
)

type PostInput struct {
	Version int           `json:"version"`
	Repo    string        `json:"repo"`
	Run     string        `json:"run"`
	Action  string        `json:"action"`
	Timing  string        `json:"timing"`
	Merges  []MergeRecord `json:"merges"`
}

type PostActionExecutor interface {
	Execute(context.Context, *Run, PostCommand, string, []MergeRecord) error
}

type PostMergeService struct {
	Journal   Journal
	Reconcile func(context.Context, *Run, *MergeRecord) error
	Cleanup   func(context.Context, *Run, *MergeRecord) error
	Actions   PostActionExecutor
}

func (s PostMergeService) PostMerge(ctx context.Context, r *Run, finish bool) error {
	for i := range r.Merges {
		m := &r.Merges[i]
		if m.Status == "pending" || m.Status == "unknown" {
			if err := s.Reconcile(ctx, r, m); err != nil {
				return err
			}
		}
	}
	if r.Phase == PhaseFinished {
		return r.blocked()
	}
	if err := s.cleanBranches(ctx, r); err != nil {
		return err
	}
	if err := r.blocked(); err != nil {
		return err
	}
	if err := s.afterEach(ctx, r); err != nil {
		return err
	}
	if !finish {
		return nil
	}
	if err := r.BeginFinalization(); err != nil {
		return err
	}
	if err := s.Journal.Save(); err != nil {
		return err
	}
	if err := s.afterRepo(ctx, r); err != nil {
		return err
	}
	if err := r.Finish(); err != nil {
		return err
	}
	return s.Journal.Save()
}

func (s PostMergeService) cleanBranches(ctx context.Context, r *Run) error {
	if !r.Policy.Merge.DeleteBranch {
		return nil
	}
	for i := range r.Merges {
		m := &r.Merges[i]
		if m.Status == "merged" {
			if err := s.Cleanup(ctx, r, m); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s PostMergeService) afterEach(ctx context.Context, r *Run) error {
	for _, m := range r.Merges {
		if m.Status != "merged" {
			continue
		}
		for _, a := range r.Policy.PostMerge {
			if a.Timing != "after_each_merge" || !selected(a, m) {
				continue
			}
			if err := s.Actions.Execute(ctx, r, a, fmt.Sprintf("post:%d:%s", m.PR, a.ID), []MergeRecord{m}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s PostMergeService) afterRepo(ctx context.Context, r *Run) error {
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
		if len(merges) > 0 {
			if err := s.Actions.Execute(ctx, r, a, "post:repo:"+a.ID, merges); err != nil {
				return err
			}
		}
	}
	return nil
}
