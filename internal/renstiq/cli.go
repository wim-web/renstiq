package renstiq

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

type cliAction func(context.Context, io.Reader, io.Writer, io.Writer) int

type cli struct{ newCommands func() []*cobra.Command }

func RunCLI(ctx context.Context, args []string, in io.Reader, out, log io.Writer) int {
	return newCLI(newApplication(log), selfUpdate).Run(ctx, args, in, out, log)
}

func newCLI(app *Application, updater func(context.Context) (UpdateResult, error)) cli {
	return cli{newCommands: func() []*cobra.Command {
		return []*cobra.Command{
			versionCommand(buildVersion), initCommand(app.Init), discoverCommand(app.Discover),
			inspectCommand(app.Inspect), feedbackCommand(app.Feedback, readDecisionSource),
			mergeCommand(app.Merge, readDecisionSource), postMergeCommand(app.PostMerge),
			statusCommand(app.Status), abandonCommand(app.Abandon), schemaCommand(Schema), updateCommand(updater),
		}
	}}
}

func (c cli) newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "renstiq",
		Short: "GitHub PR review, merge, and post-merge execution",
		Long: `renstiq — GitHub PR review, merge, and post-merge execution

Options follow the command. Use COMMAND --help for command-specific options.
--decision - reads stdin. --state-dir overrides XDG_STATE_HOME/renstiq.
inspect returns a resumable run ID; --finish closes a run and runs after_repo commands.
Operational commands output JSON; version/update output text. Child logs go to stderr.`,
		Version:       buildVersion(),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetVersionTemplate("renstiq {{.Version}}\n")
	// Cobra uses the same definitions for dispatch, help, and shell completion.
	root.AddCommand(c.newCommands()...)
	return root
}

func (c cli) Run(ctx context.Context, args []string, in io.Reader, out, log io.Writer) int {
	// Requests, flags, and Cobra's execution state belong to this invocation only.
	root := c.newRootCommand()
	// A nil argument slice would make Cobra read the host process's arguments.
	root.SetArgs(append([]string{}, args...))
	root.SetIn(in)
	w := &cliOutputWriter{Writer: out}
	root.SetOut(w)
	root.SetErr(log)
	command, err := root.ExecuteContextC(ctx)
	var exit cliExitError
	if errors.As(err, &exit) {
		return int(exit)
	}
	// Some built-in Cobra commands discard write errors (including help).
	if w.err != nil {
		fmt.Fprintln(log, w.err)
		return 1
	}
	if err == nil {
		return 0
	}
	if command == root || command.Annotations["output"] == "json" {
		name := command.Name()
		if command == root && len(args) > 0 {
			name = args[0]
		}
		return emitResult(out, log, Result{Command: name}, &InputError{err})
	}
	fmt.Fprintln(log, err)
	return 2
}

// Actions have already emitted their result and diagnostics before returning a code.
type cliExitError int

func (e cliExitError) Error() string { return fmt.Sprintf("exit status %d", e) }

func newCommand(use, short string, action cliAction) *cobra.Command {
	cmd := &cobra.Command{
		Use:                   use,
		Short:                 short,
		DisableFlagsInUseLine: true,
		ValidArgsFunction:     cobra.NoFileCompletions,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("%s does not accept arguments", cmd.Name())
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if code := action(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()); code != 0 {
				return cliExitError(code)
			}
			return nil
		},
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func newJSONCommand(use, short string, run func(context.Context, io.Reader) (Result, error)) *cobra.Command {
	cmd := newCommand(use, short, jsonAction(strings.Fields(use)[0], run))
	cmd.Annotations = map[string]string{"output": "json"}
	cmd.Args = validateFlags
	return cmd
}

type cliOutputWriter struct {
	io.Writer
	err error
}

func (w *cliOutputWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	if w.err == nil {
		w.err = err
	}
	return n, err
}
