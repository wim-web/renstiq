package renstiq

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

func commandFlags(name, usage string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { fmt.Fprintln(out, "Usage:\n  renstiq", usage); fs.PrintDefaults() }
	return fs
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	var err error
	fs.Visit(func(f *flag.Flag) {
		if err == nil && strings.TrimSpace(f.Value.String()) == "" {
			err = fmt.Errorf("--%s requires a nonempty value", f.Name)
		}
	})
	return err
}

func targetFlags(fs *flag.FlagSet, target *RepoTarget) {
	fs.StringVar(&target.Repo, "repo", "", "repository root")
	fs.BoolVar(&target.All, "all", false, "process every discovered repository")
}
func configFlag(fs *flag.FlagSet, path *string) {
	fs.StringVar(path, "config", "", "common configuration")
}
func stateFlag(fs *flag.FlagSet, path *string) {
	fs.StringVar(path, "state-dir", "", "state directory")
}
func runFlag(fs *flag.FlagSet, id *string) { fs.StringVar(id, "run", "", "run ID") }
func flagSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) { found = found || f.Name == name })
	return found
}
