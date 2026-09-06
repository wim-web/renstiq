package renstiq

import (
	"context"
	"io"
)

// Construction does no I/O; only pr list resolves GitHub authentication.
func newApplication(log io.Writer) *Application {
	return &Application{
		LoadConfig: LoadConfig, LoadPolicy: LoadPolicy, DiscoverRepos: Discover,
		ResolveRepo: func(ctx context.Context, path string) (Repository, error) {
			dir, err := canonicalDir(expandHome(path))
			if err != nil {
				return Repository{}, err
			}
			name, err := repository(ctx, dir)
			return Repository{Dir: dir, Name: name}, err
		},
		Reader: func(ctx context.Context, c Config) (PRListReader, error) { return NewGitHub(ctx, c, log) },
		Initialize: func(ctx context.Context, req InitRequest) (InitResult, error) {
			return initializeConfig(ctx, req.ConfigPath, req.Repo)
		},
	}
}
