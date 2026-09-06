package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type listReaderStub struct {
	list    func() ([]PRInfo, error)
	details func(PRInfo, bool, bool) (CandidateFacts, error)
}

func (f listReaderStub) OpenPullRequests(context.Context, string) ([]PRInfo, error) { return f.list() }
func (f listReaderStub) CandidateDetails(_ context.Context, _ string, p PRInfo, files, commits bool) (CandidateFacts, error) {
	return f.details(p, files, commits)
}
func TestPartialDetailsRetainUnknownAndContinue(t *testing.T) {
	calls := 0
	reader := listReaderStub{list: func() ([]PRInfo, error) {
		rows := []PRInfo{}
		for n := 1; n <= 3; n++ {
			p := validPR()
			p.Number = n
			if n == 3 {
				p.Base = "other"
			}
			rows = append(rows, p)
		}
		return rows, nil
	}, details: func(p PRInfo, files, commits bool) (CandidateFacts, error) {
		calls++
		if !files || commits || p.Number == 3 {
			t.Fatal("unexpected fetch", p, files, commits)
		}
		f := CandidateFacts{PR: p, FilesComplete: true, Files: []ChangedFile{{Filename: "go.mod"}}}
		if p.Number == 1 {
			f.FilesComplete = false
			return f, errors.New("unavailable")
		}
		return f, nil
	}}
	policy := defaultPolicy()
	policy.PullRequests.Files = []string{"go.mod"}
	result, err := listCandidates(context.Background(), reader, emptyPRResult(), policy, false)
	if err == nil || result.Complete || result.OpenRenovateCount == nil || *result.OpenRenovateCount != 3 || len(result.PullRequests) != 2 || result.PullRequests[0].Status != "unknown" || result.PullRequests[1].Status != "candidate" || calls != 2 || len(result.Errors) != 1 || result.Errors[0].PR != 1 {
		t.Fatal(result, err, calls)
	}
}
func TestEmptyPopulationVsZeroCandidates(t *testing.T) {
	for _, hasPR := range []bool{false, true} {
		reader := listReaderStub{list: func() ([]PRInfo, error) {
			if !hasPR {
				return []PRInfo{}, nil
			}
			p := validPR()
			p.Base = "develop"
			return []PRInfo{p}, nil
		}}
		result, err := listCandidates(context.Background(), reader, emptyPRResult(), defaultPolicy(), false)
		want := 0
		if hasPR {
			want = 1
		}
		if err != nil || !result.Complete || result.OpenRenovateCount == nil || *result.OpenRenovateCount != want || len(result.PullRequests) != 0 {
			t.Fatal(result, err)
		}
	}
}
func TestPRListCLIExitCodesAndNoLocalEffects(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	dir := cliRepo(t, root, "repo", "https://github.com/o/r.git")
	marker := filepath.Join(root, "should-not-exist")
	writeFile(t, filepath.Join(dir, "renstiq.yaml"), "version: 1\nenabled: true\npost_merge:\n- id: never\n  timing: after_repo\n  command: [touch, "+strconvQuote(marker)+"]\n")
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	writeFile(t, filepath.Join(state, "legacy"), "untouched")
	before := mustGit(t, dir, "status", "--porcelain=v1")
	for _, kind := range []string{"empty", "excluded", "partial", "auth"} {
		var out, log bytes.Buffer
		app := newApplication(&log)
		app.Reader = func(context.Context, Config) (PRListReader, error) {
			if kind == "auth" {
				return nil, errors.New("authentication failed")
			}
			return listReaderStub{list: func() ([]PRInfo, error) {
				if kind == "empty" {
					return []PRInfo{}, nil
				}
				p := validPR()
				if kind == "excluded" {
					p.Base = "other"
					return []PRInfo{p}, nil
				}
				return []PRInfo{p}, errors.New("page 2 failed")
			}}, nil
		}
		code := newCLI(app, nil).Run(context.Background(), []string{"pr", "list", "--repo", dir}, nil, &out, &log)
		var r PRListResult
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatal(err, out.String())
		}
		assertCLIOutputSchema(t, "pr-list", out.Bytes())
		failed := kind == "partial" || kind == "auth"
		if failed {
			if code != 1 || r.Complete || r.OpenRenovateCount != nil || log.Len() == 0 || len(r.Errors) == 0 {
				t.Fatal(kind, code, r, log.String())
			}
		} else if code != 0 || !r.Complete || r.OpenRenovateCount == nil {
			t.Fatal(kind, code, r)
		}
		if kind == "partial" && len(r.PullRequests) != 1 {
			t.Fatal("successful portion lost", r)
		}
	}
	if after := mustGit(t, dir, "status", "--porcelain=v1"); before != after {
		t.Fatal("local changes", before, after)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("post merge executed", err)
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 1 || entries[0].Name() != "legacy" {
		t.Fatal("state changed", entries, err)
	}
}
