package renstiq

import (
	"reflect"
	"testing"
)

func TestInstructionMatchingAndExclusions(t *testing.T) {
	f := CandidateFacts{PR: validPR(), Files: []ChangedFile{{Filename: "new/go.mod", Previous: "go.mod", Status: "renamed"}}, FilesComplete: true}
	f.PR.Updates = []DependencyUpdate{{Dependency: "one", Type: "patch"}, {Dependency: "two", Type: "minor"}}
	item := Instruction{Entry: Entry{ID: "task", Enabled: true}, Instructions: "inspect"}
	tests := []struct {
		name           string
		match, exclude Match
		want           bool
	}{
		{"unconditional", Match{}, Match{}, true},
		{"rename", Match{Files: []string{"go.mod"}}, Match{}, true},
		{"all fields", Match{Files: []string{"**/go.mod"}, Dependencies: []string{"one"}, Types: []string{"patch"}}, Match{}, true},
		{"same update", Match{Dependencies: []string{"one"}, Types: []string{"minor"}}, Match{}, false},
		{"excluded update does not veto other updates", Match{}, Match{Dependencies: []string{"one"}}, true},
		{"all eligible excluded", Match{Types: []string{"patch"}}, Match{Dependencies: []string{"one"}}, false},
		{"files exclude whole PR", Match{}, Match{Files: []string{"go.mod"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			item.Match, item.Exclude = tc.match, tc.exclude
			if got := instructionMatches(item, f); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	item.Enabled = false
	if instructionMatches(item, f) {
		t.Fatal("disabled task matched")
	}
}
func TestInstructionOnlyTasksAndCommonRepoAddition(t *testing.T) {
	p, err := policyFiles(t, "after_repo:\n- id: common\n  instructions: common work\n", "after_merge:\n- id: each\n  instructions: arbitrary work\nafter_repo:\n- id: repo\n  instructions: repo work\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.AfterMerge) != 1 || len(p.AfterRepo) != 2 || !reflect.DeepEqual([]string{p.AfterRepo[0].ID, p.AfterRepo[1].ID}, []string{"common", "repo"}) {
		t.Fatal(p)
	}
}
