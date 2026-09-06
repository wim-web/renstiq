package renstiq

import (
	"context"
	"errors"
	"io"
)

const initUsage = "init [--config FILE | --repo DIR]"

func parseInit(args []string, out io.Writer) (InitRequest, error) {
	var req InitRequest
	fs := commandFlags("init", initUsage, out)
	configFlag(fs, &req.ConfigPath)
	fs.StringVar(&req.Repo, "repo", "", "repository root")
	if err := parseFlags(fs, args); err != nil {
		return req, err
	}
	if req.Repo != "" && req.ConfigPath != "" {
		return req, errors.New("init accepts either --repo DIR or --config FILE")
	}
	return req, nil
}

func initCommand(run func(context.Context, InitRequest) (InitResult, error)) cliCommand {
	return cliCommand{"init", initUsage, true, func(args []string, out io.Writer) (cliAction, error) {
		req, err := parseInit(args, out)
		return jsonAction("init", func(ctx context.Context, _ io.Reader) (Result, error) {
			result, err := run(ctx, req)
			return Result{Init: &result}, err
		}), err
	}}
}

const discoverUsage = "discover [--config FILE]"

func parseDiscover(args []string, out io.Writer) (DiscoverRequest, error) {
	var req DiscoverRequest
	fs := commandFlags("discover", discoverUsage, out)
	configFlag(fs, &req.ConfigPath)
	err := parseFlags(fs, args)
	return req, err
}

func discoverCommand(run func(context.Context, DiscoverRequest) (BatchResult, error)) cliCommand {
	return cliCommand{"discover", discoverUsage, true, func(args []string, out io.Writer) (cliAction, error) {
		req, err := parseDiscover(args, out)
		return jsonAction("discover", func(ctx context.Context, _ io.Reader) (Result, error) {
			batch, err := run(ctx, req)
			return batchOutput(batch), err
		}), err
	}}
}
