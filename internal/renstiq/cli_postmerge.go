package renstiq

import (
	"context"
	"errors"
	"io"
)

const postMergeUsage = "post-merge (--repo DIR --run ID | --all) [--finish] [--config FILE] [--state-dir DIR]"

func parsePostMerge(args []string, out io.Writer) (PostMergeRequest, error) {
	var req PostMergeRequest
	fs := commandFlags("post-merge", postMergeUsage, out)
	targetFlags(fs, &req.Target)
	configFlag(fs, &req.ConfigPath)
	stateFlag(fs, &req.StateDir)
	runFlag(fs, &req.RunID)
	fs.BoolVar(&req.Finish, "finish", false, "finish run and execute after_repo commands")
	if err := parseFlags(fs, args); err != nil {
		return req, err
	}
	if err := req.Target.Validate(); err != nil {
		return req, err
	}
	if req.Target.All && req.RunID != "" {
		return req, errors.New("--run requires one --repo")
	}
	if !req.Target.All && req.RunID == "" {
		return req, errors.New("post-merge requires --run")
	}
	return req, nil
}

func postMergeCommand(run func(context.Context, PostMergeRequest) (BatchResult, error)) cliCommand {
	return cliCommand{"post-merge", postMergeUsage, true, func(args []string, out io.Writer) (cliAction, error) {
		req, err := parsePostMerge(args, out)
		return jsonAction("post-merge", func(ctx context.Context, _ io.Reader) (Result, error) {
			batch, err := run(ctx, req)
			return batchOutput(batch), err
		}), err
	}}
}
