package renstiq

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func versionCommand(version func() string) *cobra.Command {
	return newCommand("version", "Print the version", func(_ context.Context, _ io.Reader, out, log io.Writer) int {
		return writeText(out, log, "renstiq "+version()+"\n")
	})
}

func updateCommand(updater func(context.Context) (UpdateResult, error)) *cobra.Command {
	return newCommand("update", "Update renstiq to the latest release", func(ctx context.Context, _ io.Reader, out, log io.Writer) int {
		result, err := updater(ctx)
		if err != nil {
			fmt.Fprintln(log, err)
			return 1
		}
		if result.Updated {
			return writeText(out, log, fmt.Sprintf("updated renstiq %s -> %s\n", result.CurrentVersion, result.LatestVersion))
		}
		return writeText(out, log, fmt.Sprintf("renstiq %s is already up to date\n", result.CurrentVersion))
	})
}

const schemaUsage = "schema config|repo|config-show|pr-list|discover|result"

func schemaCommand(schema func(string) ([]byte, error)) *cobra.Command {
	var name string
	cmd := newCommand(schemaUsage, "Print a JSON schema", func(_ context.Context, _ io.Reader, out, log io.Writer) int {
		b, err := schema(name)
		if err != nil {
			fmt.Fprintln(log, err)
			return 2
		}
		return writeText(out, log, string(b))
	})
	cmd.ValidArgsFunction = nil
	cmd.ValidArgs = []string{"config", "repo", "config-show", "pr-list", "discover", "result"}
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 || !contains(cmd.ValidArgs, args[0]) {
			return errors.New("schema requires config, repo, config-show, pr-list, discover, or result")
		}
		name = args[0]
		return nil
	}
	return cmd
}
