package steps

import (
	"strings"
	"testing"

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
