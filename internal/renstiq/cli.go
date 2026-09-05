package renstiq

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type Result struct {
	Version   int          `json:"version"`
	Command   string       `json:"command"`
	Init      *InitResult  `json:"init,omitempty"`
	Results   []RepoResult `json:"results"`
	Discovery []Discovery  `json:"discovery,omitempty"`
	Error     string       `json:"error,omitempty"`
}
type RepoResult struct {
	Decision     *Decision     `json:"decision,omitempty"`
	Path         string        `json:"path"`
	Repo         string        `json:"repo,omitempty"`
	RunID        string        `json:"run,omitempty"`
	Config       *Policy       `json:"config,omitempty"`
	ConfigDigest string        `json:"config_digest,omitempty"`
	PRs          []PullRequest `json:"pull_requests,omitempty"`
	Operations   []Operation   `json:"operations,omitempty"`
	Merge        *MergeRecord  `json:"merge,omitempty"`
	State        *State        `json:"state,omitempty"`
	Error        string        `json:"error,omitempty"`
}

const help = `renstiq — GitHub PR review, merge, and post-merge execution

Usage:
  renstiq version
  renstiq init [--config FILE]
  renstiq init --repo DIR
  renstiq discover [--config FILE]
  renstiq inspect --repo DIR [--pr NUMBER] [--config FILE]
  renstiq inspect --all [--config FILE]
  renstiq feedback --repo DIR --run ID --decision FILE
  renstiq merge --repo DIR --run ID --decision FILE
  renstiq post-merge --repo DIR --run ID [--finish]
  renstiq post-merge --all --finish
  renstiq status --repo DIR | --all
  renstiq abandon --repo DIR --run ID --reason TEXT
  renstiq schema config|repo|decision|result|post-input|state

Options follow the command. --decision - reads stdin. --state-dir DIR overrides
XDG_STATE_HOME/renstiq. inspect returns a resumable run ID; --finish closes a run
and executes after_repo commands. stdout is JSON; progress and child logs go to stderr.
`

func RunCLI(ctx context.Context, args []string, in io.Reader, out, log io.Writer) int {
	return runCLI(ctx, args, in, out, log, NewGitHub)
}

func runCLI(ctx context.Context, args []string, in io.Reader, out, log io.Writer, factory func(context.Context, Config, io.Writer) (*GitHub, error)) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(out, help)
		return 0
	}
	command := args[0]
	if command == "version" || command == "--version" {
		if len(args) != 1 {
			fmt.Fprintln(log, "version does not accept arguments")
			return 2
		}
		fmt.Fprintln(out, "renstiq", buildVersion())
		return 0
	}
	if command == "schema" {
		if len(args) != 2 {
			fmt.Fprintln(log, "schema requires config, repo, decision, result, post-input, or state")
			return 2
		}
		b, e := Schema(args[1])
		if e != nil {
			fmt.Fprintln(log, e)
			return 2
		}
		_, _ = out.Write(b)
		return 0
	}
	result := Result{Version: 1, Command: command, Results: []RepoResult{}}
	emit := func(code int) int {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if e := enc.Encode(result); e != nil {
			fmt.Fprintln(log, e)
			return 1
		}
		return code
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(log)
	var cfg, dir, stateDir, runID, decisionFile, reason string
	var all, finish bool
	var prNumber int
	fs.StringVar(&reason, "reason", "", "manual reconciliation reason for abandon")
	fs.StringVar(&cfg, "config", "", "common configuration")
	fs.StringVar(&dir, "repo", "", "repository root")
	fs.StringVar(&stateDir, "state-dir", "", "state directory")
	fs.StringVar(&runID, "run", "", "run ID")
	fs.StringVar(&decisionFile, "decision", "", "decision JSON file, or -")
	fs.BoolVar(&all, "all", false, "process every discovered repository")
	fs.BoolVar(&finish, "finish", false, "finish run and execute after_repo commands")
	fs.IntVar(&prNumber, "pr", 0, "inspect one pull request")
	if e := fs.Parse(args[1:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		result.Error = e.Error()
		return emit(2)
	}
	if fs.NArg() != 0 {
		result.Error = "unexpected positional arguments"
		return emit(2)
	}
	if !contains([]string{"init", "discover", "inspect", "feedback", "merge", "post-merge", "status", "abandon"}, command) {
		result.Error = "unknown command: " + command
		return emit(2)
	}

	allowed := map[string][]string{
		"init":       {"config", "repo"},
		"discover":   {"config"},
		"inspect":    {"config", "repo", "all", "pr", "state-dir", "run"},
		"feedback":   {"config", "repo", "run", "decision", "state-dir"},
		"merge":      {"config", "repo", "run", "decision", "state-dir"},
		"post-merge": {"config", "repo", "all", "run", "finish", "state-dir"},
		"status":     {"config", "repo", "all", "state-dir"},
		"abandon":    {"config", "repo", "run", "reason", "state-dir"},
	}
	fs.Visit(func(f *flag.Flag) {
		if !contains(allowed[command], f.Name) {
			result.Error = "flag --" + f.Name + " is not valid for " + command
		}
	})
	if result.Error != "" {
		return emit(2)
	}
	if command == "init" {
		var repoFlag, configFlag bool
		fs.Visit(func(f *flag.Flag) {
			repoFlag = repoFlag || f.Name == "repo"
			configFlag = configFlag || f.Name == "config"
		})
		if (repoFlag && configFlag) || (repoFlag && dir == "") || (configFlag && cfg == "") {
			result.Error = "init accepts either --repo DIR or --config FILE, with a nonempty value"
			return emit(2)
		}
		created, err := initializeConfig(ctx, cfg, dir)
		result.Init = &created
		if err != nil {
			result.Error = err.Error()
			return emit(1)
		}
		return emit(0)
	}
	if prNumber < 0 {
		result.Error = "--pr must be positive"
		return emit(2)
	}
	c, err := LoadConfig(cfg)
	if err != nil {
		result.Error = err.Error()
		return emit(2)
	}
	if command == "discover" {
		result.Discovery = Discover(c)
		code := 0
		for _, d := range result.Discovery {
			if d.Status == "config_error" || d.Status == "discovery_error" || d.Status == "repository_error" {
				code = 1
			}
		}
		return emit(code)
	}
	if (dir == "") == !all {
		result.Error = "specify exactly one of --repo or --all"
		return emit(2)
	}
	if all && (command == "feedback" || command == "merge" || command == "abandon" || prNumber != 0 || runID != "") {
		result.Error = "feedback/merge/--pr/--run require one --repo"
		return emit(2)
	}
	if (command == "feedback" || command == "merge") && (runID == "" || decisionFile == "") {
		result.Error = "--run and --decision are required"
		return emit(2)
	}
	if command == "post-merge" && !all && runID == "" {
		result.Error = "post-merge requires --run"
		return emit(2)
	}
	if command == "abandon" && (runID == "" || strings.TrimSpace(reason) == "") {
		result.Error = "abandon requires --run and --reason"
		return emit(2)
	}
	var decision Decision
	if decisionFile != "" {
		reader := in
		if decisionFile != "-" {
			f, e := os.Open(decisionFile)
			if e != nil {
				result.Error = e.Error()
				return emit(2)
			}
			defer f.Close()
			reader = f
		}
		decision, err = ReadDecision(reader)
		if err != nil {
			result.Error = err.Error()
			return emit(2)
		}
	}
	paths := []string{dir}
	code := 0
	if all {
		paths = nil
		result.Discovery = Discover(c)
		for _, d := range result.Discovery {
			if d.Status == "enabled" {
				paths = append(paths, d.Path)
			} else if d.Status == "config_error" || d.Status == "discovery_error" || d.Status == "repository_error" {
				code = 1
			}
		}
	}
	var g *GitHub
	if command != "status" && command != "abandon" && len(paths) > 0 {
		g, err = factory(ctx, c, log)
		if err != nil {
			result.Error = err.Error()
			return emit(1)
		}
	}
	for _, path := range paths {
		var r RepoResult
		if command == "abandon" {
			r = abandonRepo(ctx, path, runID, reason, stateDir)
		} else {
			r = processRepo(ctx, command, path, runID, stateDir, prNumber, finish, c, g, decision)
		}
		result.Results = append(result.Results, r)
		if r.Error != "" {
			code = 1
		}
	}
	return emit(code)
}
func processRepo(ctx context.Context, command, path, runID, stateDir string, prNumber int, finish bool, c Config, g *GitHub, d Decision) (result RepoResult) {
	result.Path = path
	fail := func(e error) RepoResult { result.Error = e.Error(); return result }
	dir, err := canonicalDir(path)
	if err != nil {
		return fail(err)
	}
	result.Path = dir
	repo, err := repository(ctx, dir)
	if err != nil {
		return fail(err)
	}
	result.Repo = repo
	s, err := openStore(stateDir, repo)
	if err != nil {
		return fail(err)
	}
	defer s.Close()
	if command == "status" {
		result.State = &s.State
		return result
	}
	p, hash, err := LoadPolicy(dir, c)
	if err != nil {
		return fail(err)
	}
	var r *Run
	if runID != "" {
		r, err = s.Find(runID)
		if err == nil && hash != r.ConfigDigest {
			err = errors.New("configuration changed since run creation; use original configuration to finish pending actions")
		}
	} else if command == "post-merge" {
		if len(s.State.Runs) == 0 {
			return result
		}
		r = &s.State.Runs[len(s.State.Runs)-1]
		if hash != r.ConfigDigest {
			return fail(errors.New("configuration changed since run creation"))
		}
	} else {
		r, err = s.Current(p, hash)
	}
	if err != nil {
		return fail(err)
	}
	result.RunID = r.ID
	engine := Engine{GitHub: g, Store: s, Dir: dir, Repo: repo}
	switch command {
	case "inspect":
		result.Config = &p
		result.ConfigDigest = hash
		var candidates []rawPR
		if prNumber > 0 {
			raw, e := g.raw(ctx, repo, prNumber)
			if e != nil {
				return fail(e)
			}
			candidates = []rawPR{raw}
		} else {
			candidates, err = g.list(ctx, repo)
			if err != nil {
				return fail(err)
			}
		}
		errs := []error{}
		for _, raw := range candidates { // Keep all Renovate PRs even when configured author/base/file policies exclude them.
			if prNumber == 0 && !contains([]string{"app/renovate", "renovate[bot]"}, raw.User.Login) && !contains(p.PullRequests.Authors, raw.User.Login) {
				continue
			}
			pr, e := g.waitPR(ctx, repo, raw.Number, "", "", true)
			if e != nil {
				errs = append(errs, fmt.Errorf("PR #%d: %w", raw.Number, e))
				pr.Number = raw.Number
				pr.Reasons = append(pr.Reasons, e.Error())
			} else {
				pr.Reasons = machineReasons(p, pr)
			}
			result.PRs = append(result.PRs, pr)
		}
		if len(errs) > 0 {
			return fail(errors.Join(errs...))
		}
	case "feedback":
		result.Decision = &d
		result.Operations, err = engine.Feedback(ctx, r, d)
	case "merge":
		result.Decision = &d
		result.Merge, err = engine.Merge(ctx, r, d)
		result.Operations = r.Operations
	case "post-merge":
		err = engine.PostMerge(ctx, r, finish)
		result.Operations = r.Operations
	}
	if err != nil {
		return fail(err)
	}
	return result
}

func abandonRepo(ctx context.Context, path, id, reason, stateDir string) RepoResult {
	result := RepoResult{Path: path, RunID: id}
	repo, err := repository(ctx, path)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Repo = repo
	s, err := openStore(stateDir, repo)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer s.Close()
	if err = s.Abandon(id, reason); err != nil {
		result.Error = err.Error()
	}
	result.State = &s.State
	return result
}
