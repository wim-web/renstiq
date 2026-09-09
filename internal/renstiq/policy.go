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
	Files        []string `json:"changed_files_any,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Types        []string `json:"update_types,omitempty"`
}

// Each filter is an alternative route into the candidate set (OR). All
// conditions and all changed paths/updates within that filter must pass (AND).
// Omitted allowlists impose no constraint; explicit empty allowlists allow none.
type Filter struct {
	Entry
	Authors       []string `json:"authors"`
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
		LockLabel string   `json:"lock_label,omitempty"`
		Filters   []Filter `json:"filters"`
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
	MaxAttempts     int     `json:"max_attempts,omitempty"`
	IntervalSeconds float64 `json:"interval_seconds,omitempty"`
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
