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
	Status    SelectionStatus       `json:"selection"`
	Review    []ResolvedInstruction `json:"review"`
	ReviewIDs []string              `json:"review_ids"`
	Reasons   []string              `json:"reasons"`
}

// ResolvedInstruction contains the effective text, with all applicability and
// inheritance decisions already made by the CLI.
type ResolvedInstruction struct {
	ID           string `json:"id"`
	Instructions string `json:"instructions"`
}

func instructionNeedsFiles(item Instruction) bool {
	return len(item.Match.Files) > 0 || len(item.Exclude.Files) > 0
}
func instructionNeedsUpdates(item Instruction) bool {
	return len(item.Match.Dependencies)+len(item.Match.Types)+len(item.Exclude.Dependencies)+len(item.Exclude.Types) > 0
}
func reviewNeedsFiles(p Policy, f CandidateFacts) bool {
	for _, item := range p.Review {
		if status, _ := reviewFilterStatus(p, item, f); status != SelectionExcluded && instructionNeedsFiles(item) {
			return true
		}
	}
	return false
}
func reviewNeedsUpdates(p Policy, f CandidateFacts) bool {
	for _, item := range p.Review {
		if status, _ := reviewFilterStatus(p, item, f); status != SelectionExcluded && instructionNeedsUpdates(item) {
			return true
		}
	}
	return false
}

// Filter entries are alternative ways to admit the entire PR. Conditions within
// one entry are ANDed; facts from different entries cannot be combined to pass it.
func selectFilter(rule Filter, f CandidateFacts) (SelectionStatus, []string) {
	var excluded, unknown []string
	deny := func(reason string) { excluded = append(excluded, rule.ID+": "+reason) }
	missing := func(reason string) { unknown = append(unknown, rule.ID+": "+reason) }
	pr := f.PR
	for _, field := range []struct {
		name   string
		values []string
	}{
		{"author", rule.Authors}, {"label", rule.Labels}, {"base branch", rule.Bases}, {"head branch", rule.Heads},
		{"file", rule.Files}, {"commit author", rule.CommitAuthors},
		{"dependency", rule.Dependencies}, {"update type", rule.Types},
	} {
		if field.values != nil && len(field.values) == 0 {
			deny("empty " + field.name + " allowlist")
		}
	}
	if pr.Author != "" && len(rule.Authors) > 0 && !contains(rule.Authors, pr.Author) {
		deny("author not allowed")
	}
	if len(rule.Labels) > 0 {
		if !pr.LabelsKnown {
			missing("PR label list is missing or incomplete")
		} else {
			matched := false
			for _, label := range pr.Labels {
				if contains(rule.Labels, label) {
					matched = true
					break
				}
			}
			if !matched {
				deny("no allowed label is present")
			}
		}
	}
	if pr.Base != "" && len(rule.Bases) > 0 && !contains(rule.Bases, pr.Base) {
		deny("base branch not allowed")
	}
	if pr.Head != "" && len(rule.Heads) > 0 && !matchAny(rule.Heads, pr.Head) {
		deny("head branch not allowed")
	}
	if len(rule.Files) > 0 {
		if !f.FilesComplete {
			missing("changed file list is incomplete")
		} else {
			for _, path := range changedPaths(f.Files) {
				if !matchAny(rule.Files, path) {
					deny("file not allowed: " + path)
				}
			}
		}
	}
	if len(rule.CommitAuthors) > 0 {
		if !f.CommitsComplete {
			missing("commit list is incomplete")
		} else {
			for _, author := range f.CommitAuthors {
				if author == "" {
					missing("commit author is unknown")
				} else if !contains(rule.CommitAuthors, author) {
					deny("commit author not allowed: " + author)
				}
			}
		}
	}
	if len(rule.Dependencies) > 0 || len(rule.Types) > 0 {
		if !pr.UpdatesComplete || len(pr.Updates) == 0 {
			reason := pr.MetadataError
			if reason == "" {
				reason = "Renovate update information is incomplete"
			}
			missing(reason)
		} else {
			for _, update := range pr.Updates {
				if len(rule.Dependencies) > 0 && !contains(rule.Dependencies, update.Dependency) {
					deny("dependency not allowed: " + update.Dependency)
				}
				if len(rule.Types) > 0 && !contains(rule.Types, update.Type) {
					deny("update type not allowed: " + update.Type)
				}
			}
		}
	}
	// A known mismatch makes this AND entry false even if another condition is unknown.
	if len(excluded) > 0 {
		return SelectionExcluded, excluded
	}
	if len(unknown) > 0 {
		return SelectionUnknown, unknown
	}
	return SelectionCandidate, nil
}

func selectFilters(p Policy, f CandidateFacts) (SelectionStatus, []string) {
	status := SelectionExcluded
	var reasons []string
	enabled := false
	for _, rule := range p.PullRequests.Filters {
		if !rule.Enabled {
			continue
		}
		enabled = true
		selected, why := selectFilter(rule, f)
		if selected == SelectionCandidate {
			return SelectionCandidate, nil
		}
		if selected == SelectionUnknown {
			status = SelectionUnknown
		}
		reasons = append(reasons, why...)
	}
	// No enabled filters means no additional restriction, as with an omitted list.
	if !enabled {
		return SelectionCandidate, nil
	}
	return status, reasons
}

// Review references are ORed independently of candidate selection. A disabled
// filter cannot match; a successful reference resolves unknown alternatives.
func reviewFilterStatus(p Policy, item Instruction, f CandidateFacts) (SelectionStatus, []string) {
	if !item.Enabled {
		return SelectionExcluded, nil
	}
	if len(item.Match.FilterIDs) == 0 {
		return SelectionCandidate, nil
	}
	// A review already ruled out by its other conditions needs no filter reads.
	if (!instructionNeedsFiles(item) || f.FilesComplete) &&
		(!instructionNeedsUpdates(item) || (f.PR.UpdatesComplete && len(f.PR.Updates) > 0)) &&
		!instructionMatches(item, f) {
		return SelectionExcluded, nil
	}
	status := SelectionExcluded
	var reasons []string
	for _, rule := range p.PullRequests.Filters {
		if !rule.Enabled || !contains(item.Match.FilterIDs, rule.ID) {
			continue
		}
		selected, why := selectFilter(rule, f)
		if selected == SelectionCandidate {
			return SelectionCandidate, nil
		}
		if selected == SelectionUnknown {
			status = SelectionUnknown
			for _, reason := range why {
				reasons = append(reasons, "review "+item.ID+": "+reason)
			}
		}
	}
	return status, reasons
}

// A selected PR may still need facts for filters referenced by its reviews.
// Unreferenced alternatives do not require extra reads once selection succeeds.
func detailRequirements(p Policy, f CandidateFacts) (files, commits bool) {
	files = reviewNeedsFiles(p, f) && !f.FilesComplete
	references := map[string]bool{}
	for _, item := range p.Review {
		if status, _ := reviewFilterStatus(p, item, f); status == SelectionUnknown {
			for _, id := range item.Match.FilterIDs {
				references[id] = true
			}
		}
	}
	selected, _ := selectFilters(p, f)
	for _, rule := range p.PullRequests.Filters {
		if !rule.Enabled || (selected == SelectionCandidate && !references[rule.ID]) {
			continue
		}
		if selected, _ := selectFilter(rule, f); selected == SelectionUnknown {
			files = files || (len(rule.Files) > 0 && !f.FilesComplete)
			commits = commits || (len(rule.CommitAuthors) > 0 && !f.CommitsComplete)
		}
	}
	return files, commits
}

func SelectCandidate(p Policy, f CandidateFacts) Selection {
	result := Selection{Status: SelectionCandidate, Review: []ResolvedInstruction{}, ReviewIDs: []string{}, Reasons: []string{}}
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
	if result.Status == SelectionExcluded {
		return result
	}
	if pr.Number <= 0 || pr.State == "" || pr.Author == "" || pr.Base == "" || pr.Head == "" || pr.HeadSHA == "" || pr.BaseSHA == "" {
		unknown("required PR information is missing")
	}
	for _, problem := range f.Problems {
		unknown(problem)
	}
	if result.Status == SelectionUnknown {
		return result
	}
	if filtered, reasons := selectFilters(p, f); filtered != SelectionCandidate {
		result.Status, result.Reasons = filtered, reasons
		return result
	}
	// Matching any filter does not waive the inputs needed to determine every review instruction.
	if reviewNeedsFiles(p, f) && !f.FilesComplete {
		unknown("changed file list is incomplete for review")
	}
	if reviewNeedsUpdates(p, f) && (!pr.UpdatesComplete || len(pr.Updates) == 0) {
		reason := pr.MetadataError
		if reason == "" {
			reason = "Renovate update information is incomplete"
		}
		unknown("review: " + reason)
	}
	applicable := []Instruction{}
	for _, item := range p.Review {
		status, reasons := reviewFilterStatus(p, item, f)
		if status == SelectionUnknown {
			for _, reason := range reasons {
				unknown(reason)
			}
		} else if status == SelectionCandidate {
			applicable = append(applicable, item)
		}
	}
	if result.Status == SelectionCandidate {
		for _, item := range applicable {
			if instructionMatches(item, f) {
				result.ReviewIDs = append(result.ReviewIDs, item.ID)
				result.Review = append(result.Review, ResolvedInstruction{ID: item.ID, Instructions: item.Instructions})
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
