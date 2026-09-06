package renstiq

import (
	"context"
	"github.com/spf13/cobra"
	"io"
)

func prCommand(run func(context.Context, PRListRequest) (PRListResult, error)) *cobra.Command {
	group := &cobra.Command{Use: "pr", Short: "List open Renovate PR candidates"}
	var req PRListRequest
	cmd := newJSONCommand("list --repo DIR [--all] [--config FILE]", "Select candidates; candidate does not mean permission to merge", func(ctx context.Context, _ io.Reader) (PRListResult, error) { return run(ctx, req) })
	repoFlag(cmd, &req.Repo)
	configFlag(cmd, &req.ConfigPath)
	_ = cmd.MarkFlagRequired("repo")
	cmd.Flags().BoolVar(&req.All, "all", false, "include excluded open Renovate PRs in this repository")
	group.AddCommand(cmd)
	return group
}
