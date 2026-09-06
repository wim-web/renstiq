package renstiq

import (
	"context"
	"errors"
	"io"
)

const inspectUsage = "inspect (--repo DIR | --all) [--pr NUMBER] [--run ID] [--config FILE] [--state-dir DIR]"

func parseInspect(args []string, out io.Writer) (InspectRequest, error) {
	var req InspectRequest
	fs := commandFlags("inspect", inspectUsage, out)
	targetFlags(fs, &req.Target)
	configFlag(fs, &req.ConfigPath)
	stateFlag(fs, &req.StateDir)
	runFlag(fs, &req.RunID)
	fs.IntVar(&req.PR, "pr", 0, "inspect one positive pull request number")
	if err := parseFlags(fs, args); err != nil {
		return req, err
	}
	if err := req.Target.Validate(); err != nil {
		return req, err
	}
	if flagSet(fs, "pr") && req.PR <= 0 {
		return req, errors.New("--pr must be positive")
	}
	if req.Target.All && (req.PR != 0 || req.RunID != "") {
		return req, errors.New("--pr and --run require one --repo")
	}
	return req, nil
}

func inspectCommand(run func(context.Context, InspectRequest) (BatchResult, error)) cliCommand {
	return cliCommand{"inspect", inspectUsage, true, func(args []string, out io.Writer) (cliAction, error) {
		req, err := parseInspect(args, out)
		return jsonAction("inspect", func(ctx context.Context, _ io.Reader) (Result, error) {
			batch, err := run(ctx, req)
			return batchOutput(batch), err
		}), err
	}}
}
