package renstiq

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/bmatcuk/doublestar/v4"
)

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0644); e != nil {
		t.Fatal(e)
	}
}
func TestStrictConfig(t *testing.T) {
	for _, s := range []string{"version: 1\nenabled: 'true'\n", "version: 1\nenabled: true\nunknown: x\n", "version: 1\nenabled: true\nchecks:\n  minimum: '1'\n", "version: 1\nenabled: true\nenabled: false\n", "version: 1\nenabled: true\n---\nversion: 1\n", "version: 1\nenabled: null\n", "version: 1\nenabled: true\npost_merge:\n- id: a\n  timing: after_repo\n  command: [echo]\n  retry: 2\n"} {
		t.Run(s, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "renstiq.yaml"), s)
			if _, _, e := LoadPolicy(dir, DefaultConfig()); e == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
func TestInheritance(t *testing.T) {
	dir := t.TempDir()
	c := DefaultConfig()
	c.Defaults = map[string]any{"checks": map[string]any{"minimum": float64(1), "all_success": true}, "post_merge": []any{map[string]any{"id": "a", "timing": "after_repo", "command": []any{"echo"}}}}
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: true\nchecks:\n  minimum: 0\npost_merge: []\n")
	p, _, e := LoadPolicy(dir, c)
	if e != nil {
		t.Fatal(e)
	}
	if p.Checks.Minimum != 0 || !p.Checks.AllSuccess || len(p.PostMerge) != 0 {
		t.Fatalf("inheritance failed: %+v", p)
	}
	cpath := filepath.Join(dir, "config.yaml")
	writeFile(t, cpath, "version: 1\ndefaults:\n  enabled: true\n")
	if _, e = LoadConfig(cpath); e == nil {
		t.Fatal("global participation accepted")
	}
}
func TestDiscovery(t *testing.T) {
	root := t.TempDir()
	root, _ = canonicalDir(root)
	for _, d := range []string{"on", "off", "invalid", "none", "on/nested", "excluded"} {
		dir := filepath.Join(root, d)
		if e := os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
		if _, e := git(context.Background(), dir, "init", "--initial-branch=main"); e != nil {
			t.Fatal(e)
		}
		mustGit(t, dir, "remote", "add", "origin", "https://github.com/o/r.git")
	}
	writeFile(t, filepath.Join(root, "on", "renstiq.yaml"), "version: 1\nenabled: true\n")
	writeFile(t, filepath.Join(root, "on/nested", "renstiq.yaml"), "version: 1\nenabled: true\n")
	writeFile(t, filepath.Join(root, "off", "renstiq.yaml"), "version: 1\nenabled: false\n")
	writeFile(t, filepath.Join(root, "invalid", "renstiq.yaml"), "version: 1\nenabled: yes\n")
	c := DefaultConfig()
	c.Discovery.Include = []string{root + "/*/", root + "/on/"}
	c.Discovery.Exclude = []string{root + "/excluded/"}
	a := Discover(c)
	if len(a) != 5 {
		t.Fatalf("got %d paths: %+v", len(a), a)
	}
	statuses := map[string]string{}
	for _, v := range a {
		statuses[filepath.Base(v.Path)] = v.Status
	}
	for k, want := range map[string]string{"on": "enabled", "off": "disabled", "invalid": "config_error", "none": "no_config", "excluded": "excluded"} {
		if statuses[k] != want {
			t.Errorf("%s: got %s", k, statuses[k])
		}
	}
	if _, ok := statuses["nested"]; ok {
		t.Fatal("single star recursively discovered nested repo")
	}
	c.Discovery.Include = []string{root + "/*/*/"}
	a = Discover(c)
	found := false
	for _, v := range a {
		if strings.HasSuffix(v.Path, "on/nested") && v.Status == "enabled" {
			found = true
		}
	}
	if !found {
		t.Fatal("two levels missing")
	}
	c.Discovery.Include = []string{root + "/**/"}
	a = Discover(c)
	found = false
	self := false
	for _, v := range a {
		if v.Path == root {
			self = true
		}
		if strings.HasSuffix(v.Path, "on/nested") && v.Status == "enabled" {
			found = true
		}
	}
	if !found || !self {
		t.Fatalf("recursive glob must include root and descendants: root=%s found=%v self=%v paths=%+v", root, found, self, a)
	}
}

func TestNullAndEmptyArrayContracts(t *testing.T) {
	for _, body := range []string{"rules: null", "checks: null", "checks:\n  required: null", "review:\n  instructions: null", "pull_requests:\n  files: null", "post_merge: null", "rules:\n- id: x\n  files: ['**']\n  update_types: [patch]\n  checks:\n    required: null", "rules:\n- id: x\n  files: []\n  update_types: [patch]", "rules:\n- id: x\n  files: ['**']\n  update_types: []"} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: false\n"+body+"\n")
		if _, _, err := LoadPolicy(dir, DefaultConfig()); err == nil {
			t.Fatal("invalid configuration accepted", body)
		}
	}
	dir := t.TempDir()
	cfg := filepath.Join(dir, "common.yaml")
	writeFile(t, cfg, "version: 1\ndefaults:\n  checks:\n    minimum: 2\n    required: [{name: global}]\n  rules:\n  - id: inherited\n    files: ['**']\n    update_types: [patch]\n")
	c, err := LoadConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: false\npull_requests:\n  authors: []\n  files: []\nchecks:\n  required: []\nrules:\n- id: replacement\n  files: [go.mod]\n  update_types: [minor]\n  checks:\n    required: []\npost_merge: []\n")
	p, enabled, err := LoadPolicy(dir, c)
	if err != nil || enabled || p.Checks.Minimum != 2 || len(p.Checks.Required) != 0 || len(p.PullRequests.Authors) != 0 || len(p.Rules) != 1 || p.Rules[0].ID != "replacement" || p.Rules[0].Checks.Required == nil || len(*p.Rules[0].Checks.Required) != 0 {
		t.Fatal(p, enabled, err)
	}
	// A wire round trip must retain empty required overrides and omit inherited fields.
	var round Policy
	if err := decodeMap(asMap(p), &round); err != nil || round.Rules[0].Checks.Required == nil || len(*round.Rules[0].Checks.Required) != 0 || round.Rules[0].Checks.Minimum != nil {
		t.Fatal(round, err)
	}
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nrules: []\n")
	p, enabled, err = LoadPolicy(dir, c)
	if err != nil || enabled || len(p.Rules) != 0 {
		t.Fatal(p, enabled, err)
	}
	for _, body := range []string{"ci:\n  poll_seconds: 15", "defaults: null", "discovery:\n  include: null", "retry:\n  max_attempts: 0"} {
		writeFile(t, cfg, "version: 1\n"+body+"\n")
		if _, err := LoadConfig(cfg); err == nil {
			t.Fatal("invalid common configuration accepted", body)
		}
	}
}
func TestDiscoveryDeduplicatesSymlinksAndReportsDisabledErrors(t *testing.T) {
	root := t.TempDir()
	dir := cliRepo(t, root, "repo", "https://github.com/o/r.git")
	link := filepath.Join(root, "alias")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	c := DefaultConfig()
	c.Discovery.Include = []string{root + "/*", dir, link}
	rows := Discover(c)
	if len(rows) != 1 || rows[0].Status != "enabled" {
		t.Fatal(rows)
	}
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: false\nrules:\n- id: x\n  files: ['[']\n  update_types: [patch]\n")
	rows = Discover(c)
	if len(rows) != 1 || rows[0].Status != "config_error" {
		t.Fatal(rows)
	}
	c.Discovery.Exclude = []string{dir}
	rows = Discover(c)
	if len(rows) != 1 || rows[0].Status != "excluded" {
		t.Fatal(rows)
	}
}

func TestDiscoveryKeepsSuccessfulPathsAfterIOFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission errors require a non-root user")
	}
	root := t.TempDir()
	dir := cliRepo(t, root, "repo", "https://github.com/o/r.git")
	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0700) })
	c := DefaultConfig()
	c.Discovery.Include = []string{blocked + "/*", dir}
	rows := Discover(c)
	statuses := map[string]int{}
	for _, r := range rows {
		statuses[r.Status]++
	}
	if len(rows) != 2 || statuses["enabled"] != 1 || statuses["discovery_error"] != 1 {
		t.Fatal(rows)
	}
}

func TestDiscoveryKeepsRepositoriesWithinFailingRecursivePattern(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission errors require a non-root user")
	}
	root, err := canonicalDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Lexical order places a successful subtree on each side of the failure.
	first := cliRepo(t, root, "a-repo", "https://github.com/o/first.git")
	blocked := filepath.Join(root, "blocked")
	last := cliRepo(t, root, "z-repo", "https://github.com/o/last.git")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0700) })
	c := DefaultConfig()
	c.Discovery.Include = []string{root + "/**"}
	rows := Discover(c)
	enabled := map[string]bool{}
	foundError := false
	for _, row := range rows {
		if row.Status == "enabled" {
			enabled[row.Path] = true
		}
		if row.Status == "discovery_error" && row.Path == blocked && row.Reason != "" {
			foundError = true
		}
	}
	if len(enabled) != 2 || !enabled[first] || !enabled[last] || !foundError {
		t.Fatalf("lost healthy repositories or subtree diagnostics: %+v", rows)
	}
	app := &Application{LoadConfig: func(string) (Config, error) { return c, nil }, DiscoverRepos: Discover}
	result, err := app.Discover(context.Background(), DiscoverRequest{})
	if err == nil || len(result.Discovery) != 2 || len(result.Errors) == 0 {
		t.Fatalf("filtered discovery lost repositories or errors: %+v, %v", result, err)
	}
}

// Model ReadDir returning usable entries and an I/O error in the same call.
// This also exercises partial enumeration when tests run as root.
type partialDirectoryFS struct{ fs.FS }

func (p partialDirectoryFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(p.FS, name)
	if name == "." && err == nil {
		return entries, io.ErrUnexpectedEOF
	}
	return entries, err
}

func TestDiscoveryPreservesPartialDirectoryEntries(t *testing.T) {
	fsys := &discoveryFS{
		FS: partialDirectoryFS{fstest.MapFS{
			"a/renstiq.yaml": {Data: []byte("version: 1")},
			"z/renstiq.yaml": {Data: []byte("version: 1")},
		}},
		root: "/repositories",
	}
	paths, err := doublestar.Glob(fsys, "**/renstiq.yaml", doublestar.WithNoFollow())
	if err != nil || len(paths) != 2 || !contains(paths, "a/renstiq.yaml") || !contains(paths, "z/renstiq.yaml") {
		t.Fatalf("partial directory entries were lost: %v, %v", paths, err)
	}
	if len(fsys.failures) == 0 {
		t.Fatalf("directory error was lost: %+v", fsys.failures)
	}
	for _, failure := range fsys.failures {
		if failure.Path != "/repositories" || failure.Reason != io.ErrUnexpectedEOF.Error() {
			t.Fatalf("incorrect directory diagnostic: %+v", failure)
		}
	}
}
