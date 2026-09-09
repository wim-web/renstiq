package renstiq

import (
	"context"
	"github.com/spf13/cobra"
	"io"
)

func prCommand(run func(context.Context, PRListRequest) (PRListResult, error)) *cobra.Command {
	group := &cobra.Command{Use: "pr", Short: "List open PR candidates selected by configuration"}
	var req PRListRequest
	cmd := newJSONCommand("list [--repo DIR] [--all]", "Select candidates; candidate does not mean permission to merge", func(ctx context.Context, _ io.Reader) (PRListResult, error) { return run(ctx, req) })
	repoFlag(cmd, &req.Repo, ".")
	cmd.Flags().BoolVar(&req.All, "all", false, "include all open PRs, including excluded and unknown PRs, for diagnostics")
	group.AddCommand(cmd)
	return group
}
