package types

import (
	"slices"
	"testing"
)

func TestFindingsAtOrAboveSeverity(t *testing.T) {
	t.Parallel()
	f := Findings{Summary: "s", Items: []Finding{
		{ID: "e", Severity: FindingSeverityError},
		{ID: "w", Severity: "Warning"},
		{ID: "i", Severity: FindingSeverityInfo},
		{ID: "u", Severity: "critical"},
	}}
	for _, tc := range []struct {
		min  string
		want []string
	}{
		{"", []string{"e", "w", "i", "u"}},
		{FindingSeverityInfo, []string{"e", "w", "i"}},
		{FindingSeverityWarning, []string{"e", "w"}},
		{FindingSeverityError, []string{"e"}},
	} {
		got := FindingsAtOrAboveSeverity(f, tc.min)
		var ids []string
		for _, item := range got.Items {
			ids = append(ids, item.ID)
		}
		if !slices.Equal(ids, tc.want) {
			t.Fatalf("FindingsAtOrAboveSeverity(%q) = %v, want %v", tc.min, ids, tc.want)
		}
		if got.Summary != f.Summary {
			t.Fatalf("FindingsAtOrAboveSeverity(%q) dropped metadata: summary %q", tc.min, got.Summary)
		}
	}
}
