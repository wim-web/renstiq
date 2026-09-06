package renstiq

import (
	"context"
	"errors"
	"time"
)

// Requests model individual use cases. Unrelated CLI flags never reach them.
type RepoTarget struct {
	Repo string
	All  bool
}

func (t RepoTarget) Validate() error {
	if (t.Repo == "") == !t.All {
		return errors.New("specify exactly one of --repo or --all")
	}
	return nil
}

type InitRequest struct{ ConfigPath, Repo string }
type DiscoverRequest struct{ ConfigPath string }
type InspectRequest struct {
	Target                      RepoTarget
	ConfigPath, StateDir, RunID string
	PR                          int
}
type FeedbackRequest struct {
	Repo, ConfigPath, StateDir, RunID string
	Decision                          Decision
}
type MergeRequest struct {
	Repo, ConfigPath, StateDir, RunID string
	Decision                          Decision
}
type PostMergeRequest struct {
	Target                      RepoTarget
	ConfigPath, StateDir, RunID string
	Finish                      bool
}
type StatusRequest struct {
	Target               RepoTarget
	ConfigPath, StateDir string
}
type AbandonRequest struct{ Repo, StateDir, RunID, Reason string }

type Repository struct{ Dir, Name string }

type RepoResult struct {
	Decision     *Decision     `json:"decision,omitempty"`
	Path         string        `json:"path"`
	Repo         string        `json:"repo,omitempty"`
	RunID        string        `json:"run,omitempty"`
	Config       *Policy       `json:"config,omitempty"`
	ConfigDigest string        `json:"config_digest,omitempty"`
	PRs          []PullRequest `json:"pull_requests,omitempty"`
	Operations   []Operation   `json:"operations,omitempty"`
	Merge        *MergeRecord  `json:"merge,omitempty"`
	State        *State        `json:"state,omitempty"`
	Error        string        `json:"error,omitempty"`
}

type BatchResult struct {
	Results   []RepoResult
	Discovery []Discovery
}

type InputError struct{ Cause error }

func (e *InputError) Error() string { return e.Cause.Error() }
func (e *InputError) Unwrap() error { return e.Cause }

// Application owns request-level orchestration and repository-session lifetime.
// The CLI depends on individual method values, not on this dependency container.
type Application struct {
	LoadConfig    func(string) (Config, error)
	LoadPolicy    func(string, Config) (Policy, string, error)
	DiscoverRepos func(Config) []Discovery
	ResolveRepo   func(context.Context, string) (Repository, error)
	OpenState     func(string, string) (StateSession, error)
	Remotes       func(context.Context, Config) (remotePorts, error)
	BuildEngine   func(Repository, Journal, remotePorts, string) *Engine
	Initialize    func(context.Context, InitRequest) (InitResult, error)
	Now           func() time.Time
	NewID         func() string
}

func (a *Application) runs(s StateSession) RunSession {
	return RunSession{State: s.StateView(), Journal: s, Now: a.Now, NewID: a.NewID}
}

func (a *Application) config(path string) (Config, error) {
	c, err := a.LoadConfig(path)
	if err != nil {
		return c, &InputError{err}
	}
	return c, nil
}

func discoveryFailed(d Discovery) bool {
	return d.Status == "config_error" || d.Status == "discovery_error" || d.Status == "repository_error"
}

func (a *Application) targets(c Config, target RepoTarget) (BatchResult, []string) {
	batch := BatchResult{Results: []RepoResult{}}
	if !target.All {
		return batch, []string{target.Repo}
	}
	batch.Discovery = a.DiscoverRepos(c)
	paths := []string{}
	for _, d := range batch.Discovery {
		if d.Status == "enabled" {
			paths = append(paths, d.Path)
		}
	}
	return batch, paths
}

func (a *Application) visit(ctx context.Context, paths []string, stateDir string, run func(Repository, StateSession) RepoResult) []RepoResult {
	results := make([]RepoResult, 0, len(paths))
	for _, path := range paths {
		results = append(results, a.withRepository(ctx, path, stateDir, run))
	}
	return results
}

func (a *Application) withRepository(ctx context.Context, path, stateDir string, run func(Repository, StateSession) RepoResult) RepoResult {
	result := RepoResult{Path: path}
	repo, err := a.ResolveRepo(ctx, path)
	if err != nil {
		return repoFailure(result, err)
	}
	result.Path, result.Repo = repo.Dir, repo.Name
	session, err := a.OpenState(stateDir, repo.Name)
	if err != nil {
		return repoFailure(result, err)
	}
	defer session.Close() // Hold the lock through all remote effects and saves.
	result = run(repo, session)
	result.Path, result.Repo = repo.Dir, repo.Name
	return result
}

func repoFailure(result RepoResult, err error) RepoResult {
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

func resumeRun(state *State, id, hash string) (*Run, error) {
	r, err := state.Find(id)
	if err != nil {
		return nil, err
	}
	if err := r.CheckConfig(hash); err != nil {
		return nil, err
	}
	return r, nil
}

func (a *Application) Init(ctx context.Context, req InitRequest) (InitResult, error) {
	return a.Initialize(ctx, req)
}

func (a *Application) Discover(ctx context.Context, req DiscoverRequest) (BatchResult, error) {
	c, err := a.config(req.ConfigPath)
	if err != nil {
		return BatchResult{}, err
	}
	return BatchResult{Results: []RepoResult{}, Discovery: a.DiscoverRepos(c)}, nil
}
