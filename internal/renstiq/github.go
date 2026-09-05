package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Check struct {
	Name       string `json:"name"`
	Workflow   string `json:"workflow,omitempty"`
	AppID      int64  `json:"app_id,omitempty"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url,omitempty"`
}
type Comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	URL  string `json:"html_url"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Automation bool `json:"automation"`
}
type PullRequest struct {
	Number            int             `json:"number"`
	Title             string          `json:"title"`
	Body              string          `json:"body"`
	URL               string          `json:"url"`
	Author            string          `json:"author"`
	State             string          `json:"state"`
	Draft             bool            `json:"draft"`
	Head              string          `json:"head_branch"`
	Base              string          `json:"base_branch"`
	HeadSHA           string          `json:"head_sha"`
	BaseSHA           string          `json:"base_sha"`
	Merged            bool            `json:"merged"`
	MergeCommit       string          `json:"merge_commit"`
	Mergeable         *bool           `json:"mergeable"`
	MergeState        string          `json:"merge_state"`
	ReviewDecision    string          `json:"review_decision"`
	UnresolvedThreads int             `json:"unresolved_threads"`
	Files             []string        `json:"files"`
	FileDetails       []ChangedFile   `json:"file_details"`
	CommitAuthors     []string        `json:"commit_authors"`
	Checks            []Check         `json:"checks"`
	Comments          []Comment       `json:"comments"`
	ReviewComments    []Comment       `json:"review_comments"`
	Reviews           json.RawMessage `json:"reviews"`
	Labels            []string        `json:"labels"`
	Diff              string          `json:"diff"`
	Reasons           []string        `json:"merge_blockers"`
}
type ChangedFile struct {
	Filename string `json:"filename"`
	Previous string `json:"previous_filename,omitempty"`
	Status   string `json:"status"`
	Patch    string `json:"patch,omitempty"`
}
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
	Poll    time.Duration
	Log     io.Writer
	Sleep   func(context.Context, time.Duration) error
	Actor   string
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
	return &GitHub{BaseURL: "https://api.github.com", Token: token, HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}, Retry: c.Retry, Poll: time.Duration(c.CI.PollSeconds * float64(time.Second)), Log: log, Sleep: sleepContext}, nil
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
func (g *GitHub) request(ctx context.Context, method, path string, input any, out any, read bool) error {
	attempts := 1
	if read {
		attempts = g.Retry.MaxAttempts
	}
	if attempts < 1 {
		attempts = 1
	}
	var body []byte
	var e error
	if input != nil {
		body, e = json.Marshal(input)
		if e != nil {
			return e
		}
	}
	for i := 0; i < attempts; i++ {
		req, e := http.NewRequestWithContext(ctx, method, g.BaseURL+path, bytes.NewReader(body))
		if e != nil {
			return e
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
				return errors.New("GitHub response exceeds 64 MiB; refusing truncated data")
			}
			if readErr != nil {
				e = readErr
				retry = true
			} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				var message struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal(b, &message)
				e = &APIError{resp.StatusCode, method, path, message.Message}
				retry = resp.StatusCode >= 500 || resp.StatusCode == 429 || resp.StatusCode == 408 || (resp.StatusCode == 403 && (resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != ""))
				if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && time.Duration(seconds)*time.Second > delay {
					delay = time.Duration(seconds) * time.Second
				}
			} else {
				if out == nil {
					return nil
				}
				if raw, ok := out.(*string); ok {
					*raw = string(b)
					return nil
				}
				if e = json.Unmarshal(b, out); e == nil {
					return nil
				}
				retry = true
			}
		}
		if !read || !retry || i+1 == attempts {
			return e
		}
		if g.Log != nil {
			fmt.Fprintf(g.Log, "GitHub状態取得を再試行します (%d/%d): %s\n", i+1, attempts, e)
		}
		if err := g.Sleep(ctx, delay); err != nil {
			return err
		}
	}
	return e
}
func (g *GitHub) get(ctx context.Context, path string, out any) error {
	return g.request(ctx, "GET", path, nil, out, true)
}
func (g *GitHub) write(ctx context.Context, method, path string, v, out any) error {
	return g.request(ctx, method, path, v, out, false)
}
func pages[T any](ctx context.Context, g *GitHub, path string) ([]T, error) {
	out := []T{}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	for page := 1; ; page++ {
		var a []T
		if e := g.get(ctx, path+sep+"per_page=100&page="+strconv.Itoa(page), &a); e != nil {
			return nil, e
		}
		out = append(out, a...)
		if len(a) < 100 {
			return out, nil
		}
	}
}
func (g *GitHub) actor(ctx context.Context) (string, error) {
	if g.Actor != "" {
		return g.Actor, nil
	}
	var u struct {
		Login string `json:"login"`
	}
	if e := g.get(ctx, "/user", &u); e != nil {
		return "", e
	}
	g.Actor = u.Login
	return g.Actor, nil
}

type rawPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"html_url"`
	State  string `json:"state"`
	Draft  bool   `json:"draft"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		SHA  string `json:"sha"`
		Ref  string `json:"ref"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"base"`
	Merged       bool   `json:"merged"`
	MergeCommit  string `json:"merge_commit_sha"`
	Mergeable    *bool  `json:"mergeable"`
	ChangedFiles int    `json:"changed_files"`
	Commits      int    `json:"commits"`
	Labels       []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (g *GitHub) raw(ctx context.Context, repo string, n int) (rawPR, error) {
	var p rawPR
	e := g.get(ctx, fmt.Sprintf("/repos/%s/pulls/%d", repo, n), &p)
	return p, e
}
func (g *GitHub) list(ctx context.Context, repo string) ([]rawPR, error) {
	return pages[rawPR](ctx, g, "/repos/"+repo+"/pulls?state=open")
}
func (g *GitHub) comments(ctx context.Context, repo string, n int) ([]Comment, error) {
	a, e := pages[Comment](ctx, g, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, n))
	if e != nil {
		return nil, e
	}
	actor, e := g.actor(ctx)
	if e != nil {
		return nil, e
	}
	for i := range a {
		a[i].Automation = a[i].User.Login == actor && strings.Contains(a[i].Body, "<!-- renstiq:v1:")
	}
	return a, nil
}
func (g *GitHub) checks(ctx context.Context, repo, sha string) ([]Check, error) {
	out := []Check{}
	workflow := map[string]string{}
	for page := 1; ; page++ {
		var result struct {
			Runs []struct {
				Name       string `json:"name"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				URL        string `json:"html_url"`
				App        struct {
					ID   int64  `json:"id"`
					Slug string `json:"slug"`
				} `json:"app"`
			} `json:"check_runs"`
		}
		if e := g.get(ctx, fmt.Sprintf("/repos/%s/commits/%s/check-runs?filter=latest&per_page=100&page=%d", repo, sha, page), &result); e != nil {
			return nil, e
		}
		for _, v := range result.Runs {
			c := Check{Name: v.Name, Status: v.Status, Conclusion: v.Conclusion, URL: v.URL, AppID: v.App.ID}
			if v.App.Slug == "github-actions" {
				prefix := "https://github.com/" + repo + "/actions/runs/"
				if strings.HasPrefix(v.URL, prefix) {
					id := strings.Split(strings.TrimPrefix(v.URL, prefix), "/")[0]
					if _, e := strconv.ParseInt(id, 10, 64); e != nil {
						return nil, e
					}
					name, ok := workflow[id]
					if !ok {
						var run struct {
							Name string `json:"name"`
						}
						if e := g.get(ctx, "/repos/"+repo+"/actions/runs/"+id, &run); e != nil {
							return nil, e
						}
						name = run.Name
						workflow[id] = name
					}
					c.Workflow = name
				}
			}
			out = append(out, c)
		}
		if len(result.Runs) < 100 {
			break
		}
	}
	statuses, e := pages[struct {
		Context string `json:"context"`
		State   string `json:"state"`
		URL     string `json:"target_url"`
	}](ctx, g, "/repos/"+repo+"/commits/"+sha+"/statuses")
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	for _, s := range statuses {
		if seen[s.Context] {
			continue
		}
		seen[s.Context] = true
		c := Check{Name: s.Context, Status: "completed", Conclusion: s.State, URL: s.URL}
		if s.State == "pending" {
			c.Status = "pending"
			c.Conclusion = ""
		}
		out = append(out, c)
	}
	return out, nil
}
func (g *GitHub) reviewState(ctx context.Context, repo string, n int, p *PullRequest) error {
	parts := strings.Split(repo, "/")
	var cursor any
	for {
		q := `query($owner:String!,$name:String!,$number:Int!,$cursor:String){repository(owner:$owner,name:$name){pullRequest(number:$number){headRefOid baseRefOid reviewDecision mergeStateStatus reviewThreads(first:100,after:$cursor){nodes{isResolved} pageInfo{hasNextPage endCursor}}}}}`
		var r struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
			Data struct {
				Repository struct {
					PR *struct {
						Head     string `json:"headRefOid"`
						Base     string `json:"baseRefOid"`
						Decision string `json:"reviewDecision"`
						State    string `json:"mergeStateStatus"`
						Threads  struct {
							Nodes []struct {
								Resolved bool `json:"isResolved"`
							} `json:"nodes"`
							Page struct {
								Next   bool   `json:"hasNextPage"`
								Cursor string `json:"endCursor"`
							} `json:"pageInfo"`
						} `json:"reviewThreads"`
					} `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
		}
		if e := g.request(ctx, "POST", "/graphql", map[string]any{"query": q, "variables": map[string]any{"owner": parts[0], "name": parts[1], "number": n, "cursor": cursor}}, &r, true); e != nil {
			return e
		}
		if len(r.Errors) > 0 {
			return fmt.Errorf("GitHub GraphQL: %s", r.Errors[0].Message)
		}
		pr := r.Data.Repository.PR
		if pr == nil {
			return errors.New("GitHub GraphQL returned no pull request")
		}
		if pr.Head != p.HeadSHA || pr.Base != p.BaseSHA {
			return errors.New("review_required: PR changed during snapshot")
		}
		p.ReviewDecision = pr.Decision
		p.MergeState = pr.State
		for _, t := range pr.Threads.Nodes {
			if !t.Resolved {
				p.UnresolvedThreads++
			}
		}
		if !pr.Threads.Page.Next {
			break
		}
		cursor = pr.Threads.Page.Cursor
	}
	return nil
}
func (g *GitHub) snapshot(ctx context.Context, repo string, n int, withDiff bool) (PullRequest, error) {
	p := PullRequest{}
	raw, e := g.raw(ctx, repo, n)
	if e != nil {
		return p, e
	}
	p = PullRequest{Number: n, Title: raw.Title, Body: raw.Body, URL: raw.URL, Author: raw.User.Login, State: raw.State, Draft: raw.Draft, Head: raw.Head.Ref, Base: raw.Base.Ref, HeadSHA: raw.Head.SHA, BaseSHA: raw.Base.SHA, Merged: raw.Merged, MergeCommit: raw.MergeCommit, Mergeable: raw.Mergeable, Files: []string{}, Checks: []Check{}}
	base := fmt.Sprintf("/repos/%s/pulls/%d", repo, n)
	p.FileDetails, e = pages[ChangedFile](ctx, g, base+"/files")
	if e != nil {
		return p, e
	}
	if len(p.FileDetails) != raw.ChangedFiles {
		return p, errors.New("changed file list is incomplete")
	}
	for _, f := range p.FileDetails {
		p.Files = append(p.Files, f.Filename)
		if f.Previous != "" {
			p.Files = append(p.Files, f.Previous)
		}
	}
	commits, e := pages[struct {
		Author *struct {
			Login string `json:"login"`
		} `json:"author"`
	}](ctx, g, base+"/commits")
	if e != nil {
		return p, e
	}
	if len(commits) != raw.Commits {
		return p, errors.New("commit list is incomplete")
	}
	for _, c := range commits {
		login := "unknown"
		if c.Author != nil {
			login = c.Author.Login
		}
		p.CommitAuthors = append(p.CommitAuthors, login)
	}
	p.Checks, e = g.checks(ctx, repo, p.HeadSHA)
	if e != nil {
		return p, e
	}
	p.Comments, e = g.comments(ctx, repo, n)
	if e != nil {
		return p, e
	}
	p.ReviewComments, e = pages[Comment](ctx, g, base+"/comments")
	if e != nil {
		return p, e
	}
	reviews, e := pages[json.RawMessage](ctx, g, base+"/reviews")
	if e != nil {
		return p, e
	}
	p.Reviews, _ = json.Marshal(reviews)
	if e = g.reviewState(ctx, repo, n, &p); e != nil {
		return p, e
	}
	for _, l := range raw.Labels {
		p.Labels = append(p.Labels, l.Name)
	}
	if withDiff {
		p.Diff, e = g.diff(ctx, base)
		if e != nil {
			return p, e
		}
	}
	latest, e := g.raw(ctx, repo, n)
	if e != nil {
		return p, e
	}
	if latest.Head.SHA != p.HeadSHA || latest.Base.SHA != p.BaseSHA || latest.State != p.State {
		return p, errors.New("review_required: PR changed during snapshot")
	}
	return p, nil
}
func (g *GitHub) diff(ctx context.Context, path string) (string, error) { // Use a shallow client copy with a transport wrapper to set the media type.
	copy := *g
	client := *g.HTTP
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = acceptTransport{transport, "application/vnd.github.diff"}
	copy.HTTP = &client
	var s string
	e := copy.get(ctx, path, &s)
	return s, e
}

type acceptTransport struct {
	base   http.RoundTripper
	accept string
}

func (t acceptTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Accept", t.accept)
	return t.base.RoundTrip(r)
}
func pending(checks []Check) bool {
	for _, c := range checks {
		switch c.Status {
		case "queued", "pending", "in_progress", "waiting", "requested":
			return true
		}
	}
	return false
}
func (g *GitHub) waitPR(ctx context.Context, repo string, n int, head, base string, withDiff bool) (PullRequest, error) {
	for {
		p, e := g.snapshot(ctx, repo, n, withDiff)
		if e != nil {
			return p, e
		}
		if head == "" {
			head = p.HeadSHA
			base = p.BaseSHA
		}
		if p.HeadSHA != head || p.BaseSHA != base {
			return p, errors.New("review_required: head or base SHA changed; inspect and investigate the new diff")
		}
		if !pending(p.Checks) {
			return p, nil
		}
		if g.Log != nil {
			fmt.Fprintf(g.Log, "%s#%d: CI完了を待っています\n", repo, n)
		}
		if e = g.Sleep(ctx, g.Poll); e != nil {
			return p, e
		}
	}
}
func escaped(s string) string { return url.PathEscape(s) }
