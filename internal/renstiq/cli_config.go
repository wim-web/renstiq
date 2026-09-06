package renstiq

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/cobra"
)

const initUsage = "init [--config FILE | --repo DIR]"

func initCommand(run func(context.Context, InitRequest) (InitResult, error)) *cobra.Command {
	var req InitRequest
	cmd := newJSONCommand(initUsage, "Create common or repository configuration", func(ctx context.Context, _ io.Reader) (Result, error) {
		result, err := run(ctx, req)
		return Result{Command: "init", Init: &result}, err
	})
	configFlag(cmd, &req.ConfigPath)
	repoFlag(cmd, &req.Repo)
	cmd.PreRunE = func(*cobra.Command, []string) error {
		if req.Repo != "" && req.ConfigPath != "" {
			return errors.New("init accepts either --repo DIR or --config FILE")
		}
		return nil
	}
	return cmd
}

const discoverUsage = "discover [--all] [--config FILE]"

func discoverCommand(run func(context.Context, DiscoverRequest) (DiscoveryResult, error)) *cobra.Command {
	var req DiscoverRequest
	cmd := newJSONCommand(discoverUsage, "Discover enabled repositories", func(ctx context.Context, _ io.Reader) (DiscoveryResult, error) { return run(ctx, req) })
	configFlag(cmd, &req.ConfigPath)
	cmd.Flags().BoolVar(&req.All, "all", false, "include unconfigured, disabled, excluded, and erroneous paths")
	return cmd
}
func configCommand(run func(context.Context, ConfigRequest) (ConfigResult, error)) *cobra.Command {
	group := &cobra.Command{Use: "config", Short: "Inspect effective configuration"}
	var req ConfigRequest
	cmd := newJSONCommand("show --repo DIR [--config FILE]", "Resolve and validate configuration without GitHub authentication", func(ctx context.Context, _ io.Reader) (ConfigResult, error) { return run(ctx, req) })
	repoFlag(cmd, &req.Repo)
	configFlag(cmd, &req.ConfigPath)
	_ = cmd.MarkFlagRequired("repo")
	group.AddCommand(cmd)
	return group
}
