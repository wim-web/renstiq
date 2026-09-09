package renstiq

import (
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const configVersion = 2

// Entry identifies a repository-local rule or follow-up task.
type Entry struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type Match struct {
	Files        []string `json:"changed_files_any,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Types        []string `json:"update_types,omitempty"`
}

// Rules are evaluated in source order. The first match supplies the complete
// instruction; an unknown earlier rule must be resolved before later rules.
type Rule struct {
	Entry
	Authors       []string `json:"authors"`
	Labels        []string `json:"labels"`
	Bases         []string `json:"base_branches"`
	Heads         []string `json:"head_branches"`
	CommitAuthors []string `json:"commit_authors"`
	Files         []string `json:"files"`
	Dependencies  []string `json:"dependencies"`
	Types         []string `json:"update_types"`
	Instructions  string   `json:"instructions"`
}

type Instruction struct {
	Entry
	Match        Match  `json:"match"`
	Exclude      Match  `json:"exclude"`
	Instructions string `json:"instructions"`
}

type Policy struct {
	GitHubAPIReadRetry GitHubAPIReadRetry `json:"github_api_read_retry"`
	Rules              []Rule             `json:"rules"`
	Merge              struct {
		Method string `json:"method,omitempty"`
	} `json:"merge"`
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
	for _, f := range p.Rules {
		if f.Enabled && strings.TrimSpace(f.Instructions) == "" {
			return bad(fmt.Errorf("rules.%s: enabled rule requires nonempty instructions", f.ID))
		}
		if err := validatePatterns(f.ID, f.Files, f.Heads); err != nil {
			return bad(err)
		}
		if err := validateTypes(f.ID, f.Types); err != nil {
			return bad(err)
		}
	}
	for _, group := range [][]Instruction{p.OnBlocked, p.AfterMerge, p.AfterRepo} {
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
	return validateGitHubAPIReadRetry(p.GitHubAPIReadRetry)
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
