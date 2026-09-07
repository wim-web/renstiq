package renstiq

// PRInfo contains only fields needed for selection and the handoff to AI.
type PRInfo struct {
	Number          int                `json:"number"`
	Title           string             `json:"title"`
	URL             string             `json:"url"`
	Author          string             `json:"author"`
	Base            string             `json:"base_branch"`
	Head            string             `json:"head_branch"`
	HeadSHA         string             `json:"head_sha"`
	BaseSHA         string             `json:"base_sha"`
	Draft           bool               `json:"draft"`
	Labels          []string           `json:"labels"`
	Updates         []DependencyUpdate `json:"updates"`
	UpdatesComplete bool               `json:"updates_complete"`
	State           string             `json:"-"`
	Body            string             `json:"-"`
	LabelsKnown     bool               `json:"-"`
	MetadataError   string             `json:"-"`
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
	Status         SelectionStatus `json:"selection"`
	ReviewIDs      []string        `json:"review_ids"`
	ReviewRequired []string        `json:"review_required"`
	Reasons        []string        `json:"reasons"`
}

func isRenovate(author string) bool { return author == "renovate[bot]" || author == "app/renovate" }
func needsFiles(p Policy) bool {
	for _, f := range p.PullRequests.Filters {
		if f.Enabled && f.Files != nil {
			return true
		}
	}
	for _, i := range p.Review {
		if i.Enabled && (len(i.Match.Files) > 0 || len(i.Exclude.Files) > 0) {
			return true
		}
	}
	return false
}
func needsCommits(p Policy) bool {
	for _, f := range p.PullRequests.Filters {
		if f.Enabled && f.CommitAuthors != nil {
			return true
		}
	}
	return false
}
func needsUpdates(p Policy) bool {
	for _, f := range p.PullRequests.Filters {
		if f.Enabled && (f.Dependencies != nil || f.Types != nil) {
			return true
		}
	}
	for _, i := range p.Review {
		if i.Enabled && (len(i.Match.Dependencies)+len(i.Match.Types)+len(i.Exclude.Dependencies)+len(i.Exclude.Types) > 0) {
			return true
		}
	}
	return false
}

func SelectCandidate(p Policy, f CandidateFacts) Selection {
	result := Selection{Status: SelectionCandidate, ReviewIDs: []string{}, ReviewRequired: []string{"compatibility", "checks", "human_requests", "mergeability"}, Reasons: []string{}}
	exclude := func(reason string) {
		result.Status = SelectionExcluded
		result.Reasons = append(result.Reasons, reason)
	}
	unknown := func(reason string) { result.Status = SelectionUnknown; result.Reasons = append(result.Reasons, reason) }
	if f.Changed {
		unknown("PR changed during retrieval")
		return result
	}
	pr := f.PR
	if pr.State != "" && pr.State != "open" {
		exclude("PR is not open")
	}
	if pr.Author != "" && !isRenovate(pr.Author) {
		exclude("PR is not authored by Renovate")
	}
	if p.PullRequests.LockLabel != "" && contains(pr.Labels, p.PullRequests.LockLabel) {
		exclude("locked: " + p.PullRequests.LockLabel)
	}
	for _, rule := range p.PullRequests.Filters {
		if !rule.Enabled {
			continue
		}
		if pr.Author != "" && rule.Authors != nil && !contains(rule.Authors, pr.Author) {
			exclude(rule.ID + ": author not allowed")
		}
		if pr.Base != "" && rule.Bases != nil && !contains(rule.Bases, pr.Base) {
			exclude(rule.ID + ": base branch not allowed")
		}
		if pr.Head != "" && rule.Heads != nil && !matchAny(rule.Heads, pr.Head) {
			exclude(rule.ID + ": head branch not allowed")
		}
	}
	if result.Status == SelectionExcluded {
		return result
	}
	if pr.Number <= 0 || pr.State == "" || pr.Author == "" || pr.Base == "" || pr.Head == "" || pr.HeadSHA == "" || pr.BaseSHA == "" {
		unknown("required PR information is missing")
	}
	if p.PullRequests.LockLabel != "" && !pr.LabelsKnown {
		unknown("PR label list is missing")
	}
	for _, problem := range f.Problems {
		unknown(problem)
	}
	if needsFiles(p) && !f.FilesComplete {
		unknown("changed file list is incomplete")
	}
	if needsUpdates(p) && (!pr.UpdatesComplete || len(pr.Updates) == 0) {
		reason := pr.MetadataError
		if reason == "" {
			reason = "Renovate update information is incomplete"
		}
		unknown(reason)
	}
	if needsCommits(p) {
		if !f.CommitsComplete {
			unknown("commit list is incomplete")
		}
		for _, author := range f.CommitAuthors {
			if author == "" {
				unknown("commit author is unknown")
			}
		}
	}
	if result.Status == SelectionUnknown {
		return result
	}
	for _, rule := range p.PullRequests.Filters {
		if !rule.Enabled {
			continue
		}
		if rule.Files != nil {
			if len(rule.Files) == 0 {
				exclude(rule.ID + ": empty file allowlist")
			}
			for _, path := range changedPaths(f.Files) {
				if !matchAny(rule.Files, path) {
					exclude(rule.ID + ": file not allowed: " + path)
				}
			}
		}
		if rule.CommitAuthors != nil {
			if len(rule.CommitAuthors) == 0 {
				exclude(rule.ID + ": empty commit author allowlist")
			}
			for _, author := range f.CommitAuthors {
				if !contains(rule.CommitAuthors, author) {
					exclude(rule.ID + ": commit author not allowed: " + author)
				}
			}
		}
		for _, update := range pr.Updates {
			if rule.Dependencies != nil && !contains(rule.Dependencies, update.Dependency) {
				exclude(rule.ID + ": dependency not allowed: " + update.Dependency)
			}
			if rule.Types != nil && !contains(rule.Types, update.Type) {
				exclude(rule.ID + ": update type not allowed: " + update.Type)
			}
		}
	}
	if result.Status == SelectionCandidate {
		for _, item := range p.Review {
			if instructionMatches(item, f) {
				result.ReviewIDs = append(result.ReviewIDs, item.ID)
			}
		}
	}
	return result
}

func changedPaths(files []ChangedFile) []string {
	paths := []string{}
	for _, f := range files {
		paths = append(paths, f.Filename)
		if f.Previous != "" {
			paths = append(paths, f.Previous)
		}
	}
	return paths
}
func emptyMatch(m Match) bool { return len(m.Files)+len(m.Dependencies)+len(m.Types) == 0 }
func matchUpdate(m Match, paths []string, update DependencyUpdate) bool {
	if len(m.Files) > 0 {
		found := false
		for _, path := range paths {
			if matchAny(m.Files, path) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return (len(m.Dependencies) == 0 || contains(m.Dependencies, update.Dependency)) && (len(m.Types) == 0 || contains(m.Types, update.Type))
}

// File conditions refer to the PR's changed paths; dependency/type conditions
// refer to one update. Exclusions remove updates, rather than vetoing a group.
func instructionMatches(item Instruction, f CandidateFacts) bool {
	if !item.Enabled {
		return false
	}
	updates := f.PR.Updates
	if len(updates) == 0 {
		updates = []DependencyUpdate{{}}
	}
	paths := changedPaths(f.Files)
	for _, update := range updates {
		if matchUpdate(item.Match, paths, update) && (emptyMatch(item.Exclude) || !matchUpdate(item.Exclude, paths, update)) {
			return true
		}
	}
	return false
}
