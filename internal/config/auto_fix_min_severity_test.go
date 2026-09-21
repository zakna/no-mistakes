package config

import (
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/types"
)

// resolveAutoFixMinSeverity runs one global YAML document and one pushed repo
// document through the same path the daemon uses, and returns the minimum
// severity the review step's automatic fix loop would see.
func resolveAutoFixMinSeverity(t *testing.T, globalYAML, pushedRepoYAML string) string {
	t.Helper()
	global, err := LoadGlobalFromBytes([]byte(globalYAML))
	if err != nil {
		t.Fatalf("load global config: %v", err)
	}
	pushed, err := LoadRepoFromBytes([]byte(pushedRepoYAML))
	if err != nil {
		t.Fatalf("load repo config: %v", err)
	}
	return Merge(global, EffectiveRepoConfig(pushed, nil, false)).AutoFixMinSeverityFor(types.StepReview)
}

func TestAutoFixMinSeverity_UnsetKeepsEverySeverity(t *testing.T) {
	t.Parallel()
	if got := resolveAutoFixMinSeverity(t, "{}\n", "{}\n"); got != "" {
		t.Fatalf("AutoFixMinSeverityFor(review) = %q, want empty for an unset config", got)
	}
	if got := resolveAutoFixMinSeverity(t, "auto_fix_min_severity:\n  review: \"\"\n", "{}\n"); got != "" {
		t.Fatalf("AutoFixMinSeverityFor(review) = %q, want empty for an empty value", got)
	}
}

func TestAutoFixMinSeverity_GlobalAndRepoPrecedence(t *testing.T) {
	t.Parallel()
	const unset = "{}\n"
	const errorOnly = "auto_fix_min_severity:\n  review: error\n"
	const warning = "auto_fix_min_severity:\n  review: warning\n"

	for _, tc := range []struct {
		name         string
		global, repo string
		want         string
	}{
		{"global only", errorOnly, unset, types.FindingSeverityError},
		{"repo only", unset, warning, types.FindingSeverityWarning},
		{"repo overrides global", errorOnly, warning, types.FindingSeverityWarning},
		{"value is normalized", "auto_fix_min_severity:\n  review: \" Error \"\n", unset, types.FindingSeverityError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveAutoFixMinSeverity(t, tc.global, tc.repo); got != tc.want {
				t.Fatalf("AutoFixMinSeverityFor(review) = %q, want %q", got, tc.want)
			}
		})
	}
}

// Only the review step reads the option; other steps keep every severity.
func TestAutoFixMinSeverity_OnlyAppliesToReview(t *testing.T) {
	t.Parallel()
	cfg := &Config{AutoFixMinSeverity: AutoFixMinSeverity{Review: types.FindingSeverityError}}
	for _, step := range []types.StepName{types.StepTest, types.StepLint, types.StepCI, types.StepDocument, types.StepRebase} {
		if got := cfg.AutoFixMinSeverityFor(step); got != "" {
			t.Fatalf("AutoFixMinSeverityFor(%s) = %q, want empty", step, got)
		}
	}
}

func TestAutoFixMinSeverity_RejectsUnknownValue(t *testing.T) {
	t.Parallel()
	if _, err := LoadRepoFromBytes([]byte("auto_fix_min_severity:\n  review: errors\n")); err == nil {
		t.Fatal("expected an unknown repo auto_fix_min_severity.review to fail the config closed")
	} else if !strings.Contains(err.Error(), "auto_fix_min_severity.review") {
		t.Fatalf("error does not name the setting: %v", err)
	}
	if _, err := LoadGlobalFromBytes([]byte("auto_fix_min_severity:\n  review: blocking\n")); err == nil {
		t.Fatal("expected an unknown global auto_fix_min_severity.review to fail the config closed")
	}
}
