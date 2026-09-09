package renstiq

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// The reader returns successful pages even when a later page fails. Detail
// failures leave FilesComplete/CommitsComplete false; PR-wide integrity failures
// also populate CandidateFacts.Problems or Changed.
type PRListReader interface {
	OpenPullRequests(context.Context, string) ([]PRInfo, error)
	CandidateDetails(context.Context, string, PRInfo, bool, bool) (CandidateFacts, error)
}
type PRListItem struct {
	PRInfo
	Selection
}
type PRListResult struct {
	Version      int          `json:"version"`
	Path         string       `json:"path"`
	Repo         string       `json:"repo"`
	Complete     bool         `json:"complete"`
	OpenPRCount  *int         `json:"open_pr_count"`
	PullRequests []PRListItem `json:"pull_requests"`
	Errors       []ReadError  `json:"errors"`
}

func (a *Application) PRList(ctx context.Context, req PRListRequest) (PRListResult, error) {
	result := PRListResult{Version: configVersion, Path: req.Repo, PullRequests: []PRListItem{}, Errors: []ReadError{}}
	cfg, err := a.resolveConfig(ctx, ConfigRequest{Repo: req.Repo})
	result.Path, result.Repo = cfg.Path, cfg.Repo
	if err != nil {
		return result, err
	}
	if !*cfg.Enabled {
		return result, &InputError{errors.New("repository must explicitly set enabled: true")}
	}
	reader, err := a.Reader(ctx, cfg.Config.GitHubAPIReadRetry)
	if err != nil {
		result.Errors = append(result.Errors, ReadError{Stage: "authentication", Message: err.Error()})
		return result, err
	}
	return listCandidates(ctx, reader, result, *cfg.Config, req.All)
}
func listCandidates(ctx context.Context, reader PRListReader, result PRListResult, policy Policy, all bool) (PRListResult, error) {
	prs, listErr := reader.OpenPullRequests(ctx, result.Repo)
	var failures []error
	addError := func(n int, stage string, err error) {
		result.Errors = append(result.Errors, ReadError{PR: n, Stage: stage, Message: err.Error()})
		failures = append(failures, fmt.Errorf("%s PR #%d %s: %w", result.Repo, n, stage, err))
	}
	if listErr != nil {
		addError(0, "list", listErr)
	}
	count := 0
	seen := map[int]bool{}
	for _, pr := range prs {
		if seen[pr.Number] {
			addError(pr.Number, "list", errors.New("duplicate PR across pages; population is incomplete"))
			listErr = errors.New("duplicate PR")
			continue
		}
		seen[pr.Number] = true
		if pr.State != "open" {
			continue
		}
		count++
		facts := CandidateFacts{PR: pr}
		selected := SelectCandidate(policy, facts)
		if selected.Status != SelectionExcluded {
			// A rule can be ruled out after reading files, exposing a later
			// rule that needs commits. Fetch each kind at most once and keep
			// the verified facts from previous reads of the same PR snapshot.
			readFiles, readCommits := false, false
			for selected.Status == SelectionUnknown && !facts.Changed && len(facts.Problems) == 0 {
				files, commits := detailRequirements(policy, facts)
				files, commits = files && !readFiles, commits && !readCommits
				if !files && !commits {
					break
				}
				readFiles, readCommits = readFiles || files, readCommits || commits
				previous := facts
				var err error
				facts, err = reader.CandidateDetails(ctx, result.Repo, pr, files, commits)
				if !files {
					facts.Files, facts.FilesComplete = previous.Files, previous.FilesComplete
				}
				if !commits {
					facts.CommitAuthors, facts.CommitsComplete = previous.CommitAuthors, previous.CommitsComplete
				}
				facts.Problems = append(previous.Problems, facts.Problems...)
				facts.Changed = facts.Changed || previous.Changed
				if err != nil {
					// Incomplete data affects only entries which require it. An
					// otherwise unexplained reader failure is still PR-wide.
					if !facts.Changed && len(facts.Problems) == 0 && (!files || facts.FilesComplete) && (!commits || facts.CommitsComplete) {
						facts.Problems = append(facts.Problems, err.Error())
					}
					addError(pr.Number, "details", err)
				}
				selected = SelectCandidate(policy, facts)
			}
			if selected.Status == "unknown" {
				// A reader can return incomplete facts without a transport error.
				if len(result.Errors) == 0 || result.Errors[len(result.Errors)-1].PR != pr.Number {
					addError(pr.Number, "selection", errors.New(strings.Join(selected.Reasons, "; ")))
				}
			}
		}
		if all || selected.Status == SelectionCandidate {
			result.PullRequests = append(result.PullRequests, PRListItem{PRInfo: pr, Selection: selected})
		}
	}
	if listErr == nil {
		result.OpenPRCount = &count
	}
	result.Complete = len(failures) == 0
	return result, errors.Join(failures...)
}
