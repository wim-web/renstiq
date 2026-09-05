package renstiq

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

type Discovery struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func Discover(c Config) []Discovery {
	out := []Discovery{}
	seen := map[string]bool{}
	for _, pattern := range c.Discovery.Include {
		pattern = filepath.Clean(expandHome(pattern))
		paths, e := doublestar.FilepathGlob(pattern, doublestar.WithFailOnIOErrors(), doublestar.WithNoFollow())
		if e != nil {
			out = append(out, Discovery{pattern, "discovery_error", e.Error()})
			continue
		}
		for _, p := range paths {
			info, e := os.Stat(p)
			if e != nil {
				out = append(out, Discovery{p, "discovery_error", e.Error()})
				continue
			}
			if !info.IsDir() {
				continue
			}
			canonical, e := filepath.EvalSymlinks(p)
			if e != nil {
				out = append(out, Discovery{p, "discovery_error", e.Error()})
				continue
			}
			if seen[canonical] {
				continue
			}
			seen[canonical] = true
			excluded := false
			for _, ex := range c.Discovery.Exclude {
				ex = filepath.Clean(expandHome(ex))
				if matchAny([]string{ex}, p) || matchAny([]string{ex}, canonical) {
					excluded = true
					break
				}
			}
			if excluded {
				out = append(out, Discovery{canonical, "excluded", "matched discovery.exclude"})
				continue
			}
			m, e := readConfig(filepath.Join(canonical, "renstiq.yaml"), "repo")
			if errors.Is(e, os.ErrNotExist) {
				out = append(out, Discovery{canonical, "no_config", "renstiq.yaml does not exist"})
				continue
			}
			if e != nil {
				out = append(out, Discovery{canonical, "config_error", e.Error()})
				continue
			}
			if m["enabled"] != true {
				out = append(out, Discovery{canonical, "disabled", "enabled: true is not explicitly set"})
				continue
			}
			if _, _, e = LoadPolicy(canonical, c); e != nil {
				out = append(out, Discovery{canonical, "config_error", e.Error()})
				continue
			}
			if e = checkRoot(context.Background(), canonical); e != nil {
				out = append(out, Discovery{canonical, "repository_error", e.Error()})
				continue
			}
			out = append(out, Discovery{canonical, "enabled", "explicitly enabled"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	b, e := cmd.Output()
	if e != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), e, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(b)), nil
}
func canonicalDir(dir string) (string, error) {
	p, e := filepath.Abs(dir)
	if e != nil {
		return "", e
	}
	return filepath.EvalSymlinks(p)
}
func checkRoot(ctx context.Context, dir string) error {
	root, e := git(ctx, dir, "rev-parse", "--show-toplevel")
	if e != nil {
		return e
	}
	root, e = canonicalDir(root)
	if e != nil {
		return e
	}
	d, e := canonicalDir(dir)
	if e != nil {
		return e
	}
	if root != d {
		return errors.New("configuration must be at a Git repository root")
	}
	return nil
}
func repository(ctx context.Context, dir string) (string, error) {
	if e := checkRoot(ctx, dir); e != nil {
		return "", e
	}
	remote, e := git(ctx, dir, "remote", "get-url", "origin")
	if e != nil {
		return "", e
	}
	return githubRepo(remote)
}
func githubRepo(remote string) (string, error) {
	for _, prefix := range []string{"https://github.com/", "ssh://git@github.com/", "git@github.com:"} {
		if strings.HasPrefix(remote, prefix) {
			name := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(remote, prefix), "/"), ".git")
			parts := strings.Split(name, "/")
			if len(parts) == 2 && parts[0] != "" && parts[1] != "" && !strings.ContainsAny(name, " ?#%\\") {
				return name, nil
			}
		}
	}
	return "", errors.New("origin must be a github.com owner/repo URL")
}
