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
		return Result{Init: &result}, err
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

const discoverUsage = "discover [--config FILE] [--all]"

func discoverCommand(run func(context.Context, DiscoverRequest) (BatchResult, error)) *cobra.Command {
	var req DiscoverRequest
	cmd := newJSONCommand(discoverUsage, "Discover enabled repositories", func(ctx context.Context, _ io.Reader) (Result, error) {
		batch, err := run(ctx, req)
		return batchOutput(batch), err
	})
	configFlag(cmd, &req.ConfigPath)
	cmd.Flags().BoolVar(&req.All, "all", false, "include all discovery statuses")
	return cmd
}
