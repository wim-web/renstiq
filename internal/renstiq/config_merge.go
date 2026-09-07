package renstiq

import (
	"fmt"
	"reflect"
	"strings"
)

func entryList(path string) bool {
	switch path {
	case "pull_requests.filters", "review", "on_blocked", "after_merge", "after_repo":
		return true
	}
	return false
}

// mergeConfig never mutates either input. Each source is processed once, so
// instructions and named entries are not appended again when loading a repo.
func mergeConfig(base, child map[string]any, path string, appendValues bool) (map[string]any, error) {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range child {
		if key == "inherit" {
			continue
		}
		next := key
		if path != "" {
			next = path + "." + key
		}
		if entryList(next) {
			a, _ := out[key].([]any)
			b, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("%s must be an array", next)
			}
			merged, err := mergeEntries(a, b, next)
			if err != nil {
				return nil, err
			}
			out[key] = merged
			continue
		}
		bm, isMap := value.(map[string]any)
		if isMap {
			am, _ := out[key].(map[string]any)
			merged, err := mergeConfig(am, bm, next, appendValues)
			if err != nil {
				return nil, err
			}
			out[key] = merged
		} else if values, ok := value.([]any); ok && appendValues {
			original, _ := out[key].([]any)
			merged := append([]any{}, original...)
			for _, v := range values {
				found := false
				for _, old := range merged {
					if reflect.DeepEqual(old, v) {
						found = true
						break
					}
				}
				if !found {
					merged = append(merged, v)
				}
			}
			out[key] = merged
		} else if key == "instructions" && appendValues {
			old, _ := out[key].(string)
			text, _ := value.(string)
			if old != "" && text != "" {
				out[key] = strings.TrimRight(old, "\r\n") + "\n\n" + strings.TrimLeft(text, "\r\n")
			} else {
				out[key] = value
			}
		} else {
			out[key] = value
		}
	}
	return out, nil
}

func mergeEntries(base, child []any, path string) ([]any, error) {
	out := append([]any{}, base...)
	positions := map[string]int{}
	for i, value := range out {
		positions[value.(map[string]any)["id"].(string)] = i
	}
	seen := map[string]bool{}
	for _, value := range child {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: expected an ID entry", path)
		}
		id, ok := item["id"].(string)
		if !ok || id == "" || seen[id] {
			return nil, fmt.Errorf("%s: missing or duplicate id: %s", path, id)
		}
		seen[id] = true
		mode, _ := item["inherit"].(string)
		if mode != "" && mode != "merge" && mode != "override" {
			return nil, fmt.Errorf("%s.%s: invalid inherit: %s", path, id, mode)
		}
		previous := map[string]any{}
		index, exists := positions[id]
		// A disabled entry is a tombstone, even without a full replacement body.
		if exists && (mode == "merge" || item["enabled"] == false) {
			previous = out[index].(map[string]any)
		}
		merged, err := mergeConfig(previous, item, path+"."+id, mode == "merge")
		if err != nil {
			return nil, err
		}
		if _, supplied := merged["enabled"]; !supplied {
			merged["enabled"] = true
		}
		if exists {
			out[index] = merged
		} else {
			positions[id] = len(out)
			out = append(out, merged)
		}
	}
	return out, nil
}

func resolvePolicy(common, repo map[string]any) (Policy, error) {
	// The common file is the root, not an overlay on an implicit policy.
	if common == nil {
		common = map[string]any{}
	}
	if err := validateSchema("config", map[string]any{"version": configVersion, "defaults": common}); err != nil {
		return Policy{}, &InputError{err}
	}
	base, err := mergeConfig(nil, common, "", false)
	if err == nil {
		base, err = mergeConfig(base, repo, "", false)
	}
	if err != nil {
		return Policy{}, &InputError{err}
	}
	var p Policy
	if err := decodeMap(base, &p); err != nil {
		return p, &InputError{err}
	}
	// Normalize collection representation only; do not introduce policy values.
	if p.PullRequests.Filters == nil {
		p.PullRequests.Filters = []Filter{}
	}
	if p.Review == nil {
		p.Review = []Instruction{}
	}
	if p.OnBlocked == nil {
		p.OnBlocked = []Instruction{}
	}
	if p.AfterMerge == nil {
		p.AfterMerge = []Instruction{}
	}
	if p.AfterRepo == nil {
		p.AfterRepo = []Instruction{}
	}
	return p, validatePolicy(p)
}
