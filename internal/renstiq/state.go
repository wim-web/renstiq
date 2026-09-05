package renstiq

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
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
type Store struct {
	File  string
	lock  *os.File
	State State
}

func stateHome() string {
	d := os.Getenv("XDG_STATE_HOME")
	if d == "" {
		h, _ := os.UserHomeDir()
		d = filepath.Join(h, ".local", "state")
	}
	return filepath.Join(d, "renstiq")
}
func openStore(dir, repo string) (*Store, error) {
	if dir == "" {
		dir = stateHome()
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, digest(repo))
	f, e := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("another renstiq process holds this repository lock")
	}
	s := &Store{File: path + ".json", lock: f, State: State{Version: 1, Repo: repo, Runs: []Run{}}}
	b, e := os.ReadFile(s.File)
	if errors.Is(e, os.ErrNotExist) {
		return s, nil
	}
	if e == nil {
		e = json.Unmarshal(b, &s.State)
	}
	if e != nil {
		s.Close()
		return nil, e
	}
	if s.State.Version != 1 || s.State.Repo != repo {
		s.Close()
		return nil, errors.New("state identity/version mismatch")
	}
	return s, nil
}
func (s *Store) Close() { _ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN); _ = s.lock.Close() }
func (s *Store) Save() error {
	b, e := json.MarshalIndent(s.State, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(s.File), ".renstiq-state-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Rename(tmp, s.File); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(s.File))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *Store) Current(p Policy, hash string) (*Run, error) {
	if len(s.State.Runs) > 0 {
		r := &s.State.Runs[len(s.State.Runs)-1]
		if r.Phase != "finished" {
			if r.ConfigDigest != hash {
				return nil, errors.New("active run configuration changed; finish/reconcile using its original configuration")
			}
			return r, nil
		}
	}
	r := Run{ID: fmt.Sprintf("%d", time.Now().UnixNano()), Phase: "open", Policy: p, ConfigDigest: hash, Merges: []MergeRecord{}, Operations: []Operation{}}
	s.State.Runs = append(s.State.Runs, r)
	if e := s.Save(); e != nil {
		return nil, e
	}
	return &s.State.Runs[len(s.State.Runs)-1], nil
}
func (s *Store) Find(id string) (*Run, error) {
	for i := range s.State.Runs {
		if s.State.Runs[i].ID == id {
			return &s.State.Runs[i], nil
		}
	}
	return nil, fmt.Errorf("unknown run: %s", id)
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
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
		if o.Kind == "post_merge" && o.Status != "success" {
			return fmt.Errorf("post-merge %s is %s; automatic retry is disabled", o.ID, o.Status)
		}
	}
	return nil
}

// Abandon closes a run only on an explicit operator request. Failed/unknown
// records are retained verbatim; no merge or command is retried or marked successful.
func (s *Store) Abandon(id, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("abandon requires a reason describing manual reconciliation")
	}
	r, err := s.Find(id)
	if err != nil {
		return err
	}
	if r.Phase == "finished" {
		return errors.New("run is already finished")
	}
	r.Operations = append(r.Operations, Operation{ID: "abandon", Kind: "abandon", Status: "recorded", Reason: reason, Started: now(), Finished: now()})
	r.Phase = "finished"
	return s.Save()
}
