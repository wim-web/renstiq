package renstiq

import "context"

func (a *Application) Status(ctx context.Context, req StatusRequest) (BatchResult, error) {
	// A single-repository recovery command does not need configuration or auth.
	c := Config{}
	if req.Target.All {
		var err error
		c, err = a.config(req.ConfigPath)
		if err != nil {
			return BatchResult{}, err
		}
	}
	batch, paths := a.targets(c, req.Target)
	batch.Results = a.visit(ctx, paths, req.StateDir, func(repo Repository, s StateSession) RepoResult {
		return RepoResult{State: s.StateView()}
	})
	return batch, nil
}

func (a *Application) Abandon(ctx context.Context, req AbandonRequest) (RepoResult, error) {
	return a.withRepository(ctx, req.Repo, req.StateDir, func(repo Repository, s StateSession) RepoResult {
		result := RepoResult{RunID: req.RunID}
		err := a.runs(s).Abandon(req.RunID, req.Reason)
		result.State = s.StateView()
		return repoFailure(result, err)
	}), nil
}
