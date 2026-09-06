package renstiq

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	PhaseOpen       = "open"
	PhaseFinalizing = "finalizing"
	PhaseFinished   = "finished"
)

type MergeRecord struct {
	PR       int      `json:"pr"`
	HeadSHA  string   `json:"head_sha"`
	Base     string   `json:"base_branch"`
	Commit   string   `json:"commit"`
	Files    []string `json:"changed_files"`
	Decision Decision `json:"decision"`
	Status   string   `json:"status"`
	Error    string   `json:"error,omitempty"`
}
type Operation struct {
	Reason   string `json:"reason,omitempty"`
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	PR       int    `json:"pr,omitempty"`
	Error    string `json:"error,omitempty"`
	URL      string `json:"url,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Log      string `json:"log,omitempty"`
	Started  string `json:"started"`
	Finished string `json:"finished,omitempty"`
}
type Run struct {
	Decisions    []Decision    `json:"decisions"`
	ID           string        `json:"id"`
	Phase        string        `json:"phase"`
	Policy       Policy        `json:"policy"`
	ConfigDigest string        `json:"config_digest"`
	Merges       []MergeRecord `json:"merges"`
	Operations   []Operation   `json:"operations"`
}
type State struct {
	Version int    `json:"version"`
	Repo    string `json:"repo"`
	Runs    []Run  `json:"runs"`
}

// Journal must durably save the current state before returning success.
// Services never perform an external mutation after a failed intent save.
type Journal interface{ Save() error }

// StateSession holds an exclusive repository lock across the entire use case,
// including remote effects and their result saves, until Close is called.
type StateSession interface {
	Journal
	StateView() *State
	Close()
}

// RunSession coordinates pure lifecycle decisions with a durable journal.
// IDs and time are supplied by the composition root, not by the disk adapter.
type RunSession struct {
	State   *State
	Journal Journal
	NewID   func() string
	Now     func() time.Time
}

func (s RunSession) Current(p Policy, hash string) (*Run, error) {
	if r := s.State.Latest(); r != nil && r.Phase != PhaseFinished {
		if err := r.CheckConfig(hash); err != nil {
			return nil, err
		}
		return r, nil
	}
	r, err := s.State.StartRun(s.NewID(), p, hash)
	if err != nil {
		return nil, err
	}
	if err := s.Journal.Save(); err != nil {
		return nil, err
	}
	return r, nil
}

func (s RunSession) Abandon(id, reason string) error {
	r, err := s.State.Find(id)
	if err != nil {
		return err
	}
	if err := r.Abandon(reason, timestamp(s.Now())); err != nil {
		return err
	}
	return s.Journal.Save()
}

func (s *State) Latest() *Run {
	if len(s.Runs) == 0 {
		return nil
	}
	return &s.Runs[len(s.Runs)-1]
}

func (s *State) StartRun(id string, p Policy, hash string) (*Run, error) {
	if id == "" {
		return nil, errors.New("run ID must not be empty")
	}
	if r := s.Latest(); r != nil && r.Phase != PhaseFinished {
		return nil, errors.New("cannot create a run while another run is active")
	}
	for _, r := range s.Runs {
		if r.ID == id {
			return nil, fmt.Errorf("duplicate run ID: %s", id)
		}
	}
	s.Runs = append(s.Runs, Run{ID: id, Phase: PhaseOpen, Policy: p, ConfigDigest: hash, Merges: []MergeRecord{}, Operations: []Operation{}})
	return &s.Runs[len(s.Runs)-1], nil
}

func (s *State) Find(id string) (*Run, error) {
	for i := range s.Runs {
		if s.Runs[i].ID == id {
			return &s.Runs[i], nil
		}
	}
	return nil, fmt.Errorf("unknown run: %s", id)
}

func (r *Run) CheckConfig(hash string) error {
	if r.ConfigDigest != hash {
		return errors.New("configuration changed since run creation; use original configuration to finish pending actions")
	}
	return nil
}

func (r *Run) RequireOpen() error {
	if r.Phase != PhaseOpen {
		return errors.New("run is already finalizing/finished; additional merges are forbidden")
	}
	return nil
}

func (r *Run) BeginFinalization() error {
	if r.Phase != PhaseOpen && r.Phase != PhaseFinalizing {
		return errors.New("run cannot enter finalization")
	}
	if err := r.blocked(); err != nil {
		return err
	}
	r.Phase = PhaseFinalizing
	return nil
}

func (r *Run) Finish() error {
	if r.Phase != PhaseFinalizing {
		return errors.New("run must be finalizing before it can finish")
	}
	if err := r.blocked(); err != nil {
		return err
	}
	r.Phase = PhaseFinished
	return nil
}

// Abandon preserves failed/uncertain records and records an explicit operator decision.
func (r *Run) Abandon(reason, at string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("abandon requires a reason describing manual reconciliation")
	}
	if r.Phase == PhaseFinished {
		return errors.New("run is already finished")
	}
	r.Operations = append(r.Operations, Operation{ID: "abandon", Kind: "abandon", Status: "recorded", Reason: reason, Started: at, Finished: at})
	r.Phase = PhaseFinished
	return nil
}

func (r *Run) RecordDecision(d Decision) bool {
	for _, old := range r.Decisions {
		if digest(old) == digest(d) {
			return false
		}
	}
	r.Decisions = append(r.Decisions, d)
	return true
}

func (r *Run) PutOperation(op Operation) {
	if old := r.op(op.ID); old != nil {
		*old = op
	} else {
		r.Operations = append(r.Operations, op)
	}
}

func timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func now() string                  { return timestamp(time.Now()) }

func (r *Run) op(id string) *Operation {
	for i := range r.Operations {
		if r.Operations[i].ID == id {
			return &r.Operations[i]
		}
	}
	return nil
}
func (r *Run) blocked() error {
	for _, m := range r.Merges {
		if m.Status != "merged" && m.Status != "rejected" {
			return fmt.Errorf("merge #%d requires reconciliation: %s", m.PR, m.Status)
		}
	}
	for _, o := range r.Operations {
		if o.Kind == "delete_branch" && o.Status != "success" && o.Status != "skipped" {
			return fmt.Errorf("branch deletion %s requires reconciliation: %s", o.ID, o.Status)
		}
		if o.Kind == "post_merge" && o.Status != "success" {
			return fmt.Errorf("post-merge %s is %s; automatic retry is disabled", o.ID, o.Status)
		}
	}
	return nil
}
