package renstiq

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func validateFlags(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return errors.New("unexpected positional arguments")
	}
	var err error
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if err == nil && strings.TrimSpace(f.Value.String()) == "" {
			err = fmt.Errorf("--%s requires a nonempty value", f.Name)
		}
	})
	return err
}

func targetFlags(cmd *cobra.Command, target *RepoTarget) {
	repoFlag(cmd, &target.Repo)
	cmd.Flags().BoolVar(&target.All, "all", false, "process every discovered repository")
}

func repoFlag(cmd *cobra.Command, path *string) {
	cmd.Flags().StringVar(path, "repo", "", "repository root")
	flagCompletion(cmd, "repo", directoryCompletions)
}

func configFlag(cmd *cobra.Command, path *string) {
	cmd.Flags().StringVar(path, "config", "", "common configuration")
}

func stateFlag(cmd *cobra.Command, path *string) {
	cmd.Flags().StringVar(path, "state-dir", "", "state directory")
	flagCompletion(cmd, "state-dir", directoryCompletions)
}

func runFlag(cmd *cobra.Command, id *string) {
	cmd.Flags().StringVar(id, "run", "", "run ID")
	flagCompletion(cmd, "run", cobra.NoFileCompletions)
}

func directoryCompletions(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveFilterDirs
}

func flagCompletion(cmd *cobra.Command, name string, complete cobra.CompletionFunc) {
	if err := cmd.RegisterFlagCompletionFunc(name, complete); err != nil {
		// Registration errors indicate a programming error in the command definition.
		panic(err)
	}
}
