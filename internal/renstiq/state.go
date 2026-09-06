package renstiq

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// Store owns the repository lock and durable JSON storage, not run lifecycle rules.
type Store struct {
	File  string
	lock  *os.File
	State State
}

// StateView is valid while the session holds the repository lock.
func (s *Store) StateView() *State { return &s.State }

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
