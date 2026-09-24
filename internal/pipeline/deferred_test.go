package pipeline

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

// filingStep is a mockStep that also implements DeferredFindingsFiler, so these
// tests can watch exactly when the executor asks a step to file its unfixed
// findings. No forge and no database row is involved.
type filingStep struct {
	*mockStep
	mu     sync.Mutex
	calls  []filingCall
	onFile func()
	panics bool
}

type filingCall struct {
	findings string
	answers  map[string]string
}

func (f *filingStep) FileDeferredFindings(_ *StepContext, findingsJSON string, answers map[string]string) {
	f.mu.Lock()
	f.calls = append(f.calls, filingCall{findings: findingsJSON, answers: answers})
	f.mu.Unlock()
	if f.onFile != nil {
		f.onFile()
	}
	if f.panics {
		panic("filer exploded")
	}
}

func (f *filingStep) filings() []filingCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]filingCall, len(f.calls))
	copy(out, f.calls)
	return out
}

const warningFindings = `{"summary":"one warning","findings":[{"id":"1","severity":"warning","file":"a.go","description":"The retry loop has no backoff"}],"reviewed_paths":["a.go"]}`
const infoFindings = `{"summary":"one note","findings":[{"id":"1","severity":"info","action":"no-op","file":"a.go","description":"This name reads oddly"}],"reviewed_paths":["a.go"]}`

func filingOnConfig() *config.Config {
	return config.Merge(&config.GlobalConfig{
		FileDeferredFindings: config.FileDeferredFindingsRaw{Review: config.FileDeferredFindingsGitHubIssue},
	}, &config.RepoConfig{})
}

// runFilingGateCase drives a one-step run to its approval gate, answers it with
// action, and returns what the step was asked to file.
func runFilingGateCase(t *testing.T, cfg *config.Config, findings string, action types.ApprovalAction) []filingCall {
	t.Helper()
	step := &filingStep{mockStep: newApprovalStep(types.StepReview, findings)}
	database, p, run, repo := setupTest(t)
	exec := NewExecutor(database, p, cfg, nil, []Step{step}, nil)

	done := make(chan error, 1)
	go func() { done <- exec.Execute(context.Background(), run, repo, t.TempDir()) }()

	waitForStepStatus(t, database, run.ID, step.Name(), types.StepStatusAwaitingApproval)
	if err := exec.Respond(step.Name(), action, nil); err != nil {
		t.Fatalf("Respond(%s) error = %v", action, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not settle after the gate answer")
	}
	return step.filings()
}

func TestFileDeferredFindings_ApprovedGateFilesTheFindings(t *testing.T) {
	calls := runFilingGateCase(t, filingOnConfig(), warningFindings, types.ActionApprove)
	if len(calls) != 1 {
		t.Fatalf("filings = %d, want 1", len(calls))
	}
	// The executor hands over its own re-serialized outstanding set, so compare
	// the findings themselves rather than the bytes.
	filed, err := types.ParseFindingsJSON(calls[0].findings)
	if err != nil {
		t.Fatalf("parse filed payload: %v", err)
	}
	if len(filed.Items) != 1 || filed.Items[0].Description != "The retry loop has no backoff" {
		t.Fatalf("filed findings = %+v, want the gate's own finding", filed.Items)
	}
	if calls[0].answers["1"] != DeferralApproved {
		t.Fatalf("answer = %q, want %q", calls[0].answers["1"], DeferralApproved)
	}
}

func TestFileDeferredFindings_SkippedGateFilesTheFindings(t *testing.T) {
	calls := runFilingGateCase(t, filingOnConfig(), warningFindings, types.ActionSkip)
	if len(calls) != 1 || calls[0].answers["1"] != DeferralSkipped {
		t.Fatalf("filings = %+v, want one skipped answer", calls)
	}
}

func TestFileDeferredFindings_ConfigOffMeansNoCallAtAll(t *testing.T) {
	off := config.Merge(&config.GlobalConfig{}, &config.RepoConfig{})
	if calls := runFilingGateCase(t, off, warningFindings, types.ActionApprove); len(calls) != 0 {
		t.Fatalf("filings = %+v, want none with filing off", calls)
	}
	if calls := runFilingGateCase(t, nil, warningFindings, types.ActionApprove); len(calls) != 0 {
		t.Fatalf("filings = %+v, want none with no config at all", calls)
	}
}

// Info findings never park at a gate, so a clean completion is the only place
// they can be filed.
func TestFileDeferredFindings_CleanCompletionFilesUnblockingFindings(t *testing.T) {
	step := &filingStep{mockStep: &mockStep{name: types.StepReview, outcome: &StepOutcome{Findings: infoFindings, ReviewedPaths: []string{"a.go"}}}}
	database, p, run, repo := setupTest(t)
	exec := NewExecutor(database, p, filingOnConfig(), nil, []Step{step}, nil)

	if err := exec.Execute(context.Background(), run, repo, t.TempDir()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	calls := step.filings()
	if len(calls) != 1 || calls[0].answers["1"] != DeferralCompleted {
		t.Fatalf("filings = %+v, want one completed answer", calls)
	}
}

func TestFileDeferredFindings_CleanStepWithNoFindingsFilesNothing(t *testing.T) {
	step := &filingStep{mockStep: newPassStep(types.StepReview)}
	database, p, run, repo := setupTest(t)
	exec := NewExecutor(database, p, filingOnConfig(), nil, []Step{step}, nil)

	if err := exec.Execute(context.Background(), run, repo, t.TempDir()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls := step.filings(); len(calls) != 0 {
		t.Fatalf("filings = %+v, want none when the step reported nothing", calls)
	}
}

func TestFileDeferredFindings_AbortedGateFilesNothing(t *testing.T) {
	step := &filingStep{mockStep: newApprovalStep(types.StepReview, warningFindings)}
	database, p, run, repo := setupTest(t)
	exec := NewExecutor(database, p, filingOnConfig(), nil, []Step{step}, nil)

	done := make(chan error, 1)
	go func() { done <- exec.Execute(context.Background(), run, repo, t.TempDir()) }()
	waitForStepStatus(t, database, run.ID, step.Name(), types.StepStatusAwaitingApproval)
	if err := exec.Respond(step.Name(), types.ActionAbort, nil); err != nil {
		t.Fatalf("Respond(abort) error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not settle after the abort")
	}
	// An abort is not a delivery: the operator stopped the run, so nothing
	// shipped unfixed and nothing is filed.
	if calls := step.filings(); len(calls) != 0 {
		t.Fatalf("filings = %+v, want none after an abort", calls)
	}
}

// A step that does not implement the interface must be completely unaffected,
// exactly like ApprovalOverrideVerifier.
func TestFileDeferredFindings_StepWithoutTheInterfaceStillCompletes(t *testing.T) {
	step := newApprovalStep(types.StepLint, warningFindings)
	database, p, run, repo := setupTest(t)
	exec := NewExecutor(database, p, filingOnConfig(), nil, []Step{step}, nil)

	done := make(chan error, 1)
	go func() { done <- exec.Execute(context.Background(), run, repo, t.TempDir()) }()
	waitForStepStatus(t, database, run.ID, step.Name(), types.StepStatusAwaitingApproval)
	if err := exec.Respond(step.Name(), types.ActionApprove, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not complete")
	}
}

func TestFileDeferredFindings_FilerPanicDoesNotFailTheRun(t *testing.T) {
	step := &filingStep{mockStep: newApprovalStep(types.StepReview, warningFindings), panics: true}
	database, p, run, repo := setupTest(t)
	exec := NewExecutor(database, p, filingOnConfig(), nil, []Step{step}, nil)

	done := make(chan error, 1)
	go func() { done <- exec.Execute(context.Background(), run, repo, t.TempDir()) }()
	waitForStepStatus(t, database, run.ID, step.Name(), types.StepStatusAwaitingApproval)
	if err := exec.Respond(step.Name(), types.ActionApprove, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Execute() error = %v, want the panic contained", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not complete after the filer panicked")
	}
	got, err := database.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != types.RunCompleted {
		t.Fatalf("run status = %s, want %s", got.Status, types.RunCompleted)
	}
}

func TestDeferralAnswers_SelectedFindingIsUnfixedNotAccepted(t *testing.T) {
	findings := `{"findings":[{"id":"1","severity":"warning","file":"a.go","description":"first"},{"id":"2","severity":"info","file":"b.go","description":"second"}]}`
	answers := deferralAnswers(findings, DeferralApproved, []string{"1"})
	if answers["1"] != DeferralUnfixed {
		t.Fatalf("selected answer = %q, want %q: nobody accepted that defect", answers["1"], DeferralUnfixed)
	}
	if answers["2"] != DeferralApproved {
		t.Fatalf("unselected answer = %q, want %q", answers["2"], DeferralApproved)
	}
}

func TestDeferralAnswers_UnparseablePayloadAnswersNothing(t *testing.T) {
	if answers := deferralAnswers("not json", DeferralApproved, nil); answers != nil {
		t.Fatalf("answers = %+v, want nil", answers)
	}
}
