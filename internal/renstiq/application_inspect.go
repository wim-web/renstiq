package renstiq

import "context"

func (a *Application) Inspect(ctx context.Context, req InspectRequest) (BatchResult, error) {
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
		policy, hash, err := a.LoadPolicy(repo.Dir, c)
		if err != nil {
			return repoFailure(result, err)
		}
		var r *Run
		if req.RunID != "" {
			r, err = resumeRun(s.StateView(), req.RunID, hash)
		} else {
			r, err = a.runs(s).Current(policy, hash)
		}
		if err != nil {
			return repoFailure(result, err)
		}
		result.RunID, result.Config, result.ConfigDigest = r.ID, &policy, hash
		result.PRs, err = (Inspector{Remote: remote.Inspect}).Inspect(ctx, repo.Name, policy, req.PR)
		return repoFailure(result, err)
	})
	return batch, nil
}
