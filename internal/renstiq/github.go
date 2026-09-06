package renstiq

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type APIError struct {
	Status                int
	Method, Path, Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("GitHub %s %s: HTTP %d: %s", e.Method, e.Path, e.Status, e.Message)
}

type GitHub struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	Retry   Retry
	Log     io.Writer
	Sleep   func(context.Context, time.Duration) error
}

func NewGitHub(ctx context.Context, c Config, log io.Writer) (*GitHub, error) {
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		b, e := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com").Output()
		if e != nil {
			return nil, errors.New("GitHub authentication unavailable: set GH_TOKEN/GITHUB_TOKEN or run gh auth login --hostname github.com")
		}
		token = strings.TrimSpace(string(b))
	}
	return &GitHub{BaseURL: "https://api.github.com", Token: token, HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}, Retry: c.Retry, Log: log, Sleep: sleepContext}, nil
}
func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (g *GitHub) get(ctx context.Context, path string, out any) error {
	_, err := g.getResponse(ctx, path, out)
	return err
}
func (g *GitHub) getResponse(ctx context.Context, path string, out any) (http.Header, error) {
	attempts := g.Retry.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var e error
	for i := 0; i < attempts; i++ {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, g.BaseURL+path, nil)
		if e != nil {
			return nil, e
		}
		req.Header.Set("Authorization", "Bearer "+g.Token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "renstiq")
		req.Header.Set("Content-Type", "application/json")
		resp, err := g.HTTP.Do(req)
		retry := false
		delay := time.Duration(g.Retry.IntervalSeconds * float64(time.Second))
		if err != nil {
			e = errors.New("GitHub transport failed: " + err.Error())
			retry = true
		} else {
			b, readErr := io.ReadAll(io.LimitReader(resp.Body, (64<<20)+1))
			resp.Body.Close()
			if len(b) > 64<<20 {
				return nil, errors.New("GitHub response exceeds 64 MiB; refusing truncated data")
			}
			if readErr != nil {
				e = readErr
				retry = true
			} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				var message struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal(b, &message)
				e = &APIError{resp.StatusCode, http.MethodGet, path, message.Message}
				retry = resp.StatusCode >= 500 || resp.StatusCode == 429 || resp.StatusCode == 408 || (resp.StatusCode == 403 && (resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != ""))
				if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && time.Duration(seconds)*time.Second > delay {
					delay = time.Duration(seconds) * time.Second
				}
			} else {
				target := reflect.ValueOf(out)
				fresh := reflect.New(target.Elem().Type())
				if e = json.Unmarshal(b, fresh.Interface()); e == nil {
					target.Elem().Set(fresh.Elem())
					return resp.Header, nil
				}
				retry = true
			}
		}
		if !retry || i+1 == attempts {
			return nil, e
		}
		if g.Log != nil {
			fmt.Fprintf(g.Log, "GitHub状態取得を再試行します (%d/%d): %s\n", i+1, attempts, e)
		}
		if err := g.Sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, e
}
func pages[T any](ctx context.Context, g *GitHub, path string) ([]T, error) {
	out := []T{}
	seenPages := map[[32]byte]bool{}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	for page := 1; ; page++ {
		var a []T
		headers, e := g.getResponse(ctx, path+sep+"per_page=100&page="+strconv.Itoa(page), &a)
		if e != nil {
			return out, e
		}
		if a == nil {
			return out, errors.New("GitHub returned null instead of a page")
		}
		b, _ := json.Marshal(a)
		hash := sha256.Sum256(b)
		if seenPages[hash] {
			return out, errors.New("GitHub repeated a page; refusing incomplete data")
		}
		seenPages[hash] = true
		out = append(out, a...)
		link := headers.Get("Link")
		if !strings.Contains(link, `rel="next"`) && (link != "" || len(a) < 100) {
			return out, nil
		}
	}
}

type rawPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"html_url"`
	State  string `json:"state"`
	Draft  bool   `json:"draft"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"base"`
	ChangedFiles *int `json:"changed_files"`
	Commits      *int `json:"commits"`
}

func (p rawPR) info() PRInfo {
	return PRInfo{Number: p.Number, Title: p.Title, URL: p.URL, Author: p.User.Login, State: p.State, Draft: p.Draft, Head: p.Head.Ref, Base: p.Base.Ref, HeadSHA: p.Head.SHA, BaseSHA: p.Base.SHA}
}
func (g *GitHub) raw(ctx context.Context, repo string, n int) (rawPR, error) {
	var p rawPR
	err := g.get(ctx, fmt.Sprintf("/repos/%s/pulls/%d", repo, n), &p)
	return p, err
}
func (g *GitHub) OpenPullRequests(ctx context.Context, repo string) ([]PRInfo, error) {
	raw, err := pages[rawPR](ctx, g, "/repos/"+repo+"/pulls?state=open&sort=created&direction=asc")
	out := make([]PRInfo, 0, len(raw))
	for _, p := range raw {
		out = append(out, p.info())
		if p.Number <= 0 || p.User.Login == "" || p.State != "open" {
			err = errors.Join(err, errors.New("open PR list contains missing or inconsistent identity/state"))
		}
	}
	return out, err
}
func samePR(a, b PRInfo) bool {
	return a.Number == b.Number && a.HeadSHA == b.HeadSHA && a.BaseSHA == b.BaseSHA && a.State == b.State && a.Head == b.Head && a.Base == b.Base && a.Author == b.Author
}
func (g *GitHub) CandidateDetails(ctx context.Context, repo string, initial PRInfo, files, commits bool) (CandidateFacts, error) {
	facts := CandidateFacts{PR: initial}
	before, err := g.raw(ctx, repo, initial.Number)
	if err != nil {
		return facts, err
	}
	if !samePR(initial, before.info()) {
		facts.Changed = true
		return facts, errors.New("PR changed before detail retrieval")
	}
	base := fmt.Sprintf("/repos/%s/pulls/%d", repo, initial.Number)
	var failures []error
	if files {
		facts.Files, err = pages[ChangedFile](ctx, g, base+"/files")
		if err == nil {
			if before.ChangedFiles == nil || *before.ChangedFiles < 0 || len(facts.Files) != *before.ChangedFiles || *before.ChangedFiles > 3000 {
				err = errors.New("changed file count is missing, exceeds GitHub's 3000-file limit, or does not match the retrieved list")
			} else {
				seen := map[string]bool{}
				for _, f := range facts.Files {
					if f.Filename == "" || f.Status == "" || seen[f.Filename] || (f.Status == "renamed" && f.Previous == "") {
						err = errors.New("changed file information is incomplete or duplicated")
						break
					}
					seen[f.Filename] = true
				}
			}
		}
		facts.FilesComplete = err == nil
		if err != nil {
			failures = append(failures, err)
		}
	}
	if commits {
		type commit struct {
			SHA    string `json:"sha"`
			Author *struct {
				Login string `json:"login"`
			} `json:"author"`
		}
		rows, e := pages[commit](ctx, g, base+"/commits")
		if e == nil && (before.Commits == nil || *before.Commits < 0 || len(rows) != *before.Commits || *before.Commits > 250) {
			e = errors.New("commit count is missing, exceeds GitHub's 250-commit limit, or does not match the retrieved list")
		}
		seen := map[string]bool{}
		for _, c := range rows {
			author := ""
			if c.Author != nil {
				author = c.Author.Login
			}
			facts.CommitAuthors = append(facts.CommitAuthors, author)
			if author == "" {
				e = errors.Join(e, errors.New("commit author is unknown"))
			}
			if c.SHA == "" || seen[c.SHA] {
				e = errors.Join(e, errors.New("commit identity is missing or duplicated"))
			}
			seen[c.SHA] = true
		}
		facts.CommitsComplete = e == nil
		if e != nil {
			failures = append(failures, e)
		}
	}
	after, e := g.raw(ctx, repo, initial.Number)
	if e != nil {
		failures = append(failures, e)
	} else {
		if !samePR(initial, after.info()) {
			facts.Changed = true
			failures = append(failures, errors.New("PR changed during detail retrieval"))
		}
		if files && (after.ChangedFiles == nil || before.ChangedFiles == nil || *after.ChangedFiles != *before.ChangedFiles) {
			facts.FilesComplete = false
			failures = append(failures, errors.New("changed file count changed during retrieval"))
		}
		if commits && (after.Commits == nil || before.Commits == nil || *after.Commits != *before.Commits) {
			facts.CommitsComplete = false
			failures = append(failures, errors.New("commit count changed during retrieval"))
		}
	}
	return facts, errors.Join(failures...)
}
