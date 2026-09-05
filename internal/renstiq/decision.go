package renstiq

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

type Evidence struct {
	Source  string `json:"source"`
	Finding string `json:"finding"`
}
type Update struct {
	Dependency string   `json:"dependency"`
	Type       string   `json:"update_type"`
	Files      []string `json:"files"`
	From       string   `json:"from,omitempty"`
	To         string   `json:"to,omitempty"`
}
type Feedback struct {
	Comment      string   `json:"comment,omitempty"`
	EquivalentID int64    `json:"equivalent_comment_id,omitempty"`
	UpdateID     int64    `json:"update_comment_id,omitempty"`
	Add          []string `json:"add_labels,omitempty"`
	Remove       []string `json:"remove_labels,omitempty"`
}
type PostChoice struct {
	ID     string `json:"id"`
	Needed bool   `json:"needed"`
	Reason string `json:"reason"`
}
type Decision struct {
	Version      int        `json:"version"`
	Repo         string     `json:"repo"`
	PR           int        `json:"pr"`
	HeadSHA      string     `json:"head_sha"`
	BaseSHA      string     `json:"base_sha"`
	ConfigDigest string     `json:"config_digest"`
	Decision     string     `json:"decision"`
	ReasonType   string     `json:"reason_type"`
	Reason       string     `json:"reason"`
	Evidence     []Evidence `json:"evidence"`
	Review       struct {
		InstructionsFollowed bool `json:"instructions_followed"`
		UpstreamChecked      bool `json:"upstream_checked"`
		UsageChecked         bool `json:"usage_checked"`
		NoUnresolvedRequests bool `json:"no_unresolved_requests"`
		Compatible           bool `json:"compatible"`
	} `json:"review"`
	Updates   []Update     `json:"updates"`
	Feedback  Feedback     `json:"feedback"`
	PostMerge []PostChoice `json:"post_merge"`
}

func ReadDecision(r io.Reader) (Decision, error) {
	var d Decision
	b, e := io.ReadAll(io.LimitReader(r, 4<<20))
	if e != nil {
		return d, e
	}
	var v any
	if e = json.Unmarshal(b, &v); e != nil {
		return d, e
	}
	if e = validateSchema("decision", v); e != nil {
		return d, e
	}
	e = json.Unmarshal(b, &d)
	return d, e
}
func (d Decision) Validate(repo, hash string, p Policy) error {
	if d.Repo != repo || d.ConfigDigest != hash {
		return errors.New("decision repository/config_digest mismatch; inspect and review again")
	}
	if !d.Review.InstructionsFollowed || !d.Review.UsageChecked {
		return errors.New("complete repository instruction and usage investigation before submitting a decision")
	}
	if d.Decision == "merge" && (!d.Review.UpstreamChecked || !d.Review.NoUnresolvedRequests || !d.Review.Compatible || len(d.Updates) == 0) {
		return errors.New("merge requires completed upstream/compatibility/human-request investigation and updates")
	}
	if d.ReasonType == "compatibility" && (d.Decision != "hold" || d.Review.Compatible) {
		return errors.New("compatibility issue must hold the PR")
	}
	if d.Decision == "merge" && d.ReasonType != "compatible" && d.ReasonType != "resolved" {
		return errors.New("merge reason_type must be compatible or resolved")
	}
	if d.Feedback.EquivalentID != 0 && d.Feedback.UpdateID != 0 {
		return errors.New("equivalent_comment_id and update_comment_id are mutually exclusive")
	}
	for _, l := range append(append([]string{}, d.Feedback.Add...), d.Feedback.Remove...) {
		if !contains(p.Feedback.Labels, l) {
			return fmt.Errorf("label is not configured: %s", l)
		}
	}
	for _, l := range d.Feedback.Add {
		if contains(d.Feedback.Remove, l) {
			return fmt.Errorf("label is both added and removed: %s", l)
		}
	}
	ids := map[string]bool{}
	for _, choice := range d.PostMerge {
		if ids[choice.ID] {
			return fmt.Errorf("duplicate post_merge choice: %s", choice.ID)
		}
		ids[choice.ID] = true
		found := false
		for _, a := range p.PostMerge {
			if a.ID == choice.ID {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unconfigured post_merge id: %s", choice.ID)
		}
	}
	if d.Decision == "merge" {
		for _, a := range p.PostMerge {
			if a.RequiresReview && !ids[a.ID] {
				return fmt.Errorf("post_merge need must be assessed: %s", a.ID)
			}
		}
	}
	return nil
}
func checkReasons(c Checks, checks []Check) []string {
	r := []string{}
	if len(checks) < c.Minimum {
		r = append(r, fmt.Sprintf("checks: need at least %d, got %d", c.Minimum, len(checks)))
	}
	for _, q := range c.Required {
		found := false
		for _, v := range checks {
			if v.Name == q.Name && (q.Workflow == "" || v.Workflow == q.Workflow) && (q.AppID == 0 || q.AppID == v.AppID) {
				found = true
				if v.Conclusion != "success" {
					r = append(r, "required check unsuccessful: "+q.Workflow+"/"+q.Name)
				}
			}
		}
		if !found {
			r = append(r, "required check missing: "+q.Workflow+"/"+q.Name)
		}
	}
	if c.AllSuccess {
		for _, v := range checks {
			if v.Conclusion != "success" {
				r = append(r, "check unsuccessful: "+v.Name+" ("+v.Status+"/"+v.Conclusion+")")
			}
		}
	}
	return r
}
func machineReasons(p Policy, pr PullRequest) []string {
	r := []string{}
	if pr.State != "open" {
		r = append(r, "PR is not open")
	}
	if !contains(p.PullRequests.Authors, pr.Author) {
		r = append(r, "author not allowed")
	}
	if !contains(p.PullRequests.Bases, pr.Base) {
		r = append(r, "base branch not allowed")
	}
	if len(p.PullRequests.Heads) > 0 && !matchAny(p.PullRequests.Heads, pr.Head) {
		r = append(r, "head branch not allowed")
	}
	if pr.Draft {
		r = append(r, "draft PR")
	}
	if pr.Mergeable == nil || !*pr.Mergeable {
		r = append(r, "PR is not known mergeable")
	}
	if pr.MergeState == "BLOCKED" || pr.MergeState == "DIRTY" || pr.MergeState == "UNKNOWN" {
		r = append(r, "merge state: "+pr.MergeState)
	}
	if p.Merge.RequireClean && pr.MergeState != "CLEAN" {
		r = append(r, "CLEAN merge state required")
	}
	if pr.ReviewDecision == "CHANGES_REQUESTED" || pr.ReviewDecision == "REVIEW_REQUIRED" {
		r = append(r, "review decision: "+pr.ReviewDecision)
	}
	if pr.UnresolvedThreads > 0 {
		r = append(r, "unresolved review threads")
	}
	for _, a := range pr.CommitAuthors {
		if len(p.PullRequests.CommitAuthors) > 0 && !contains(p.PullRequests.CommitAuthors, a) {
			r = append(r, "commit author not allowed: "+a)
		}
	}
	if len(p.PullRequests.Files) > 0 {
		for _, f := range pr.Files {
			if !matchAny(p.PullRequests.Files, f) {
				r = append(r, "file not allowed: "+f)
			}
		}
	}
	r = append(r, checkReasons(p.Checks, pr.Checks)...)
	return r
}
func policyReasons(p Policy, pr PullRequest, d Decision) []string {
	machinePolicy := p
	machinePolicy.Checks = Checks{}
	r := machineReasons(machinePolicy, pr)
	covered := map[string]bool{}
	for _, u := range d.Updates {
		for _, f := range u.Files {
			if !contains(pr.Files, f) {
				r = append(r, "update refers to unchanged file: "+f)
			}
			covered[f] = true
		}
		if len(p.Rules) == 0 {
			r = append(r, checkReasons(p.Checks, pr.Checks)...)
			continue
		}
		allowed := false
		for _, rule := range p.Rules {
			if !contains(rule.Types, u.Type) || (len(rule.Dependencies) > 0 && !contains(rule.Dependencies, u.Dependency)) {
				continue
			}
			all := true
			for _, f := range u.Files {
				if !matchAny(rule.Files, f) {
					all = false
				}
			}
			if !all {
				continue
			}
			allowed = true
			c := p.Checks
			if rule.Checks != nil {
				if rule.Checks.Minimum != nil {
					c.Minimum = *rule.Checks.Minimum
				}
				if rule.Checks.Required != nil {
					c.Required = rule.Checks.Required
				}
				if rule.Checks.AllSuccess != nil {
					c.AllSuccess = *rule.Checks.AllSuccess
				}
			}
			r = append(r, checkReasons(c, pr.Checks)...)
		}
		if !allowed {
			r = append(r, "no allow rule for "+u.Dependency+"/"+u.Type)
		}
	}
	for _, f := range pr.Files {
		if !covered[f] {
			r = append(r, "file missing from investigated updates: "+f)
		}
	}
	sort.Strings(r)
	return r
}
func selected(a PostCommand, m MergeRecord) bool {
	if len(a.Match.Files) > 0 {
		yes := false
		for _, f := range m.Files {
			if matchAny(a.Match.Files, f) {
				yes = true
			}
		}
		if !yes {
			return false
		}
	}
	if len(a.Match.Dependencies) > 0 || len(a.Match.Types) > 0 {
		yes := false
		for _, u := range m.Decision.Updates {
			if (len(a.Match.Dependencies) == 0 || contains(a.Match.Dependencies, u.Dependency)) && (len(a.Match.Types) == 0 || contains(a.Match.Types, u.Type)) {
				yes = true
			}
		}
		if !yes {
			return false
		}
	}
	if a.RequiresReview {
		for _, c := range m.Decision.PostMerge {
			if c.ID == a.ID {
				return c.Needed
			}
		}
		return false
	}
	return true
}
func feedbackBody(d Decision) string {
	body := d.Feedback.Comment
	if body == "" {
		body = d.Reason
	}
	if body != d.Reason {
		body += "\n\n保留・判断理由: " + d.Reason
	}
	body += "\n\n調査根拠:\n"
	for _, e := range d.Evidence {
		body += "- " + e.Source + ": " + e.Finding + "\n"
	}
	body = strings.TrimSpace(body)
	return body + "\n\n<!-- renstiq:v1:" + digest(body) + " -->"
}
