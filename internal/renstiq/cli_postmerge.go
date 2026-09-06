package renstiq

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/cobra"
)

const postMergeUsage = "post-merge (--repo DIR --run ID | --all) [--finish] [--config FILE] [--state-dir DIR]"

func postMergeCommand(run func(context.Context, PostMergeRequest) (BatchResult, error)) *cobra.Command {
	var req PostMergeRequest
	cmd := newJSONCommand(postMergeUsage, "Execute post-merge commands or finish a run", func(ctx context.Context, _ io.Reader) (Result, error) {
		batch, err := run(ctx, req)
		return batchOutput(batch), err
	})
	targetFlags(cmd, &req.Target)
	configFlag(cmd, &req.ConfigPath)
	stateFlag(cmd, &req.StateDir)
	runFlag(cmd, &req.RunID)
	cmd.Flags().BoolVar(&req.Finish, "finish", false, "finish run and execute after_repo commands")
	cmd.PreRunE = func(*cobra.Command, []string) error {
		if err := req.Target.Validate(); err != nil {
			return err
		}
		if req.Target.All && req.RunID != "" {
			return errors.New("--run requires one --repo")
		}
		if !req.Target.All && req.RunID == "" {
			return errors.New("post-merge requires --run")
		}
		return nil
	}
	return cmd
}
