package renstiq

import (
	"context"
	"errors"
	"io"
	"strings"
)

const statusUsage = "status (--repo DIR | --all) [--config FILE] [--state-dir DIR]"
const abandonUsage = "abandon --repo DIR --run ID --reason TEXT [--state-dir DIR]"

func parseStatus(args []string, out io.Writer) (StatusRequest, error) {
	var req StatusRequest
	fs := commandFlags("status", statusUsage, out)
	targetFlags(fs, &req.Target)
	configFlag(fs, &req.ConfigPath)
	stateFlag(fs, &req.StateDir)
	if err := parseFlags(fs, args); err != nil {
		return req, err
	}
	if err := req.Target.Validate(); err != nil {
		return req, err
	}
	if req.ConfigPath != "" && !req.Target.All {
		return req, errors.New("status --config is only used with --all")
	}
	return req, nil
}

func statusCommand(run func(context.Context, StatusRequest) (BatchResult, error)) cliCommand {
	return cliCommand{"status", statusUsage, true, func(args []string, out io.Writer) (cliAction, error) {
		req, err := parseStatus(args, out)
		return jsonAction("status", func(ctx context.Context, _ io.Reader) (Result, error) {
			batch, err := run(ctx, req)
			return batchOutput(batch), err
		}), err
	}}
}

func parseAbandon(args []string, out io.Writer) (AbandonRequest, error) {
	var req AbandonRequest
	fs := commandFlags("abandon", abandonUsage, out)
	fs.StringVar(&req.Repo, "repo", "", "repository root")
	fs.StringVar(&req.Reason, "reason", "", "manual reconciliation reason")
	stateFlag(fs, &req.StateDir)
	runFlag(fs, &req.RunID)
	if err := parseFlags(fs, args); err != nil {
		return req, err
	}
	if req.Repo == "" {
		return req, errors.New("--repo is required")
	}
	if req.RunID == "" || strings.TrimSpace(req.Reason) == "" {
		return req, errors.New("abandon requires --run and --reason")
	}
	return req, nil
}

func abandonCommand(run func(context.Context, AbandonRequest) (RepoResult, error)) cliCommand {
	return cliCommand{"abandon", abandonUsage, true, func(args []string, out io.Writer) (cliAction, error) {
		req, err := parseAbandon(args, out)
		return jsonAction("abandon", func(ctx context.Context, _ io.Reader) (Result, error) {
			result, err := run(ctx, req)
			return singleOutput(result), err
		}), err
	}}
}
