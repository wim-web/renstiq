package renstiq

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type Engine struct {
	GitHub    *GitHub
	Store     *Store
	Dir, Repo string
	Sync      func(context.Context, string, string, []MergeRecord) error
}

func (e *Engine) Feedback(ctx context.Context, r *Run, d Decision) ([]Operation, error) {
	p := r.Policy
	if err := d.Validate(e.Repo, r.ConfigDigest, p); err != nil {
		return nil, err
	}
	if err := e.recordDecision(r, d); err != nil {
		return nil, err
	}
	pr, err := e.GitHub.waitPR(ctx, e.Repo, d.PR, d.HeadSHA, d.BaseSHA, false)
	if err != nil {
		return nil, err
	}
	if !contains(p.PullRequests.Authors, pr.Author) {
		return nil, errors.New("PR author not allowed for feedback")
	}
	results := []Operation{}
	failed := false
	commentAllowed := d.ReasonType == "compatibility" || contains(p.Feedback.CommentOn, d.ReasonType)
	if commentAllowed && (d.Feedback.Comment != "" || d.ReasonType == "compatibility" || d.Feedback.EquivalentID != 0) {
		body := feedbackBody(d)
		id := "comment:" + fmt.Sprint(d.PR) + ":" + digest(body)
		op := Operation{ID: id, Kind: "comment", PR: d.PR, Status: "pending", Started: now()}
		existing := int64(0)
		for _, c := range pr.Comments {
			if c.ID == d.Feedback.EquivalentID {
				existing = c.ID
				op.URL = c.URL
			}
			if c.Automation && c.Body == body {
				existing = c.ID
				op.URL = c.URL
			}
		}
		if d.Feedback.EquivalentID != 0 && existing == 0 {
			op.Status = "failed"
			op.Error = "equivalent comment does not exist on this PR"
		} else if existing != 0 {
			op.Status = "skipped"
		} else {
			path := fmt.Sprintf("/repos/%s/issues/%d/comments", e.Repo, d.PR)
			method := "POST"
			if d.Feedback.UpdateID != 0 {
				found := false
				for _, c := range pr.Comments {
					if c.ID == d.Feedback.UpdateID && c.Automation {
						found = true
					}
				}
				if !found {
					op.Status = "failed"
					op.Error = "only renstiq comments owned by the current actor can be updated"
				} else {
					path = fmt.Sprintf("/repos/%s/issues/comments/%d", e.Repo, d.Feedback.UpdateID)
					method = "PATCH"
				}
			}
			if op.Status == "pending" {
				old := r.op(id)
				if old != nil && (old.Status == "pending" || old.Status == "unknown") {
					op.Status = "unknown"
					op.Error = "previous comment request is unresolved; not resending"
				} else {
					upsertOperation(r, op)
					if err = e.Store.Save(); err != nil {
						return results, err
					}
					var created Comment
					err = e.GitHub.write(ctx, method, path, map[string]string{"body": body}, &created)
					if err == nil {
						op.Status = "success"
						op.URL = created.URL
					} else {
						op.Status = "unknown"
						op.Error = err.Error()
						comments, readErr := e.GitHub.comments(ctx, e.Repo, d.PR)
						if readErr == nil {
							for _, c := range comments {
								if c.Automation && c.Body == body {
									op.Status = "success"
									op.URL = c.URL
									op.Error = ""
								}
							}
						}
						var ae *APIError
						if op.Status != "success" && errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500 {
							op.Status = "failed"
						}
					}
				}
			}
		}
		op.Finished = now()
		upsertOperation(r, op)
		if err = e.Store.Save(); err != nil {
			return results, err
		}
		results = append(results, op)
		failed = op.Status == "failed" || op.Status == "unknown"
	}
	for _, group := range []struct {
		labels []string
		add    bool
	}{{d.Feedback.Add, true}, {d.Feedback.Remove, false}} {
		for _, label := range group.labels {
			id := fmt.Sprintf("label:%d:%t:%s", d.PR, group.add, label)
			op := Operation{ID: id, Kind: "label", PR: d.PR, Started: now(), Status: "pending"}
			raw, getErr := e.GitHub.raw(ctx, e.Repo, d.PR)
			if getErr != nil {
				op.Status = "failed"
				op.Error = getErr.Error()
			} else {
				present := false
				for _, l := range raw.Labels {
					if l.Name == label {
						present = true
					}
				}
				if present == group.add {
					op.Status = "skipped"
				} else if old := r.op(id); old != nil && (old.Status == "pending" || old.Status == "unknown") {
					op.Status = "unknown"
					op.Error = "previous label request unresolved; not resending"
				} else {
					upsertOperation(r, op)
					if err = e.Store.Save(); err != nil {
						return results, err
					}
					path := fmt.Sprintf("/repos/%s/issues/%d/labels", e.Repo, d.PR)
					var writeErr error
					if group.add {
						writeErr = e.GitHub.write(ctx, "POST", path, map[string]any{"labels": []string{label}}, nil)
					} else {
						writeErr = e.GitHub.write(ctx, "DELETE", path+"/"+escaped(label), nil, nil)
					}
					op.Status = "success"
					if writeErr != nil {
						op.Status = "unknown"
						op.Error = writeErr.Error()
						raw, re := e.GitHub.raw(ctx, e.Repo, d.PR)
						if re == nil {
							present = false
							for _, l := range raw.Labels {
								if l.Name == label {
									present = true
								}
							}
							if present == group.add {
								op.Status = "success"
								op.Error = ""
							}
						}
						var ae *APIError
						if op.Status != "success" && errors.As(writeErr, &ae) && ae.Status >= 400 && ae.Status < 500 {
							op.Status = "failed"
						}
					}
				}
			}
			op.Finished = now()
			upsertOperation(r, op)
			if err = e.Store.Save(); err != nil {
				return results, err
			}
			results = append(results, op)
			failed = failed || op.Status == "failed" || op.Status == "unknown"
		}
	}
	if failed {
		return results, errors.New("one or more feedback operations failed or remain unknown")
	}
	return results, nil
}
func upsertOperation(r *Run, op Operation) {
	// Older runs can contain duplicate comment records. Replace all matching
	// records with one resolved/current operation, preserving the other IDs.
	operations := r.Operations[:0]
	replaced := false
	for _, old := range r.Operations {
		if old.ID == op.ID {
			if replaced {
				continue
			}
			old = op
			replaced = true
		}
		operations = append(operations, old)
	}
	if !replaced {
		operations = append(operations, op)
	}
	r.Operations = operations
}
func (e *Engine) Merge(ctx context.Context, r *Run, d Decision) (*MergeRecord, error) {
	if err := d.Validate(e.Repo, r.ConfigDigest, r.Policy); err != nil {
		return nil, err
	}
	if d.Decision != "merge" {
		return nil, errors.New("decision is not merge; use feedback for a hold")
	}
	for i := range r.Merges {
		m := &r.Merges[i]
		if m.PR == d.PR {
			if m.HeadSHA != d.HeadSHA {
				return m, errors.New("previous merge record has a different head SHA")
			}
			if m.Status == "merged" {
				return m, e.PostMerge(ctx, r, false)
			}
			if m.Status == "pending" || m.Status == "unknown" {
				if err := e.reconcileMerge(ctx, r, m); err != nil {
					return m, err
				}
				return m, e.PostMerge(ctx, r, false)
			}
			if m.Status == "rejected" {
				return m, errors.New("merge was rejected in this run; finish the run before a new attempt")
			}
		}
	}
	if r.Phase != "open" {
		return nil, errors.New("run is already finalizing/finished; additional merges are forbidden")
	}
	// Reconcile unfinished cleanup before checking whether another merge is safe.
	if err := e.PostMerge(ctx, r, false); err != nil {
		return nil, err
	}
	if err := e.recordDecision(r, d); err != nil {
		return nil, err
	}
	pr, err := e.GitHub.waitPR(ctx, e.Repo, d.PR, d.HeadSHA, d.BaseSHA, false)
	if err != nil {
		return nil, err
	}
	if reasons := policyReasons(r.Policy, pr, d); len(reasons) > 0 {
		return nil, fmt.Errorf("merge blocked: %s", strings.Join(reasons, "; "))
	}
	// Repeat all mutable checks immediately before the conditional merge request.
	pr, err = e.GitHub.waitPR(ctx, e.Repo, d.PR, d.HeadSHA, d.BaseSHA, false)
	if err != nil {
		return nil, err
	}
	if reasons := policyReasons(r.Policy, pr, d); len(reasons) > 0 {
		return nil, fmt.Errorf("merge blocked: %s", strings.Join(reasons, "; "))
	}
	m := MergeRecord{PR: d.PR, HeadSHA: d.HeadSHA, Base: pr.Base, Files: pr.Files, Decision: d, Status: "pending"}
	r.Merges = append(r.Merges, m)
	if err = e.Store.Save(); err != nil {
		return nil, err
	}
	record := &r.Merges[len(r.Merges)-1]
	var response struct {
		Merged  bool   `json:"merged"`
		SHA     string `json:"sha"`
		Message string `json:"message"`
	}
	err = e.GitHub.write(ctx, "PUT", fmt.Sprintf("/repos/%s/pulls/%d/merge", e.Repo, d.PR), map[string]string{"sha": d.HeadSHA, "merge_method": r.Policy.Merge.Method}, &response)
	if err == nil && response.Merged && response.SHA != "" {
		record.Status = "merged"
		record.Commit = response.SHA
	} else if err == nil {
		record.Status = "rejected"
		record.Error = response.Message
	} else {
		record.Status = "unknown"
		record.Error = err.Error()
		var ae *APIError
		if errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500 {
			record.Status = "rejected"
		}
		if record.Status == "unknown" {
			_ = e.reconcileMerge(ctx, r, record)
		}
	}
	if saveErr := e.Store.Save(); saveErr != nil {
		return record, saveErr
	}
	if record.Status != "merged" {
		return record, fmt.Errorf("merge %s: %s", record.Status, record.Error)
	}
	return record, e.PostMerge(ctx, r, false)
}
func (e *Engine) reconcileMerge(ctx context.Context, r *Run, m *MergeRecord) error {
	raw, err := e.GitHub.raw(ctx, e.Repo, m.PR)
	if err != nil {
		return err
	}
	if raw.Merged && raw.Head.SHA == m.HeadSHA && raw.MergeCommit != "" {
		m.Status = "merged"
		m.Commit = raw.MergeCommit
		m.Error = ""
		return e.Store.Save()
	}
	m.Status = "unknown"
	m.Error = "merge response unknown; current state cannot confirm completion; request will not be resent"
	if err = e.Store.Save(); err != nil {
		return err
	}
	return errors.New(m.Error)
}
func (e *Engine) deleteBranch(ctx context.Context, r *Run, m *MergeRecord) error {
	id := fmt.Sprintf("delete_branch:%d", m.PR)
	old := r.op(id)
	if old != nil && (old.Status == "success" || old.Status == "skipped") {
		return nil
	}
	raw, err := e.GitHub.raw(ctx, e.Repo, m.PR)
	if err != nil {
		return err
	}
	if raw.Head.Repo.FullName != e.Repo || raw.Head.Ref == raw.Base.Ref {
		return errors.New("refusing to delete a fork/base branch")
	}
	path := "/repos/" + e.Repo + "/git/refs/heads/" + escaped(raw.Head.Ref)
	op := Operation{ID: id, Kind: "delete_branch", PR: m.PR, Status: "pending", Started: now()}
	upsertOperation(r, op)
	if err = e.Store.Save(); err != nil {
		return err
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err = e.GitHub.get(ctx, strings.Replace(path, "/git/refs/", "/git/ref/", 1), &ref)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 404 {
		op.Status = "skipped"
	} else if err != nil {
		op.Status = "failed"
		op.Error = err.Error()
	} else if ref.Object.SHA != m.HeadSHA {
		op.Status = "failed"
		op.Error = "branch head changed; branch retained"
	} else if old != nil {
		op.Status = "unknown"
		op.Error = "previous branch deletion is unresolved; not resending"
	} else if err = e.GitHub.write(ctx, "DELETE", path, nil, nil); err != nil {
		op.Status = "unknown"
		op.Error = err.Error()
		readErr := e.GitHub.get(ctx, strings.Replace(path, "/git/refs/", "/git/ref/", 1), &ref)
		if errors.As(readErr, &ae) && ae.Status == 404 {
			op.Status = "success"
			op.Error = ""
		}
	} else {
		op.Status = "success"
	}
	op.Finished = now()
	upsertOperation(r, op)
	if err = e.Store.Save(); err != nil {
		return err
	}
	if op.Status == "failed" || op.Status == "unknown" {
		return errors.New(op.Error)
	}
	return nil
}

func (e *Engine) recordDecision(r *Run, d Decision) error {
	for _, old := range r.Decisions {
		if digest(old) == digest(d) {
			return nil
		}
	}
	r.Decisions = append(r.Decisions, d)
	return e.Store.Save()
}
