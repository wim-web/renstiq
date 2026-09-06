package renstiq

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
)

func runTestCLI(ctx context.Context, args []string, in io.Reader, out, log io.Writer, factory func(context.Context, Config, io.Writer) (*GitHub, error)) int {
	app := newApplication(log)
	app.Reader = func(ctx context.Context, c Config) (PRListReader, error) { return factory(ctx, c, log) }
	return newCLI(app, selfUpdate).Run(ctx, args, in, out, log)
}
func runTestUpdateCLI(ctx context.Context, args []string, out, log io.Writer, updater func(context.Context) (UpdateResult, error)) int {
	return newCLI(&Application{}, updater).Run(ctx, append([]string{"update"}, args...), nil, out, log)
}
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(context.Background(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func cliRepo(t *testing.T, root, name, remote string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: true\n")
	mustGit(t, dir, "init", "--initial-branch=main")
	mustGit(t, dir, "remote", "add", "origin", remote)
	return dir
}
func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }
func ptr[T any](v T) *T            { return &v }
func validPR() PRInfo {
	return PRInfo{Number: 1, Title: "Update dependency example", URL: "https://github.com/o/r/pull/1", State: "open", Author: "renovate[bot]", Base: "main", Head: "renovate/dep", HeadSHA: "head", BaseSHA: "base"}
}
