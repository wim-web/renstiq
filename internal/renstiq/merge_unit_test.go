package renstiq

import (
	"context"
	"testing"
)

func TestMergeIntentAndCompletionSaveBoundaries(t *testing.T) {
	for _, failAt := range []int{2, 3} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			s, r := newMemorySession()
			s.failAt = failAt
			writes, reads := 0, 0
			d := validDecision()
			remote := mergeFake{
				wait: func() (PullRequest, error) { reads++; return validPR(), nil },
				state: func() (PRState, error) {
					return PRState{Merged: writes > 0, HeadSHA: d.HeadSHA, MergeCommit: "commit"}, nil
				},
				merge: func() (MergeReceipt, error) {
					writes++
					s.events = append(s.events, "merge")
					return MergeReceipt{Merged: true, Commit: "commit"}, nil
				},
			}
			service := MergeService{Repo: "o/r", Remote: remote, Journal: s, Reconciler: MergeReconciler{Repo: "o/r", Remote: remote, Journal: s}, AfterMerge: func(context.Context, *Run, bool) error { return nil }}
			if _, err := service.Merge(context.Background(), r, d); err == nil {
				t.Fatal("save failure ignored")
			}
			if reads != 2 {
				t.Fatal("mutable checks not repeated", reads)
			}
			if failAt == 2 {
				if writes != 0 {
					t.Fatal("write before durable intent")
				}
				return
			}
			if writes != 1 {
				t.Fatal(writes)
			}
			r = s.reload()
			if r.Merges[0].Status != "pending" {
				t.Fatal(r.Merges)
			}
			m, err := service.Merge(context.Background(), r, d)
			if err != nil || m.Status != "merged" || writes != 1 {
				t.Fatal(m, err, writes)
			}
		})
	}
}

func TestMergeRejectsBeforeExternalEffects(t *testing.T) {
	s, r := newMemorySession()
	r.Phase = PhaseFinalizing
	// Nil dependencies prove validation does not enter an external operation.
	service := MergeService{Repo: "o/r", Journal: s}
	if _, err := service.Merge(context.Background(), r, validDecision()); err == nil {
		t.Fatal("merge during finalization")
	}
	if s.saves != 0 {
		t.Fatal(s.saves)
	}
}
