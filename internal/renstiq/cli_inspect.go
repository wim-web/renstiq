package renstiq

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/cobra"
)

const inspectUsage = "inspect (--repo DIR | --all) [--pr NUMBER] [--run ID] [--config FILE] [--state-dir DIR]"

func inspectCommand(run func(context.Context, InspectRequest) (BatchResult, error)) *cobra.Command {
	var req InspectRequest
	cmd := newJSONCommand(inspectUsage, "Inspect pull requests and create or resume a run", func(ctx context.Context, _ io.Reader) (Result, error) {
		batch, err := run(ctx, req)
		return batchOutput(batch), err
	})
	targetFlags(cmd, &req.Target)
	configFlag(cmd, &req.ConfigPath)
	stateFlag(cmd, &req.StateDir)
	runFlag(cmd, &req.RunID)
	cmd.Flags().IntVar(&req.PR, "pr", 0, "inspect one positive pull request number")
	flagCompletion(cmd, "pr", cobra.NoFileCompletions)
	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := req.Target.Validate(); err != nil {
			return err
		}
		if cmd.Flags().Changed("pr") && req.PR <= 0 {
			return errors.New("--pr must be positive")
		}
		if req.Target.All && (req.PR != 0 || req.RunID != "") {
			return errors.New("--pr and --run require one --repo")
		}
		return nil
	}
	return cmd
}
