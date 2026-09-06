package renstiq

import "context"

func (a *Application) Feedback(ctx context.Context, req FeedbackRequest) (RepoResult, error) {
	c, err := a.config(req.ConfigPath)
	if err != nil {
		return RepoResult{}, err
	}
	remote, err := a.Remotes(ctx, c)
	if err != nil {
		return RepoResult{}, err
	}
	return a.withRepository(ctx, req.Repo, req.StateDir, func(repo Repository, s StateSession) RepoResult {
		result := RepoResult{Decision: &req.Decision}
		_, hash, err := a.LoadPolicy(repo.Dir, c)
		if err != nil {
			return repoFailure(result, err)
		}
		r, err := resumeRun(s.StateView(), req.RunID, hash)
		if err != nil {
			return repoFailure(result, err)
		}
		result.RunID = r.ID
		engine := a.BuildEngine(repo, s, remote, req.StateDir)
		result.Operations, err = engine.Feedback(ctx, r, req.Decision)
		return repoFailure(result, err)
	}), nil
}

func (a *Application) Merge(ctx context.Context, req MergeRequest) (RepoResult, error) {
	c, err := a.config(req.ConfigPath)
	if err != nil {
		return RepoResult{}, err
	}
	remote, err := a.Remotes(ctx, c)
	if err != nil {
		return RepoResult{}, err
	}
	return a.withRepository(ctx, req.Repo, req.StateDir, func(repo Repository, s StateSession) RepoResult {
		result := RepoResult{Decision: &req.Decision}
		_, hash, err := a.LoadPolicy(repo.Dir, c)
		if err != nil {
			return repoFailure(result, err)
		}
		r, err := resumeRun(s.StateView(), req.RunID, hash)
		if err != nil {
			return repoFailure(result, err)
		}
		result.RunID = r.ID
		engine := a.BuildEngine(repo, s, remote, req.StateDir)
		result.Merge, err = engine.Merge(ctx, r, req.Decision)
		result.Operations = r.Operations
		return repoFailure(result, err)
	}), nil
}
