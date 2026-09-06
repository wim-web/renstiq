package renstiq

import (
	"context"
	"errors"
	"fmt"
)

func (g *GitHub) WaitPR(ctx context.Context, repo string, number int, rev Revision, diff bool) (PullRequest, error) {
	return g.waitPR(ctx, repo, number, rev.Head, rev.Base, diff)
}

func prState(raw rawPR) PRState {
	state := PRState{Number: raw.Number, Author: raw.User.Login, Merged: raw.Merged, HeadSHA: raw.Head.SHA,
		HeadBranch: raw.Head.Ref, HeadRepo: raw.Head.Repo.FullName, BaseBranch: raw.Base.Ref, MergeCommit: raw.MergeCommit}
	for _, l := range raw.Labels {
		state.Labels = append(state.Labels, l.Name)
	}
	return state
}

func (g *GitHub) PullRequestState(ctx context.Context, repo string, number int) (PRState, error) {
	raw, err := g.raw(ctx, repo, number)
	return prState(raw), err
}

func (g *GitHub) OpenPullRequests(ctx context.Context, repo string) ([]PRState, error) {
	raw, err := g.list(ctx, repo)
	if err != nil {
		return nil, err
	}
	prs := make([]PRState, 0, len(raw))
	for _, r := range raw {
		prs = append(prs, prState(r))
	}
	return prs, nil
}

func (g *GitHub) Comments(ctx context.Context, repo string, number int) ([]Comment, error) {
	return g.comments(ctx, repo, number)
}

func classifyWrite(err error) error {
	if err == nil {
		return nil
	}
	var api *APIError
	if errors.As(err, &api) && api.Status >= 400 && api.Status < 500 {
		return &RejectedWrite{Cause: err}
	}
	return err
}

func (g *GitHub) CreateComment(ctx context.Context, repo string, number int, body string) (Comment, error) {
	var comment Comment
	err := g.write(ctx, "POST", fmt.Sprintf("/repos/%s/issues/%d/comments", repo, number), map[string]string{"body": body}, &comment)
	return comment, classifyWrite(err)
}

func (g *GitHub) UpdateComment(ctx context.Context, repo string, id int64, body string) (Comment, error) {
	var comment Comment
	err := g.write(ctx, "PATCH", fmt.Sprintf("/repos/%s/issues/comments/%d", repo, id), map[string]string{"body": body}, &comment)
	return comment, classifyWrite(err)
}

func (g *GitHub) AddLabel(ctx context.Context, repo string, number int, label string) error {
	return classifyWrite(g.write(ctx, "POST", fmt.Sprintf("/repos/%s/issues/%d/labels", repo, number), map[string]any{"labels": []string{label}}, nil))
}
func (g *GitHub) RemoveLabel(ctx context.Context, repo string, number int, label string) error {
	return classifyWrite(g.write(ctx, "DELETE", fmt.Sprintf("/repos/%s/issues/%d/labels/%s", repo, number, escaped(label)), nil, nil))
}

func (g *GitHub) MergePullRequest(ctx context.Context, repo string, number int, head, method string) (MergeReceipt, error) {
	var result struct {
		Merged  bool   `json:"merged"`
		SHA     string `json:"sha"`
		Message string `json:"message"`
	}
	err := g.write(ctx, "PUT", fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, number), map[string]string{"sha": head, "merge_method": method}, &result)
	return MergeReceipt{Merged: result.Merged, Commit: result.SHA, Message: result.Message}, classifyWrite(err)
}

func (g *GitHub) BranchHead(ctx context.Context, repo, branch string) (string, bool, error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := g.get(ctx, "/repos/"+repo+"/git/ref/heads/"+escaped(branch), &ref)
	var api *APIError
	if errors.As(err, &api) && api.Status == 404 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return ref.Object.SHA, true, nil
}

func (g *GitHub) DeleteBranch(ctx context.Context, repo, branch string) error {
	return classifyWrite(g.write(ctx, "DELETE", "/repos/"+repo+"/git/refs/heads/"+escaped(branch), nil, nil))
}

func githubPorts(g *GitHub) remotePorts {
	return remotePorts{Inspect: g, Reader: g, Comments: g, Labels: g, Merge: g, Branches: g}
}
