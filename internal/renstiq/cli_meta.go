package renstiq

import (
	"context"
	"errors"
	"fmt"
	"io"
)

func noArguments(name string, args []string, out io.Writer) error {
	fs := commandFlags(name, name, out)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("%s does not accept arguments", name)
	}
	return nil
}

func versionCommand(version func() string) cliCommand {
	return cliCommand{"version", "version", false, func(args []string, out io.Writer) (cliAction, error) {
		err := noArguments("version", args, out)
		return func(_ context.Context, _ io.Reader, out, log io.Writer) int {
			return writeText(out, log, "renstiq "+version()+"\n")
		}, err
	}}
}

func updateCommand(updater func(context.Context) (UpdateResult, error)) cliCommand {
	return cliCommand{"update", "update", false, func(args []string, out io.Writer) (cliAction, error) {
		err := noArguments("update", args, out)
		return func(ctx context.Context, _ io.Reader, out, log io.Writer) int {
			result, err := updater(ctx)
			if err != nil {
				fmt.Fprintln(log, err)
				return 1
			}
			if result.Updated {
				return writeText(out, log, fmt.Sprintf("updated renstiq %s -> %s\n", result.CurrentVersion, result.LatestVersion))
			}
			return writeText(out, log, fmt.Sprintf("renstiq %s is already up to date\n", result.CurrentVersion))
		}, err
	}}
}

const schemaUsage = "schema config|repo|decision|result|post-input|state"

func schemaCommand(schema func(string) ([]byte, error)) cliCommand {
	return cliCommand{"schema", schemaUsage, false, func(args []string, out io.Writer) (cliAction, error) {
		fs := commandFlags("schema", schemaUsage, out)
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() != 1 || !contains([]string{"config", "repo", "decision", "result", "post-input", "state"}, fs.Arg(0)) {
			return nil, errors.New("schema requires config, repo, decision, result, post-input, or state")
		}
		return func(_ context.Context, _ io.Reader, out, log io.Writer) int {
			b, err := schema(fs.Arg(0))
			if err != nil {
				fmt.Fprintln(log, err)
				return 2
			}
			return writeText(out, log, string(b))
		}, nil
	}}
}
