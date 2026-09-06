package renstiq

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

type InitRequest struct{ ConfigPath, Repo string }
type DiscoverRequest struct {
	ConfigPath string
	All        bool
}
type ConfigRequest struct{ ConfigPath, Repo string }
type PRListRequest struct {
	ConfigPath, Repo string
	All              bool
}
type Repository struct{ Dir, Name string }

type InputError struct{ Cause error }

func (e *InputError) Error() string { return e.Cause.Error() }
func (e *InputError) Unwrap() error { return e.Cause }

type Sources struct {
	Common     *string `json:"common"`
	Repository string  `json:"repository"`
}
type ConfigResult struct {
	Version int      `json:"version"`
	Path    string   `json:"path"`
	Repo    string   `json:"repo"`
	Enabled *bool    `json:"enabled,omitempty"`
	Sources *Sources `json:"sources,omitempty"`
	Config  *Policy  `json:"config,omitempty"`
}
type ReadError struct {
	Path    string `json:"path,omitempty"`
	PR      int    `json:"pr,omitempty"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
}
type DiscoveryResult struct {
	Version   int         `json:"version"`
	Discovery []Discovery `json:"discovery"`
	Errors    []ReadError `json:"errors"`
}

// Dependencies only wrap I/O used by the remaining commands.
type Application struct {
	LoadConfig    func(string) (Config, error)
	LoadPolicy    func(string, Config) (Policy, bool, error)
	DiscoverRepos func(Config) []Discovery
	ResolveRepo   func(context.Context, string) (Repository, error)
	Reader        func(context.Context, Config) (PRListReader, error)
	Initialize    func(context.Context, InitRequest) (InitResult, error)
}

func (a *Application) Init(ctx context.Context, req InitRequest) (InitResult, error) {
	return a.Initialize(ctx, req)
}
func discoveryFailed(d Discovery) bool {
	return d.Status == "config_error" || d.Status == "discovery_error" || d.Status == "repository_error"
}
func (a *Application) Discover(_ context.Context, req DiscoverRequest) (DiscoveryResult, error) {
	result := DiscoveryResult{Version: 1, Discovery: []Discovery{}, Errors: []ReadError{}}
	c, err := a.LoadConfig(req.ConfigPath)
	if err != nil {
		return result, err
	}
	var failures []error
	for _, d := range a.DiscoverRepos(c) {
		if req.All || d.Status == "enabled" {
			result.Discovery = append(result.Discovery, d)
		}
		if discoveryFailed(d) {
			result.Errors = append(result.Errors, ReadError{Path: d.Path, Stage: d.Status, Message: d.Reason})
			failure := fmt.Errorf("%s: %s", d.Path, d.Reason)
			if d.Status == "config_error" {
				failures = append(failures, &InputError{failure})
			} else {
				failures = append(failures, failure)
			}
		}
	}
	return result, errors.Join(failures...)
}
func (a *Application) resolveConfig(ctx context.Context, req ConfigRequest) (ConfigResult, Config, error) {
	result := ConfigResult{Version: 1, Path: req.Repo}
	if req.Repo == "" {
		return result, Config{}, &InputError{errors.New("--repo is required")}
	}
	c, err := a.LoadConfig(req.ConfigPath)
	if err != nil {
		return result, c, err
	}
	repo, err := a.ResolveRepo(ctx, req.Repo)
	if err != nil {
		return result, c, err
	}
	result.Path, result.Repo = repo.Dir, repo.Name
	policy, enabled, err := a.LoadPolicy(repo.Dir, c)
	if err != nil {
		return result, c, err
	}
	result.Enabled, result.Config = &enabled, &policy
	result.Sources = &Sources{Common: c.Source, Repository: filepath.Join(repo.Dir, "renstiq.yaml")}
	return result, c, nil
}
func (a *Application) ConfigShow(ctx context.Context, req ConfigRequest) (ConfigResult, error) {
	result, _, err := a.resolveConfig(ctx, req)
	return result, err
}
