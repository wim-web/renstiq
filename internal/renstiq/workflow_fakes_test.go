package renstiq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func fixedClock() time.Time { return fixedTime }

// Persists a separate snapshot, so tests can really reload the last durable
// intent after a failed save rather than reuse a mutated in-memory Run.
type memorySession struct {
	state         State
	disk          []byte
	saves, failAt int
	events        []string
	closed        bool
}

func newMemorySession() (*memorySession, *Run) {
	p := defaultPolicy()
	s := &memorySession{state: State{Version: 1, Repo: "o/r", Runs: []Run{{ID: "run", Phase: PhaseOpen, Policy: p, ConfigDigest: digest(p), Merges: []MergeRecord{}, Operations: []Operation{}}}}}
	s.disk, _ = json.Marshal(s.state)
	return s, &s.state.Runs[0]
}
func (s *memorySession) StateView() *State { return &s.state }
func (s *memorySession) Close()            { s.closed = true; s.events = append(s.events, "close") }
func (s *memorySession) Save() error {
	s.saves++
	status := s.state.Latest().Phase
	r := s.state.Latest()
	if len(r.Merges) > 0 {
		status = r.Merges[len(r.Merges)-1].Status
	}
	if len(r.Operations) > 0 {
		status = r.Operations[len(r.Operations)-1].Status
	}
	s.events = append(s.events, "save:"+status)
	if s.saves == s.failAt {
		return fmt.Errorf("save %d failed", s.saves)
	}
	b, err := json.Marshal(s.state)
	if err == nil {
		s.disk = b
	}
	return err
}
func (s *memorySession) reload() *Run {
	if err := json.Unmarshal(s.disk, &s.state); err != nil {
		panic(err)
	}
	return s.state.Latest()
}

type commentFake struct {
	create func(string) (Comment, error)
	update func(int64, string) (Comment, error)
	list   func() ([]Comment, error)
}

func (f commentFake) CreateComment(_ context.Context, _ string, _ int, b string) (Comment, error) {
	return f.create(b)
}
func (f commentFake) UpdateComment(_ context.Context, _ string, id int64, b string) (Comment, error) {
	return f.update(id, b)
}
func (f commentFake) Comments(context.Context, string, int) ([]Comment, error) { return f.list() }

type mergeFake struct {
	wait  func() (PullRequest, error)
	state func() (PRState, error)
	merge func() (MergeReceipt, error)
}

func (f mergeFake) WaitPR(context.Context, string, int, Revision, bool) (PullRequest, error) {
	return f.wait()
}
func (f mergeFake) PullRequestState(context.Context, string, int) (PRState, error) { return f.state() }
func (f mergeFake) MergePullRequest(context.Context, string, int, string, string) (MergeReceipt, error) {
	return f.merge()
}

type runnerFunc func(context.Context, CommandSpec) (int, error)

func (f runnerFunc) Run(ctx context.Context, s CommandSpec) (int, error) { return f(ctx, s) }

type logFake struct {
	bytes.Buffer
	events  *[]string
	syncErr error
	closed  bool
}

func (l *logFake) Path() string { return "memory://operation-log" }
func (l *logFake) Sync() error  { *l.events = append(*l.events, "log:sync"); return l.syncErr }
func (l *logFake) Close() error {
	l.closed = true
	*l.events = append(*l.events, "log:close")
	return nil
}

type logStoreFake struct {
	log *logFake
	err error
}

func (s logStoreFake) Open(string, string) (OperationLog, error) {
	*s.log.events = append(*s.log.events, "log:open")
	if s.err != nil {
		return nil, s.err
	}
	return s.log, nil
}

var effectError = errors.New("effect failed")
