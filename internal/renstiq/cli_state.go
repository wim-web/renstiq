package renstiq

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

const statusUsage = "status (--repo DIR | --all) [--config FILE] [--state-dir DIR]"
const abandonUsage = "abandon --repo DIR --run ID --reason TEXT [--state-dir DIR]"

func statusCommand(run func(context.Context, StatusRequest) (BatchResult, error)) *cobra.Command {
	var req StatusRequest
	cmd := newJSONCommand(statusUsage, "Show recorded run state", func(ctx context.Context, _ io.Reader) (Result, error) {
		batch, err := run(ctx, req)
		return batchOutput(batch), err
	})
	targetFlags(cmd, &req.Target)
	configFlag(cmd, &req.ConfigPath)
	stateFlag(cmd, &req.StateDir)
	cmd.PreRunE = func(*cobra.Command, []string) error {
		if err := req.Target.Validate(); err != nil {
			return err
		}
		if req.ConfigPath != "" && !req.Target.All {
			return errors.New("status --config is only used with --all")
		}
		return nil
	}
	return cmd
}

func abandonCommand(run func(context.Context, AbandonRequest) (RepoResult, error)) *cobra.Command {
	var req AbandonRequest
	cmd := newJSONCommand(abandonUsage, "Abandon a run after manual reconciliation", func(ctx context.Context, _ io.Reader) (Result, error) {
		result, err := run(ctx, req)
		return singleOutput(result), err
	})
	repoFlag(cmd, &req.Repo)
	cmd.Flags().StringVar(&req.Reason, "reason", "", "manual reconciliation reason")
	flagCompletion(cmd, "reason", cobra.NoFileCompletions)
	stateFlag(cmd, &req.StateDir)
	runFlag(cmd, &req.RunID)
	cmd.PreRunE = func(*cobra.Command, []string) error {
		if req.Repo == "" {
			return errors.New("--repo is required")
		}
		if req.RunID == "" || strings.TrimSpace(req.Reason) == "" {
			return errors.New("abandon requires --run and --reason")
		}
		return nil
	}
	return cmd
}
