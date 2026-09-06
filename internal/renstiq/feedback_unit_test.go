package renstiq

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestCommentIntentAndCompletionSaveBoundaries(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			s, r := newMemorySession()
			s.failAt = failAt
			writes := 0
			remote := commentFake{create: func(string) (Comment, error) {
				writes++
				s.events = append(s.events, "comment:create")
				return Comment{ID: 1}, nil
			}}
			service := CommentService{Repo: "o/r", Remote: remote, Journal: s, Now: fixedClock}
			d := reviewCommentDecision(r)
			if _, err := service.Ensure(context.Background(), r, d, nil); err == nil {
				t.Fatal("save failure ignored")
			}
			if failAt == 1 {
				if writes != 0 || !reflect.DeepEqual(s.events, []string{"save:pending"}) {
					t.Fatal(writes, s.events)
				}
			} else {
				if writes != 1 || !reflect.DeepEqual(s.events, []string{"save:pending", "comment:create", "save:success"}) {
					t.Fatal(writes, s.events)
				}
				r = s.reload()
				op, err := service.Ensure(context.Background(), r, d, nil)
				if err != nil || op.Status != "unknown" || writes != 1 {
					t.Fatal(op, err, writes)
				}
			}
		})
	}
}

func TestPureCommentPlanning(t *testing.T) {
	_, r := newMemorySession()
	d := reviewCommentDecision(r)
	body := feedbackBody(d)
	owned := Comment{ID: 3, Body: body, Automation: true, URL: "owned"}
	cases := []struct {
		name     string
		modify   func(*Decision)
		comments []Comment
		old      *Operation
		status   string
	}{
		{name: "new", status: "pending"},
		{name: "identical owned", comments: []Comment{owned}, status: "skipped"},
		{name: "unresolved", old: &Operation{Status: "pending"}, status: "unknown"},
		{name: "unknown", old: &Operation{Status: "unknown"}, status: "unknown"},
		{name: "known rejection can retry", old: &Operation{Status: "failed"}, status: "pending"},
		{name: "missing equivalent", modify: func(d *Decision) { d.Feedback.EquivalentID = 9 }, status: "failed"},
		{name: "unowned update", modify: func(d *Decision) { d.Feedback.UpdateID = 9 }, comments: []Comment{{ID: 9}}, status: "failed"},
		{name: "owned update", modify: func(d *Decision) { d.Feedback.UpdateID = 9 }, comments: []Comment{{ID: 9, Automation: true}}, status: "pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := d
			if tc.modify != nil {
				tc.modify(&input)
			}
			if got := planComment(input, tc.comments, tc.old); got.Operation.Status != tc.status {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestCommentUncertainAndRejectedOutcomes(t *testing.T) {
	for _, mode := range []string{"confirmed", "unknown", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			s, r := newMemorySession()
			d := reviewCommentDecision(r)
			writes := 0
			remote := commentFake{
				create: func(string) (Comment, error) {
					writes++
					if mode == "rejected" {
						return Comment{}, &RejectedWrite{Cause: effectError}
					}
					return Comment{}, effectError
				},
				list: func() ([]Comment, error) {
					if mode == "confirmed" {
						return []Comment{{ID: 1, Body: feedbackBody(d), Automation: true}}, nil
					}
					return nil, nil
				},
			}
			service := CommentService{Repo: "o/r", Remote: remote, Journal: s, Now: fixedClock}
			op, err := service.Ensure(context.Background(), r, d, nil)
			want := map[string]string{"confirmed": "success", "unknown": "unknown", "rejected": "failed"}[mode]
			if err != nil || op.Status != want || writes != 1 {
				t.Fatal(op, err, writes)
			}
			if mode == "unknown" {
				r = s.reload()
				op, err = service.Ensure(context.Background(), r, d, nil)
				if err != nil || op.Status != "unknown" || writes != 1 {
					t.Fatal(op, err, writes)
				}
			}
		})
	}
}

func TestRejectedWritePreservesCause(t *testing.T) {
	if !errors.Is(&RejectedWrite{Cause: effectError}, effectError) || !rejectedWrite(&RejectedWrite{Cause: effectError}) || rejectedWrite(effectError) {
		t.Fatal("write error contract broken")
	}
}
