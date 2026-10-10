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
	return config.Merge(&config.GlobalConfig{Review: config.OperatorReviewRaw{RequireErrorsFixed: &on}}, &config.RepoConfig{})
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

const errorAndWarningFindings = `{"summary":"one error, one warning","findings":[{"id":"1","severity":"error","file":"a.go","description":"The token is logged in clear text"},{"id":"2","severity":"warning","file":"b.go","description":"The retry loop has no backoff"}],"reviewed_paths":["a.go","b.go"]}`

func requireErrorsFixedAndFilingConfig() *config.Config {
	on := true
	return config.Merge(&config.GlobalConfig{
		Review:               config.OperatorReviewRaw{RequireErrorsFixed: &on},
		FileDeferredFindings: config.FileDeferredFindingsRaw{Review: config.FileDeferredFindingsGitHubIssue},
	}, &config.RepoConfig{})
}

// Filing an error as an issue is not fixing it. With both options on, a gate
// that holds an error refuses approve and skip, and a refused answer files
// nothing: neither the error nor the warning beside it leaves the gate.
func TestRequireErrorsFixed_FilingDoesNotCountAsFixed(t *testing.T) {
	step := &filingStep{mockStep: newApprovalStep(types.StepReview, errorAndWarningFindings)}
	database, p, run, repo := setupTest(t)
	exec := NewExecutor(database, p, requireErrorsFixedAndFilingConfig(), nil, []Step{step}, nil)

	done := make(chan error, 1)
	go func() { done <- exec.Execute(context.Background(), run, repo, t.TempDir()) }()
	waitForStepStatus(t, database, run.ID, step.Name(), types.StepStatusAwaitingApproval)

	for _, action := range []types.ApprovalAction{types.ActionApprove, types.ActionSkip} {
		err := exec.Respond(step.Name(), action, nil)
		if err == nil || !strings.Contains(err.Error(), "error finding 1") {
			t.Fatalf("Respond(%s) error = %v, want a refusal naming error finding 1", action, err)
		}
		if calls := step.filings(); len(calls) != 0 {
			t.Fatalf("filings after refused %s = %+v, want none", action, calls)
		}
	}
	if err := exec.Respond(step.Name(), types.ActionAbort, nil); err != nil {
		t.Fatalf("Respond(abort) error = %v, want abort allowed", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not settle after the abort")
	}
	if calls := step.filings(); len(calls) != 0 {
		t.Fatalf("filings after abort = %+v, want none", calls)
	}
}

// The requirement leaves deferred filing of warning and info findings exactly
// as it is without the requirement.
func TestRequireErrorsFixed_WarningFilingIsUnchanged(t *testing.T) {
	for _, action := range []types.ApprovalAction{types.ActionApprove, types.ActionSkip} {
		with := runFilingGateCase(t, requireErrorsFixedAndFilingConfig(), warningFindings, action)
		without := runFilingGateCase(t, filingOnConfig(), warningFindings, action)
		if len(with) != 1 || len(without) != 1 {
			t.Fatalf("%s filings = %d with the requirement, %d without; want 1 and 1", action, len(with), len(without))
		}
		if with[0].findings != without[0].findings || with[0].answers["1"] != without[0].answers["1"] {
			t.Fatalf("%s filing with the requirement = %+v, without = %+v; want identical", action, with[0], without[0])
		}
	}
}

// Info findings never park, so clean completion files them with or without
// the requirement.
func TestRequireErrorsFixed_InfoFilingOnCleanCompletionIsUnchanged(t *testing.T) {
	step := &filingStep{mockStep: &mockStep{name: types.StepReview, outcome: &StepOutcome{Findings: infoFindings, ReviewedPaths: []string{"a.go"}}}}
	database, p, run, repo := setupTest(t)
	exec := NewExecutor(database, p, requireErrorsFixedAndFilingConfig(), nil, []Step{step}, nil)
	if err := exec.Execute(context.Background(), run, repo, t.TempDir()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	calls := step.filings()
	if len(calls) != 1 || calls[0].answers["1"] != DeferralCompleted {
		t.Fatalf("filings = %+v, want one completed filing", calls)
	}
}
