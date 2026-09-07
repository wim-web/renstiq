package renstiq

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// Renovate's default PR table supplies updateType, which can differ from a
// SemVer comparison (pin, digest, rollback, replacement, non-SemVer schemes).
// Never infer the type from a PR title, branch name, or an AI interpretation.
// See https://docs.renovatebot.com/configuration-options/#prbodycolumns.
type DependencyUpdate struct {
	Dependency string `json:"dependency"`
	Type       string `json:"update_type"`
}

var markdownLink = regexp.MustCompile(`\[([^\[\]]*)\]\([^\n]*?\)`)
var htmlTag = regexp.MustCompile(`<[^>]*>`)
var htmlRow = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
var htmlCell = regexp.MustCompile(`(?is)<t[dh]\b[^>]*>(.*?)</t[dh]>`)

func plainCell(s string) string {
	s = markdownLink.ReplaceAllString(s, "$1")
	s = htmlTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.NewReplacer("`", "", "\\_", "_", "\\*", "*", "\\|", "|", "\\[", "[", "\\]", "]").Replace(s)
	return strings.TrimSpace(s)
}

// Split pipes only outside code spans, link destinations, and escapes.
func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") {
		return nil
	}
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") {
		line = line[:len(line)-1]
	}
	var cells []string
	start, parens := 0, 0
	code, escaped := false, false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '`' {
			code = !code
		}
		if !code {
			if r == '(' {
				parens++
			}
			if r == ')' && parens > 0 {
				parens--
			}
		}
		if r == '|' && !code && parens == 0 {
			cells = append(cells, line[start:i])
			start = i + 1
		}
	}
	return append(cells, line[start:])
}

func renovateUpdates(body string) ([]DependencyUpdate, error) {
	// Dependency data is in the summary, not in release notes or fenced code
	// examples copied from upstream documentation.
	var summary []string
	fence := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[0]
			n := 0
			for n < len(trimmed) && trimmed[n] == marker {
				n++
			}
			fence = trimmed[:n]
			continue
		}
		if trimmed == "### Release Notes" || trimmed == "### Configuration" {
			break
		}
		summary = append(summary, line)
	}
	body = strings.Join(summary, "\n")
	// HTML tables are also accepted when the PR body uses Renovate's HTML format.
	var rows [][]string
	for _, line := range strings.Split(body, "\n") {
		rows = append(rows, tableCells(line))
	}
	for _, row := range htmlRow.FindAllStringSubmatch(body, -1) {
		cells := []string{}
		for _, cell := range htmlCell.FindAllStringSubmatch(row[1], -1) {
			cells = append(cells, cell[1])
		}
		rows = append(rows, cells)
	}
	updates := []DependencyUpdate{}
	packageCol, updateCol, width := -1, -1, 0
	found := false
	for _, row := range rows {
		if len(row) == 0 {
			packageCol, updateCol = -1, -1
			continue
		}
		pc, uc := -1, -1
		for i, cell := range row {
			switch strings.ToLower(plainCell(cell)) {
			case "package":
				pc = i
			case "update":
				uc = i
			}
		}
		if pc >= 0 {
			// A package table without Update is incomplete even if another table
			// provides usable rows. Do not silently accept only part of a group.
			if uc < 0 {
				return nil, fmt.Errorf("Renovate Package table has no Update column")
			}
			packageCol, updateCol, width = pc, uc, len(row)
			found = true
			continue
		}
		if packageCol < 0 {
			continue
		}
		separator := true
		for _, cell := range row {
			if !strings.Contains(cell, "-") || strings.Trim(strings.TrimSpace(cell), ":-") != "" {
				separator = false
			}
		}
		if separator {
			continue
		}
		if len(row) != width {
			return nil, fmt.Errorf("Renovate Package table contains an incomplete row")
		}
		name, typ := plainCell(row[packageCol]), plainCell(row[updateCol])
		if name == "" || !contains(updateTypes, typ) {
			return nil, fmt.Errorf("Renovate Package table has missing dependency or unsupported Update value: %q", typ)
		}
		// Replacement rows name both old and new packages. Both are subject to
		// an explicit dependency allowlist and can select review instructions.
		for _, dep := range strings.Split(name, " → ") {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				return nil, fmt.Errorf("Renovate replacement has an empty package name")
			}
			u := DependencyUpdate{Dependency: dep, Type: typ}
			duplicate := false
			for _, old := range updates {
				if old == u {
					duplicate = true
					break
				}
			}
			if !duplicate {
				updates = append(updates, u)
			}
		}
	}
	if !found || len(updates) == 0 {
		return nil, fmt.Errorf("Renovate Package/Update table is missing or empty")
	}
	return updates, nil
}
