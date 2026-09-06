package renstiq

// PRInfo contains only fields needed for selection and the handoff to AI.
type PRInfo struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Author  string `json:"author"`
	Base    string `json:"base_branch"`
	Head    string `json:"head_branch"`
	HeadSHA string `json:"head_sha"`
	BaseSHA string `json:"base_sha"`
	Draft   bool   `json:"draft"`
	State   string `json:"-"`
}
type ChangedFile struct {
	Filename string `json:"filename"`
	Previous string `json:"previous_filename,omitempty"`
	Status   string `json:"status"`
}
type CandidateFacts struct {
	PR              PRInfo
	Files           []ChangedFile
	FilesComplete   bool
	CommitAuthors   []string
	CommitsComplete bool
	Changed         bool
	Problems        []string
}
type SelectionStatus string

const (
	SelectionCandidate SelectionStatus = "candidate"
	SelectionExcluded  SelectionStatus = "excluded"
	SelectionUnknown   SelectionStatus = "unknown"
)

type Selection struct {
	Status           SelectionStatus `json:"selection"`
	CandidateRuleIDs []string        `json:"candidate_rule_ids"`
	ReviewRequired   []string        `json:"review_required"`
	Reasons          []string        `json:"reasons"`
}

func isRenovate(author string) bool { return author == "renovate[bot]" || author == "app/renovate" }
func needsFiles(p Policy) bool      { return len(p.PullRequests.Files) > 0 || len(p.Rules) > 0 }

// SelectCandidate only evaluates facts supplied by the reader, without I/O or
// interpreting dependency names, update types, checks, or review instructions.
func SelectCandidate(p Policy, f CandidateFacts) Selection {
	result := Selection{Status: SelectionCandidate, CandidateRuleIDs: []string{}, ReviewRequired: []string{"update_type", "dependency", "checks", "compatibility", "human_requests", "mergeability"}, Reasons: []string{}}
	if len(p.PostMerge) > 0 {
		result.ReviewRequired = append(result.ReviewRequired, "post_merge")
	}
	unknown := func(reason string) { result.Status = SelectionUnknown; result.Reasons = append(result.Reasons, reason) }
	exclude := func(reason string) {
		result.Status = SelectionExcluded
		result.Reasons = append(result.Reasons, reason)
	}
	if f.Changed {
		unknown("PR head, base, or state changed during retrieval")
		return result
	}
	pr := f.PR
	if pr.State != "" && pr.State != "open" {
		exclude("PR is not open")
	}
	if pr.Author != "" && (!isRenovate(pr.Author) || !contains(p.PullRequests.Authors, pr.Author)) {
		exclude("author not allowed")
	}
	if pr.Base != "" && !contains(p.PullRequests.Bases, pr.Base) {
		exclude("base branch not allowed")
	}
	if pr.Head != "" && len(p.PullRequests.Heads) > 0 && !matchAny(p.PullRequests.Heads, pr.Head) {
		exclude("head branch not allowed")
	}
	if result.Status == SelectionExcluded {
		return result
	}
	if pr.Number <= 0 || pr.State == "" || pr.Author == "" || pr.Base == "" || pr.Head == "" || pr.HeadSHA == "" || pr.BaseSHA == "" {
		unknown("required PR information is missing")
	}
	for _, problem := range f.Problems {
		unknown(problem)
	}
	if needsFiles(p) && !f.FilesComplete {
		unknown("changed file list is incomplete")
	}
	if len(p.PullRequests.CommitAuthors) > 0 {
		if !f.CommitsComplete {
			unknown("commit list is incomplete")
		}
		for _, author := range f.CommitAuthors {
			if author == "" {
				unknown("commit author is unknown")
			}
		}
	}
	// Incomplete required data must never turn a partial list into an exclusion.
	if result.Status == SelectionUnknown {
		return result
	}
	related := map[string]bool{}
	for _, file := range f.Files {
		paths := []string{file.Filename}
		if file.Previous != "" {
			paths = append(paths, file.Previous)
		}
		for _, path := range paths {
			if len(p.PullRequests.Files) > 0 && !matchAny(p.PullRequests.Files, path) {
				exclude("file not allowed: " + path)
			}
			covered := len(p.Rules) == 0
			for _, rule := range p.Rules {
				if matchAny(rule.Files, path) {
					covered = true
					related[rule.ID] = true
				}
			}
			if !covered {
				exclude("file not covered by any rule: " + path)
			}
		}
	}
	for _, rule := range p.Rules {
		if related[rule.ID] {
			result.CandidateRuleIDs = append(result.CandidateRuleIDs, rule.ID)
		}
	}
	if len(p.PullRequests.CommitAuthors) > 0 {
		for _, author := range f.CommitAuthors {
			if !contains(p.PullRequests.CommitAuthors, author) {
				exclude("commit author not allowed: " + author)
			}
		}
	}
	return result
}
