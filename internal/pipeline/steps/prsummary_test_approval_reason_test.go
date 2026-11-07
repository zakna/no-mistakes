package steps

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

func untestedFindingsJSON(t *testing.T) string {
	t.Helper()
	return liveValidatedFindingsJSON(t, []types.TestScenario{
		{Name: "host answers the read-only probe", Result: types.ScenarioResultUntested, Reason: "no host access from the sandbox"},
	}, types.TestVerdictInconclusive)
}

func TestBuildTestingSummary_PublishesTestApprovalReason(t *testing.T) {
	t.Parallel()
	steps, rounds := testStepWithFindings(t, untestedFindingsJSON(t))
	reason := "ran the read-only host verification by hand; all checks passed"
	steps[0].ApprovalReason = &reason

	md := BuildTestingSummary(steps, rounds)

	if !strings.Contains(md, "Approved with exception: "+reason) {
		t.Fatalf("Testing section must publish the approval reason:\n%s", md)
	}
	if !strings.Contains(md, "recorded before this approval") {
		t.Fatalf("scenario results must be marked as recorded before the approval:\n%s", md)
	}
}

func TestBuildTestingSummary_NoApprovalReasonRendersAsBefore(t *testing.T) {
	t.Parallel()
	steps, rounds := testStepWithFindings(t, untestedFindingsJSON(t))
	blank := "  "

	without := BuildTestingSummary(steps, rounds)
	steps[0].ApprovalReason = &blank
	blankMD := BuildTestingSummary(steps, rounds)

	for _, md := range []string{without, blankMD} {
		if strings.Contains(md, "Approved with") || strings.Contains(md, "recorded before this approval") {
			t.Fatalf("a run without a reason must render unchanged:\n%s", md)
		}
	}
	if without != blankMD {
		t.Fatalf("blank reason must equal no reason:\n%s\n---\n%s", without, blankMD)
	}
}

func TestBuildTestingSummaryForPR_SeparatesApprovalReasonFromSummaryParagraph(t *testing.T) {
	t.Parallel()
	compactJSON, err := json.Marshal(types.Findings{Tested: []string{"`go test ./...`"}})
	if err != nil {
		t.Fatal(err)
	}
	reason := "ran the read-only host verification by hand; all checks passed"
	for _, tc := range []struct {
		name         string
		findingsJSON string
		want         string
	}{
		{
			name:         "agent summary",
			findingsJSON: untestedFindingsJSON(t),
			want:         "- The summary and scenario results below were recorded before this approval.\n\ndrove the checkout scenarios against a running app",
		},
		{
			name:         "compact tested summary",
			findingsJSON: string(compactJSON),
			want:         ": " + reason + "\n\nCompleted 1 recorded test check.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			steps, rounds := testStepWithFindings(t, tc.findingsJSON)
			steps[0].ApprovalReason = &reason

			md := BuildTestingSummaryForPRWithProvider(steps, rounds, "https://github.com/example/widgets.git", "abc123", t.TempDir(), "", nil, scm.ProviderGitHub)

			if !strings.Contains(md, "- Approved with") || !strings.Contains(md, tc.want) {
				t.Fatalf("summary paragraph must start after a blank line that ends the approval bullets, want %q in:\n%s", tc.want, md)
			}
		})
	}
}
