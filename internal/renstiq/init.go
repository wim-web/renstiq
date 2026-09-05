package renstiq

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type InitResult struct {
	Scope   string `json:"scope"`
	Path    string `json:"path"`
	Created bool   `json:"created"`
}

const commonConfigTemplate = `version: 1
discovery:
  # Add absolute directory patterns to discover repositories, for example:
  # include: ["/path/to/checkouts/*/"]
  include: []
  exclude: []
defaults: {}
`

const repoConfigTemplate = `version: 1
enabled: true
# Override shared settings here as needed.
`

func initializeConfig(ctx context.Context, configFile, repoDir string) (InitResult, error) {
	result := InitResult{Scope: "common"}
	content := commonConfigTemplate
	mode := os.FileMode(0600)
	path := configFile
	if repoDir != "" {
		result.Scope = "repo"
		dir, err := canonicalDir(expandHome(repoDir))
		if err != nil {
			return result, err
		}
		if err := checkRoot(ctx, dir); err != nil {
			return result, err
		}
		path = filepath.Join(dir, "renstiq.yaml")
		content = repoConfigTemplate
		mode = 0644
	} else if path == "" {
		path = configPath()
	}
	path, err := filepath.Abs(expandHome(path))
	if err != nil {
		return result, err
	}
	result.Path = path
	if err := createConfigFile(path, content, mode); err != nil {
		return result, err
	}
	result.Created = true
	return result, nil
}

func createConfigFile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".renstiq-init-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Publish the complete file atomically without replacing an existing file or symlink.
	if err := os.Link(f.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("config already exists; left unchanged: %s", path)
		}
		return err
	}
	return nil
}
