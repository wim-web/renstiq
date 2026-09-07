package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewInstructionsInheritance(t *testing.T) {
	for _, tc := range []struct {
		name, common, repo, want, mode string
	}{
		{"default override", "instructions: common", "instructions: repo", "repo", "override"},
		{"explicit override", "instructions: common", "instructions_mode: override\ninstructions: repo", "repo", "override"},
		{"repo merge", "instructions: common", "instructions_mode: merge\ninstructions: repo", "common\n\nrepo", "merge"},
		{"common merge", "instructions_mode: merge\ninstructions: common", "instructions: repo", "common\n\nrepo", "merge"},
		{"repo overrides common merge", "instructions_mode: merge\ninstructions: common", "instructions_mode: override\ninstructions: repo", "repo", "override"},
		{"repo overrides common override", "instructions_mode: override\ninstructions: common", "instructions_mode: merge\ninstructions: repo", "common\n\nrepo", "merge"},
		{"omitted review", "instructions_mode: merge\ninstructions: common", "", "common", "merge"},
		{"mode without instructions", "instructions: common", "instructions_mode: merge", "common", "merge"},
		{"override without instructions", "instructions_mode: merge\ninstructions: common", "instructions_mode: override", "common", "override"},
		{"no common instructions", "", "instructions_mode: merge\ninstructions: repo", "repo", "merge"},
		{"no instructions", "", "instructions_mode: merge", "", "merge"},
		{"multiline YAML", "instructions: |\n  common line 1\n  common line 2\n", "instructions_mode: merge\ninstructions: |\n  repo line 1\n  repo line 2\n", "common line 1\ncommon line 2\n\nrepo line 1\nrepo line 2\n", "merge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			commonPath := filepath.Join(dir, "common.yaml")
			common := "version: 1\n"
			if tc.common != "" {
				common += "defaults:\n  review:\n    " + strings.ReplaceAll(tc.common, "\n", "\n    ") + "\n"
			}
			writeFile(t, commonPath, common)
			c, err := LoadConfig(commonPath)
			if err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(c.Defaults)
			if err != nil {
				t.Fatal(err)
			}
			repo := "version: 1\nenabled: true\n"
			if tc.repo != "" {
				repo += "review:\n  " + strings.ReplaceAll(tc.repo, "\n", "\n  ") + "\n"
			}
			writeFile(t, filepath.Join(dir, "renstiq.yaml"), repo)
			// Reusing common configuration must not accumulate repository instructions.
			for i := 0; i < 2; i++ {
				p, enabled, err := LoadPolicy(dir, c)
				if err != nil {
					t.Fatal(err)
				}
				if !enabled || p.Review.Instructions != tc.want || p.Review.InstructionsMode != tc.mode {
					t.Fatalf("got %+v (enabled=%v), want instructions=%q mode=%q", p.Review, enabled, tc.want, tc.mode)
				}
			}
			after, err := json.Marshal(c.Defaults)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("common configuration changed: %s -> %s, %v", before, after, err)
			}
		})
	}
}

func TestReviewInstructionsInvalidMode(t *testing.T) {
	for _, value := range []string{"append", "''", "null", "true", "[]"} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nreview:\n  instructions_mode: "+value+"\n")
			if _, _, err := LoadPolicy(dir, DefaultConfig()); err == nil || !strings.Contains(err.Error(), "instructions_mode") {
				t.Fatalf("invalid repository mode accepted or incorrect error: %v", err)
			}
			common := filepath.Join(dir, "common.yaml")
			writeFile(t, common, "version: 1\ndefaults:\n  review:\n    instructions_mode: "+value+"\n")
			if _, err := LoadConfig(common); err == nil || !strings.Contains(err.Error(), "instructions_mode") {
				t.Fatalf("invalid common mode accepted or incorrect error: %v", err)
			}
		})
	}
}

func TestConfigShowMergedReviewInstructions(t *testing.T) {
	dir := cliRepo(t, t.TempDir(), "repo", "https://github.com/o/r.git")
	common := filepath.Join(t.TempDir(), "common.yaml")
	writeFile(t, common, "version: 1\ndefaults:\n  review:\n    instructions_mode: merge\n    instructions: common\n")
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: true\nreview:\n  instructions: repo\n")
	var out, log bytes.Buffer
	if code := newCLI(newApplication(&log), nil).Run(context.Background(), []string{"config", "show", "--repo", dir, "--config", common}, nil, &out, &log); code != 0 {
		t.Fatal(code, out.String(), log.String())
	}
	var result ConfigResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Config == nil || result.Config.Review.Instructions != "common\n\nrepo" || result.Config.Review.InstructionsMode != "merge" {
		t.Fatalf("unexpected effective configuration: %s", out.String())
	}
	assertCLIOutputSchema(t, "config-show", out.Bytes())
}
