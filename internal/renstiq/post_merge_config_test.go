package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPostMergeConditionsConfigShow(t *testing.T) {
	empty := Match{Files: []string{}, Dependencies: []string{}, Types: []string{}}
	for _, tc := range []struct {
		name    string
		fields  string
		match   Match
		exclude Match
	}{
		{"omitted", "", empty, empty},
		{"empty object", "  exclude: {}\n", empty, empty},
		{"empty arrays", "  exclude:\n    changed_files_any: []\n    dependencies: []\n    update_types: []\n", empty, empty},
		{"exclude only", "  exclude:\n    dependencies: [some-dev-tool]\n", empty, Match{Files: []string{}, Dependencies: []string{"some-dev-tool"}, Types: []string{}}},
		{
			"match and exclude",
			"  match:\n    changed_files_any: ['**']\n    dependencies: [app, some-dev-tool]\n    update_types: [patch, minor]\n  exclude:\n    changed_files_any: ['tools/**']\n    dependencies: [some-dev-tool]\n    update_types: [minor]\n",
			Match{Files: []string{"**"}, Dependencies: []string{"app", "some-dev-tool"}, Types: []string{"patch", "minor"}},
			Match{Files: []string{"tools/**"}, Dependencies: []string{"some-dev-tool"}, Types: []string{"minor"}},
		},
	} {
		for _, source := range []string{"repo", "common"} {
			t.Run(tc.name+"/"+source, func(t *testing.T) {
				dir := cliRepo(t, t.TempDir(), "repo", "https://github.com/o/r.git")
				common := filepath.Join(t.TempDir(), "common.yaml")
				body := "post_merge:\n- id: rebuild\n  timing: after_repo\n  command: [go, build, ./...]\n" + tc.fields
				if source == "common" {
					writeFile(t, common, "version: 1\ndefaults:\n  "+strings.ReplaceAll(strings.TrimSuffix(body, "\n"), "\n", "\n  ")+"\n")
					writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: true\n")
				} else {
					writeFile(t, common, "version: 1\n")
					writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: true\n"+body)
				}
				var out, log bytes.Buffer
				if code := RunCLI(context.Background(), []string{"config", "show", "--repo", dir, "--config", common}, nil, &out, &log); code != 0 {
					t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), log.String())
				}
				assertCLIOutputSchema(t, "config-show", out.Bytes())
				var result ConfigResult
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Config == nil || len(result.Config.PostMerge) != 1 {
					t.Fatalf("missing post-merge configuration: %s", out.String())
				}
				post := result.Config.PostMerge[0]
				if !reflect.DeepEqual(post.Match, tc.match) || !reflect.DeepEqual(post.Exclude, tc.exclude) {
					t.Fatalf("conditions lost in effective configuration: %+v", post)
				}
				if bytes.Contains(out.Bytes(), []byte(`"requires_review"`)) {
					t.Fatalf("removed option leaked into output: %s", out.String())
				}
				// A repository post_merge list replaces the common list, including exclusions.
				if source == "common" {
					writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\npost_merge:\n- id: replacement\n  timing: after_each_merge\n  command: [echo, done]\n")
					result, err := newApplication(&log).ConfigShow(context.Background(), ConfigRequest{Repo: dir, ConfigPath: common})
					if err != nil {
						t.Fatal(err)
					}
					if len(result.Config.PostMerge) != 1 || result.Config.PostMerge[0].ID != "replacement" || !reflect.DeepEqual(result.Config.PostMerge[0].Exclude, empty) {
						t.Fatalf("replacement inherited exclusions: %+v", result.Config.PostMerge)
					}
				}
			})
		}
	}
}

func TestPostMergeInvalidConditionsRejected(t *testing.T) {
	for _, tc := range []struct{ field, key string }{
		{"requires_review: true", "requires_review"},
		{"requires_review: false", "requires_review"},
		{"exclude: null", "exclude"},
		{"exclude: []", "exclude"},
		{"exclude: {unknown: []}", "unknown"},
		{"exclude: {changed_files_any: ['[']}", "exclude glob"},
		{"exclude: {changed_files_any: [1]}", "changed_files_any"},
		{"exclude: {dependencies: some-dev-tool}", "dependencies"},
		{"exclude: {dependencies: ['']}", "dependencies"},
		{"exclude: {update_types: null}", "update_types"},
		{"exclude: {update_types: [false]}", "update_types"},
		{"match: {changed_files_any: ['[']}", "match glob"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			dir := t.TempDir()
			body := "post_merge:\n- id: rebuild\n  timing: after_repo\n  command: [echo]\n  " + tc.field + "\n"
			writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: true\n"+body)
			if _, _, err := LoadPolicy(dir, DefaultConfig()); err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("invalid repo option must report %q: %v", tc.key, err)
			}
			common := filepath.Join(dir, "common.yaml")
			writeFile(t, common, "version: 1\ndefaults:\n  "+strings.ReplaceAll(strings.TrimSuffix(body, "\n"), "\n", "\n  ")+"\n")
			if _, err := LoadConfig(common); err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("invalid common option must report %q: %v", tc.key, err)
			}
		})
	}
}
