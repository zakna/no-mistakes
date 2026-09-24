package pipeline

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

const errorFindings = `{"summary":"one error","findings":[{"id":"1","severity":"error","file":"a.go","description":"The token is logged in clear text"}]}`
const twoErrorFindings = `{"summary":"two errors","findings":[{"id":"1","severity":"error","file":"a.go","description":"first"},{"id":"2","severity":"error","file":"b.go","description":"second"}]}`

func requireErrorsFixedConfig(on bool) *config.Config {
	return config.Merge(&config.GlobalConfig{Review: config.GlobalReviewRaw{RequireErrorsFixed: &on}}, &config.RepoConfig{})
}

// runGuardedGate parks a review gate, answers it with action, and returns the
// error Respond gave.
func runGuardedGate(t *testing.T, cfg *config.Config, findings string, action types.ApprovalAction) error {
	t.Helper()
	step := newApprovalStep(types.StepReview, findings)
	database, p, run, repo := setupTest(t)
	exec := NewExecutor(database, p, cfg, nil, []Step{step}, nil)

	done := make(chan error, 1)
	go func() { done <- exec.Execute(context.Background(), run, repo, t.TempDir()) }()
	waitForStepStatus(t, database, run.ID, step.Name(), types.StepStatusAwaitingApproval)

	respondErr := exec.Respond(step.Name(), action, nil)

	// Release the run whatever happened, so the test never leaks the executor.
	if respondErr != nil {
		if err := exec.Respond(step.Name(), types.ActionAbort, nil); err != nil {
			t.Fatalf("abort after refusal: %v", err)
		}
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not settle")
	}
	return respondErr
}

func TestRequireErrorsFixed_RefusesApproveAndSkipAtTheReviewGate(t *testing.T) {
	for _, action := range []types.ApprovalAction{types.ActionApprove, types.ActionSkip} {
		err := runGuardedGate(t, requireErrorsFixedConfig(true), errorFindings, action)
		if err == nil {
			t.Fatalf("Respond(%s) error = nil, want a refusal", action)
		}
		for _, want := range []string{"require_errors_fixed", "error finding 1", "fix"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("Respond(%s) error = %q, want it to name %q", action, err, want)
			}
		}
	}
}

// Off by default: the gate behaves exactly as it did before this option.
func TestRequireErrorsFixed_OffAcceptsApproveAndSkip(t *testing.T) {
	for _, action := range []types.ApprovalAction{types.ActionApprove, types.ActionSkip} {
		if err := runGuardedGate(t, requireErrorsFixedConfig(false), errorFindings, action); err != nil {
			t.Fatalf("Respond(%s) error = %v, want the answer accepted", action, err)
		}
		if err := runGuardedGate(t, nil, errorFindings, action); err != nil {
			t.Fatalf("Respond(%s) with no config = %v, want the answer accepted", action, err)
		}
	}
}

// The requirement is about errors only. A warning gate still accepts the
// operator's answer, which is what the blocking-only filter and the deferred
// issue filing depend on.
func TestRequireErrorsFixed_WarningGateIsStillAnswerable(t *testing.T) {
	if err := runGuardedGate(t, requireErrorsFixedConfig(true), warningFindings, types.ActionApprove); err != nil {
		t.Fatalf("Respond(approve) error = %v, want a warning gate to stay answerable", err)
	}
}

func TestRequireErrorsFixed_AbortIsAlwaysAllowed(t *testing.T) {
	step := newApprovalStep(types.StepReview, errorFindings)
	database, p, run, repo := setupTest(t)
	exec := NewExecutor(database, p, requireErrorsFixedConfig(true), nil, []Step{step}, nil)

	done := make(chan error, 1)
	go func() { done <- exec.Execute(context.Background(), run, repo, t.TempDir()) }()
	waitForStepStatus(t, database, run.ID, step.Name(), types.StepStatusAwaitingApproval)
	if err := exec.Respond(step.Name(), types.ActionAbort, nil); err != nil {
		t.Fatalf("Respond(abort) error = %v, want abort always allowed", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not settle after the abort")
	}
}

func TestApprovalRefusal_GuardScopeAndWording(t *testing.T) {
	// Another step's gate is untouched: the requirement is about review findings.
	if refusal := approvalRefusal(types.StepCI, errorFindings, true); refusal.reason != "" {
		t.Fatalf("CI refusal = %q, want none", refusal.reason)
	}
	// Every error finding is named, so the answer says what to repair.
	refusal := approvalRefusal(types.StepReview, twoErrorFindings, true)
	if !strings.Contains(refusal.reason, "error findings 1, 2") {
		t.Fatalf("reason = %q, want both findings named", refusal.reason)
	}
	if !refusal.blocksSkip {
		t.Fatal("the requirement must refuse skip as well: a skip ships the defect too")
	}
	// The older refusals keep their exact scope: approve only.
	protected := approvalRefusal(types.StepPush, `{"findings":[{"id":"protected-path-refusal","severity":"error"}]}`, false)
	if protected.reason == "" || protected.blocksSkip {
		t.Fatalf("protected-path refusal = %+v, want approve-only", protected)
	}
	if protected.refuses(types.ActionSkip) {
		t.Fatal("a protected-path refusal must keep accepting skip")
	}
	if !protected.refuses(types.ActionApprove) {
		t.Fatal("a protected-path refusal must still refuse approve")
	}
	// An unreadable payload must not pass as "no errors".
	if refusal := approvalRefusal(types.StepReview, "not json", true); refusal.reason == "" {
		t.Fatal("unreadable findings must fail closed under the requirement")
	}
	// No findings, no refusal.
	if refusal := approvalRefusal(types.StepReview, "", true); refusal.reason != "" {
		t.Fatalf("empty findings refusal = %q, want none", refusal.reason)
	}
	if (gateRefusal{}).refuses(types.ActionApprove) {
		t.Fatal("an empty refusal must refuse nothing")
	}
}
