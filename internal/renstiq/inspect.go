package renstiq

import (
	"context"
	"errors"
	"fmt"
)

type InspectionReader interface {
	PRReader
	PRStateReader
	OpenPullRequests(context.Context, string) ([]PRState, error)
}

type Inspector struct{ Remote InspectionReader }

func (i Inspector) Inspect(ctx context.Context, repo string, policy Policy, number int) ([]PullRequest, error) {
	candidates, err := i.candidates(ctx, repo, number)
	if err != nil {
		return nil, err
	}
	var prs []PullRequest
	var errs []error
	for _, candidate := range candidates {
		// Keep all Renovate PRs even when an author policy excludes them.
		if number == 0 && !contains([]string{"app/renovate", "renovate[bot]"}, candidate.Author) && !contains(policy.PullRequests.Authors, candidate.Author) {
			continue
		}
		pr, err := i.Remote.WaitPR(ctx, repo, candidate.Number, Revision{}, true)
		if err != nil {
			errs = append(errs, fmt.Errorf("PR #%d: %w", candidate.Number, err))
			pr.Number = candidate.Number
			pr.Reasons = append(pr.Reasons, err.Error())
		} else {
			pr.Reasons = machineReasons(policy, pr)
		}
		prs = append(prs, pr)
	}
	return prs, errors.Join(errs...)
}

func (i Inspector) candidates(ctx context.Context, repo string, number int) ([]PRState, error) {
	if number == 0 {
		return i.Remote.OpenPullRequests(ctx, repo)
	}
	pr, err := i.Remote.PullRequestState(ctx, repo, number)
	if err != nil {
		return nil, err
	}
	return []PRState{pr}, nil
}
