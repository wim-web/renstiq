package renstiq

import (
	"context"
	"errors"
	"io"
)

// Feedback and merge deliberately share exactly the same input syntax, but
// convert it to different typed requests. No other command uses this type.
type reviewOptions struct{ Repo, ConfigPath, StateDir, RunID, DecisionFile string }
type decisionLoader func(string, io.Reader) (Decision, error)

func reviewUsage(name string) string {
	return name + " --repo DIR --run ID --decision FILE [--config FILE] [--state-dir DIR]"
}

func parseReview(name string, args []string, out io.Writer) (reviewOptions, error) {
	var opts reviewOptions
	fs := commandFlags(name, reviewUsage(name), out)
	fs.StringVar(&opts.Repo, "repo", "", "repository root")
	fs.StringVar(&opts.DecisionFile, "decision", "", "decision JSON file, or - for stdin")
	configFlag(fs, &opts.ConfigPath)
	stateFlag(fs, &opts.StateDir)
	runFlag(fs, &opts.RunID)
	if err := parseFlags(fs, args); err != nil {
		return opts, err
	}
	if opts.Repo == "" {
		return opts, errors.New("--repo is required")
	}
	if opts.RunID == "" || opts.DecisionFile == "" {
		return opts, errors.New("--run and --decision are required")
	}
	return opts, nil
}

func feedbackCommand(run func(context.Context, FeedbackRequest) (RepoResult, error), read decisionLoader) cliCommand {
	return cliCommand{"feedback", reviewUsage("feedback"), true, func(args []string, out io.Writer) (cliAction, error) {
		opts, err := parseReview("feedback", args, out)
		return jsonAction("feedback", func(ctx context.Context, in io.Reader) (Result, error) {
			d, err := read(opts.DecisionFile, in)
			if err != nil {
				return Result{}, &InputError{err}
			}
			result, err := run(ctx, FeedbackRequest{Repo: opts.Repo, ConfigPath: opts.ConfigPath, StateDir: opts.StateDir, RunID: opts.RunID, Decision: d})
			return singleOutput(result), err
		}), err
	}}
}

func mergeCommand(run func(context.Context, MergeRequest) (RepoResult, error), read decisionLoader) cliCommand {
	return cliCommand{"merge", reviewUsage("merge"), true, func(args []string, out io.Writer) (cliAction, error) {
		opts, err := parseReview("merge", args, out)
		return jsonAction("merge", func(ctx context.Context, in io.Reader) (Result, error) {
			d, err := read(opts.DecisionFile, in)
			if err != nil {
				return Result{}, &InputError{err}
			}
			result, err := run(ctx, MergeRequest{Repo: opts.Repo, ConfigPath: opts.ConfigPath, StateDir: opts.StateDir, RunID: opts.RunID, Decision: d})
			return singleOutput(result), err
		}), err
	}}
}
