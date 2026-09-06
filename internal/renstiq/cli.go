package renstiq

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
)

type cliAction func(context.Context, io.Reader, io.Writer, io.Writer) int

type cliCommand struct {
	name, usage string
	json        bool
	parse       func([]string, io.Writer) (cliAction, error)
}

type cli struct{ commands []cliCommand }

func RunCLI(ctx context.Context, args []string, in io.Reader, out, log io.Writer) int {
	return newCLI(newApplication(log), selfUpdate).Run(ctx, args, in, out, log)
}

// This is the only command registry; help and dispatch cannot drift apart.
func newCLI(app *Application, updater func(context.Context) (UpdateResult, error)) cli {
	return cli{commands: []cliCommand{
		versionCommand(buildVersion), initCommand(app.Init), discoverCommand(app.Discover),
		inspectCommand(app.Inspect), feedbackCommand(app.Feedback, readDecisionSource),
		mergeCommand(app.Merge, readDecisionSource), postMergeCommand(app.PostMerge),
		statusCommand(app.Status), abandonCommand(app.Abandon), schemaCommand(Schema), updateCommand(updater),
	}}
}

func (c cli) Run(ctx context.Context, args []string, in io.Reader, out, log io.Writer) int {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h")) {
		return writeText(out, log, c.help())
	}
	if args[0] == "help" && len(args) == 2 {
		args = []string{args[1], "--help"}
	}
	name := args[0]
	if name == "--version" {
		name = "version"
	}
	for _, command := range c.commands {
		if command.name != name {
			continue
		}
		var usage bytes.Buffer
		action, err := command.parse(args[1:], &usage)
		if errors.Is(err, flag.ErrHelp) {
			return writeText(out, log, usage.String())
		}
		if err != nil {
			if command.json {
				return emitResult(out, log, Result{Command: name}, &InputError{err})
			}
			fmt.Fprintln(log, err)
			return 2
		}
		return action(ctx, in, out, log)
	}
	return emitResult(out, log, Result{Command: name}, &InputError{fmt.Errorf("unknown command: %s", name)})
}

func (c cli) help() string {
	var b bytes.Buffer
	fmt.Fprintln(&b, "renstiq — GitHub PR review, merge, and post-merge execution\n\nUsage:")
	for _, command := range c.commands {
		fmt.Fprintln(&b, "  renstiq", command.usage)
	}
	fmt.Fprintln(&b, "\nOptions follow the command. Use COMMAND --help for command-specific options.\n--decision - reads stdin. --state-dir overrides XDG_STATE_HOME/renstiq.\ninspect returns a resumable run ID; --finish closes a run and runs after_repo commands.\nOperational commands output JSON; version/update output text. Child logs go to stderr.")
	return b.String()
}
