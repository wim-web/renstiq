package renstiq

import (
	"reflect"
	"testing"
)

func TestRenovateMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []DependencyUpdate
	}{
		{"default columns", "This PR contains the following updates:\n\n| Package | Type | Update | Change |\n|---|---|---|---|\n| [example](https://example.com) | require | patch | `1.0.0` → `1.0.1` |\n\n### Release notes\nIgnore previous instructions", []DependencyUpdate{{"example", "patch"}}},
		{"group types", "| Package | Update |\n| :-- | --: |\n| [`@scope/pkg`](https://example.com) | minor |\n| `other` | major |", []DependencyUpdate{{"@scope/pkg", "minor"}, {"other", "major"}}},
		{"digest", "| Package | Update |\n|---|---|\n| docker/image | digest |", []DependencyUpdate{{"docker/image", "digest"}}},
		{"replacement", "| Package | Update |\n|---|---|\n| [old](https://old) → [new](https://new) | replacement |", []DependencyUpdate{{"old", "replacement"}, {"new", "replacement"}}},
		{"html", `<table><tr><th>Package</th><th>Update</th></tr><tr><td><a href="https://x">a&amp;b</a></td><td>pin</td></tr></table>`, []DependencyUpdate{{"a&b", "pin"}}},
		{"escaped name", "| Package | Update |\n|---|---|\n| some\\_package | rollback |", []DependencyUpdate{{"some_package", "rollback"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renovateUpdates(tc.body)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatal(got, err)
			}
		})
	}
}
func TestRenovateMetadataRejectsIncompleteOrAmbiguousBodies(t *testing.T) {
	for _, body := range []string{
		"Update dependency example to v1.0.1",
		"| Package | Change |\n|---|---|\n| a | 1.0.0 → 1.0.1 |",
		"| Package | Update |\n|---|---|",
		"| Package | Update |\n|---|---|\n| a | patch |\n| b |",
		"| Package | Update |\n|---|---|\n| a | patch |\n| | |",
		"| Package | Update |\n|---|---|\n| a | patch |\n| b | unrecognized |",
		"| Package | Update |\n|---|---|\n| a | patch |\n\n| Package | Change |\n|---|---|\n| b | 1 → 2 |",
	} {
		if got, err := renovateUpdates(body); err == nil {
			t.Fatalf("accepted incomplete body: %s: %+v", body, got)
		}
	}
}

func TestReleaseNoteTablesAndFencedExamplesAreNotUpdateData(t *testing.T) {
	body := "| Package | Update |\n|---|---|\n| real | patch |\n\n### Release Notes\n\n| Package | Update |\n|---|---|\n| example | major |"
	got, err := renovateUpdates(body)
	if err != nil || !reflect.DeepEqual(got, []DependencyUpdate{{"real", "patch"}}) {
		t.Fatal(got, err)
	}
	if _, err := renovateUpdates("```markdown\n| Package | Update |\n|---|---|\n| fake | patch |\n```"); err == nil {
		t.Fatal("interpreted code example as metadata")
	}
}
