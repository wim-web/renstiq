package renstiq

import (
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const configVersion = 2

// Entry identifies one independently inheritable policy item. Inherit is an
// input-only directive; config show exposes the resolved item and its enabled state.
type Entry struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type Match struct {
	FilterIDs     []string `json:"filter_ids,omitempty"` // Only review.match accepts filter references.
	FilterIDsMode string   `json:"filter_ids_mode,omitempty"`
	Files         []string `json:"changed_files_any,omitempty"`
	Dependencies  []string `json:"dependencies,omitempty"`
	Types         []string `json:"update_types,omitempty"`
}

// Each filter is an alternative route into the candidate set (OR). Conditions
// within each filter are ANDed. Update types match any update; files, commit
// authors and dependency names must match all their corresponding PR values.
// Omitted allowlists impose no constraint; explicit empty allowlists allow none.
type Filter struct {
	Entry
	Authors       []string `json:"authors"`
	Labels        []string `json:"labels"`
	Bases         []string `json:"base_branches"`
	Heads         []string `json:"head_branches"`
	CommitAuthors []string `json:"commit_authors"`
	Files         []string `json:"files"`
	Dependencies  []string `json:"dependencies"`
	Types         []string `json:"update_types"`
}

type Instruction struct {
	Entry
	Match        Match  `json:"match"`
	Exclude      Match  `json:"exclude"`
	Instructions string `json:"instructions"`
}

type Policy struct {
	GitHubAPIReadRetry GitHubAPIReadRetry `json:"github_api_read_retry"`
	PullRequests       struct {
		Filters []Filter `json:"filters"`
	} `json:"pull_requests"`
	Merge struct {
		Method string `json:"method,omitempty"`
	} `json:"merge"`
	Review     []Instruction `json:"review"`
	OnBlocked  []Instruction `json:"on_blocked"`
	AfterMerge []Instruction `json:"after_merge"`
	AfterRepo  []Instruction `json:"after_repo"`
}

type GitHubAPIReadRetry struct {
	MaxAttempts       *int     `json:"max_attempts,omitempty"`
	IntervalSeconds   *float64 `json:"interval_seconds,omitempty"`
	RespectRetryAfter bool     `json:"respect_retry_after,omitempty"`
}

func validateGitHubAPIReadRetry(retry GitHubAPIReadRetry) error {
	bad := func(message string) error { return &InputError{fmt.Errorf("github_api_read_retry: %s", message)} }
	if retry.MaxAttempts == nil {
		if retry.IntervalSeconds != nil || retry.RespectRetryAfter {
			return bad("max_attempts is required when retry options are specified")
		}
		return nil
	}
	if *retry.MaxAttempts < 1 || *retry.MaxAttempts > 100 {
		return bad("max_attempts must be between 1 and 100")
	}
	if *retry.MaxAttempts > 1 && retry.IntervalSeconds == nil {
		return bad("interval_seconds is required when max_attempts is greater than 1; specify 0 for immediate retry")
	}
	if retry.IntervalSeconds != nil && (*retry.IntervalSeconds < 0 || *retry.IntervalSeconds > 86400) {
		return bad("interval_seconds must be between 0 and 86400")
	}
	return nil
}

var updateTypes = []string{"patch", "minor", "major", "digest", "pin", "pinDigest", "lockFileMaintenance", "lockfileUpdate", "replacement", "rollback", "bump"}

func validatePolicy(p Policy) error {
	bad := func(err error) error { return &InputError{err} }
	for _, f := range p.PullRequests.Filters {
		if err := validatePatterns(f.ID, f.Files, f.Heads); err != nil {
			return bad(err)
		}
		if err := validateTypes(f.ID, f.Types); err != nil {
			return bad(err)
		}
	}
	for _, group := range [][]Instruction{p.Review, p.OnBlocked, p.AfterMerge, p.AfterRepo} {
		for _, item := range group {
			if item.Enabled && strings.TrimSpace(item.Instructions) == "" {
				return bad(fmt.Errorf("%s: enabled instruction requires nonempty instructions", item.ID))
			}
			for _, m := range []Match{item.Match, item.Exclude} {
				if err := validatePatterns(item.ID, m.Files); err != nil {
					return bad(err)
				}
				if err := validateTypes(item.ID, m.Types); err != nil {
					return bad(err)
				}
			}
		}
	}
	return nil
}

func validateReviewReferences(p Policy) error {
	filterIDs := map[string]bool{}
	for _, f := range p.PullRequests.Filters {
		filterIDs[f.ID] = true
	}
	for _, item := range p.Review {
		for _, id := range item.Match.FilterIDs {
			if !filterIDs[id] {
				return &InputError{fmt.Errorf("review.%s.match.filter_ids: unknown filter id: %s", item.ID, id)}
			}
		}
	}
	return nil
}

func validatePatterns(id string, groups ...[]string) error {
	for _, patterns := range groups {
		for _, pattern := range patterns {
			if !doublestar.ValidatePattern(pattern) {
				return fmt.Errorf("%s: invalid glob: %s", id, pattern)
			}
		}
	}
	return nil
}

func validateTypes(id string, types []string) error {
	for _, typ := range types {
		if !contains(updateTypes, typ) {
			return fmt.Errorf("%s: unknown update type: %s", id, typ)
		}
	}
	return nil
}
