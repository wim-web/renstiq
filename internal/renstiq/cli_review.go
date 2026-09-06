package renstiq

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/cobra"
)

// Feedback and merge deliberately share exactly the same input syntax, but
// convert it to different typed requests. No other command uses this type.
type reviewOptions struct{ Repo, ConfigPath, StateDir, RunID, DecisionFile string }
type decisionLoader func(string, io.Reader) (Decision, error)

func reviewUsage(name string) string {
	return name + " --repo DIR --run ID --decision FILE [--config FILE] [--state-dir DIR]"
}

func reviewFlags(cmd *cobra.Command, opts *reviewOptions) {
	repoFlag(cmd, &opts.Repo)
	cmd.Flags().StringVar(&opts.DecisionFile, "decision", "", "decision JSON file, or - for stdin")
	configFlag(cmd, &opts.ConfigPath)
	stateFlag(cmd, &opts.StateDir)
	runFlag(cmd, &opts.RunID)
	cmd.PreRunE = func(*cobra.Command, []string) error {
		if opts.Repo == "" {
			return errors.New("--repo is required")
		}
		if opts.RunID == "" || opts.DecisionFile == "" {
			return errors.New("--run and --decision are required")
		}
		return nil
	}
}

func feedbackCommand(run func(context.Context, FeedbackRequest) (RepoResult, error), read decisionLoader) *cobra.Command {
	var opts reviewOptions
	cmd := newJSONCommand(reviewUsage("feedback"), "Post review comments and labels from a decision", func(ctx context.Context, in io.Reader) (Result, error) {
		d, err := read(opts.DecisionFile, in)
		if err != nil {
			return Result{}, &InputError{err}
		}
		result, err := run(ctx, FeedbackRequest{Repo: opts.Repo, ConfigPath: opts.ConfigPath, StateDir: opts.StateDir, RunID: opts.RunID, Decision: d})
		return singleOutput(result), err
	})
	reviewFlags(cmd, &opts)
	return cmd
}

func mergeCommand(run func(context.Context, MergeRequest) (RepoResult, error), read decisionLoader) *cobra.Command {
	var opts reviewOptions
	cmd := newJSONCommand(reviewUsage("merge"), "Merge a pull request using a review decision", func(ctx context.Context, in io.Reader) (Result, error) {
		d, err := read(opts.DecisionFile, in)
		if err != nil {
			return Result{}, &InputError{err}
		}
		result, err := run(ctx, MergeRequest{Repo: opts.Repo, ConfigPath: opts.ConfigPath, StateDir: opts.StateDir, RunID: opts.RunID, Decision: d})
		return singleOutput(result), err
	})
	reviewFlags(cmd, &opts)
	return cmd
}
