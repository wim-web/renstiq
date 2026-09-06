package renstiq

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

// Construction itself performs no I/O. Dependencies are resolved only after a
// command's parser has validated its complete input contract.
func newApplication(log io.Writer) *Application {
	return &Application{
		LoadConfig: LoadConfig, LoadPolicy: LoadPolicy, DiscoverRepos: Discover,
		ResolveRepo: func(ctx context.Context, path string) (Repository, error) {
			dir, err := canonicalDir(path)
			if err != nil {
				return Repository{}, err
			}
			name, err := repository(ctx, dir)
			return Repository{Dir: dir, Name: name}, err
		},
		OpenState: func(dir, repo string) (StateSession, error) { return openStore(dir, repo) },
		Remotes: func(ctx context.Context, c Config) (remotePorts, error) {
			g, err := NewGitHub(ctx, c, log)
			if err != nil {
				return remotePorts{}, err
			}
			return githubPorts(g), nil
		},
		BuildEngine: func(repo Repository, j Journal, remote remotePorts, stateDir string) *Engine {
			home, _ := os.UserHomeDir()
			executor := &PostExecutor{Repo: repo.Name, Dir: repo.Dir, Home: home, Journal: j, Sync: synchronize,
				Runner: ExecRunner{}, Logs: fileLogs(stateDir, repo.Name), Output: log, Now: time.Now}
			return newEngine(repo.Name, j, remote, executor, time.Now)
		},
		Initialize: func(ctx context.Context, req InitRequest) (InitResult, error) {
			return initializeConfig(ctx, req.ConfigPath, req.Repo)
		},
		Now: time.Now, NewID: func() string { return fmt.Sprint(time.Now().UnixNano()) },
	}
}
