package renstiq

import "context"

func (a *Application) PostMerge(ctx context.Context, req PostMergeRequest) (BatchResult, error) {
	c, err := a.config(req.ConfigPath)
	if err != nil {
		return BatchResult{}, err
	}
	batch, paths := a.targets(c, req.Target)
	if len(paths) == 0 {
		return batch, nil
	}
	remote, err := a.Remotes(ctx, c)
	if err != nil {
		return batch, err
	}
	batch.Results = a.visit(ctx, paths, req.StateDir, func(repo Repository, s StateSession) RepoResult {
		result := RepoResult{}
		_, hash, err := a.LoadPolicy(repo.Dir, c)
		if err != nil {
			return repoFailure(result, err)
		}
		var r *Run
		if req.RunID != "" {
			r, err = resumeRun(s.StateView(), req.RunID, hash)
		} else {
			r = s.StateView().Latest()
			if r != nil {
				err = r.CheckConfig(hash)
			}
		}
		if err != nil {
			return repoFailure(result, err)
		}
		if r == nil {
			return result
		}
		result.RunID = r.ID
		engine := a.BuildEngine(repo, s, remote, req.StateDir)
		err = engine.PostMerge(ctx, r, req.Finish)
		result.Operations = r.Operations
		return repoFailure(result, err)
	})
	return batch, nil
}
