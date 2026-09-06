package renstiq

import (
	"reflect"
	"testing"
)

func TestRunSessionReusesActiveWithoutGeneratingID(t *testing.T) {
	s, r := newMemorySession()
	runs := RunSession{State: &s.state, Journal: s, Now: fixedClock, NewID: func() string { t.Fatal("active run generated another ID"); return "" }}
	found, err := runs.Current(r.Policy, r.ConfigDigest)
	if err != nil || found != r || s.saves != 0 {
		t.Fatal(found, err, s.saves)
	}
	if _, err := runs.Current(r.Policy, "changed"); err == nil || s.saves != 0 {
		t.Fatal(err, s.saves)
	}
}

func TestRunLifecycleChecksTransitions(t *testing.T) {
	_, r := newMemorySession()
	if err := r.Finish(); err == nil || r.Phase != PhaseOpen {
		t.Fatal("finished without finalization")
	}
	r.Operations = []Operation{{ID: "post", Kind: "post_merge", Status: "unknown"}}
	if err := r.BeginFinalization(); err == nil || r.Phase != PhaseOpen {
		t.Fatal("unknown operation allowed finalization")
	}
	r.Operations[0].Status = "success"
	if err := r.BeginFinalization(); err != nil {
		t.Fatal(err)
	}
	if err := r.RequireOpen(); err == nil {
		t.Fatal("finalizing run accepts merges")
	}
	if err := r.Finish(); err != nil || r.Phase != PhaseFinished {
		t.Fatal(err, r.Phase)
	}
	if err := r.BeginFinalization(); err == nil {
		t.Fatal("finished run reopened")
	}
}

func TestRunSessionAbandonKeepsHistoryAndUsesInjectedTime(t *testing.T) {
	s, r := newMemorySession()
	r.Operations = []Operation{{ID: "post", Kind: "post_merge", Status: "unknown", Error: "response lost"}}
	before := r.Operations[0]
	runs := RunSession{State: &s.state, Journal: s, Now: fixedClock, NewID: func() string { return "new" }}
	if err := runs.Abandon(r.ID, " "); err == nil || s.saves != 0 {
		t.Fatal(err, s.saves)
	}
	if err := runs.Abandon(r.ID, "operator reconciled"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Operations[0], before) || r.Phase != PhaseFinished {
		t.Fatal(r)
	}
	audit := r.Operations[1]
	if audit.Started != timestamp(fixedTime) || audit.Finished != audit.Started {
		t.Fatal(audit)
	}
	next, err := runs.Current(r.Policy, r.ConfigDigest)
	if err != nil || next.ID != "new" || next.Phase != PhaseOpen || len(next.Operations) != 0 {
		t.Fatal(next, err)
	}
}

func TestStateRejectsDuplicateOrActiveRun(t *testing.T) {
	s, r := newMemorySession()
	if _, err := s.state.StartRun("next", r.Policy, r.ConfigDigest); err == nil {
		t.Fatal("active run replaced")
	}
	r.Phase = PhaseFinished
	for _, id := range []string{"", "run"} {
		if _, err := s.state.StartRun(id, r.Policy, r.ConfigDigest); err == nil {
			t.Fatal("bad ID", id)
		}
	}
}
