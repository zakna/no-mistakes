package pipeline

import (
	"context"
	"slices"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

// mixedSeverityReviewFindings has one auto-fix finding at each severity, each
// in its own file so carry-forward coverage can clear them independently.
const mixedSeverityReviewFindings = `{"findings":[` +
	`{"id":"review-1","severity":"error","file":"a.go","description":"nil dereference","action":"auto-fix"},` +
	`{"id":"review-2","severity":"warning","file":"b.go","description":"missing error wrap","action":"auto-fix"},` +
	`{"id":"review-3","severity":"info","file":"c.go","description":"could simplify","action":"auto-fix"}` +
	`],"summary":"3 findings"}`

var mixedSeverityReviewPaths = []string{"a.go", "b.go", "c.go"}

func findingIDsFromJSON(t *testing.T, raw string) []string {
	t.Helper()
	if raw == "" {
		return nil
	}
	parsed, err := types.ParseFindingsJSON(raw)
	if err != nil {
		t.Fatalf("parse findings: %v", err)
	}
	ids := make([]string, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

// Without the option, the automatic loop fixes every auto-fix finding,
// including info: the behavior before auto_fix_min_severity existed.
func TestExecutor_AutoFixMinSeverityUnsetFixesEverySeverity(t *testing.T) {
	database, p, run, repo := setupTest(t)
	cfg := &config.Config{AutoFix: config.AutoFix{Review: 3}}

	var selected []string
	calls := 0
	step := &adaptiveCallStep{name: types.StepReview, fn: func(sctx *StepContext) (*StepOutcome, error) {
		calls++
		if calls == 1 {
			return &StepOutcome{NeedsApproval: true, AutoFixable: true, Findings: mixedSeverityReviewFindings}, nil
		}
		selected = findingIDsFromJSON(t, sctx.PreviousFindings)
		return &StepOutcome{ReviewedPaths: mixedSeverityReviewPaths, ReviewablePaths: mixedSeverityReviewPaths}, nil
	}}

	exec := NewExecutor(database, p, cfg, nil, []Step{step}, nil)
	if err := exec.Execute(context.Background(), run, repo, t.TempDir()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if want := []string{"review-1", "review-2", "review-3"}; !slices.Equal(selected, want) {
		t.Fatalf("auto-fixed findings = %v, want %v", selected, want)
	}
}

// With "error", only blocking findings are fixed automatically. The warning
// is handed to the fixer as deferred, is never auto-fixed even with budget
// left, and parks the step for a decision exactly as it does today.
func TestExecutor_AutoFixMinSeverityErrorFixesOnlyErrorsAndWarningsPark(t *testing.T) {
	database, p, run, repo := setupTest(t)
	cfg := &config.Config{
		AutoFix:            config.AutoFix{Review: 3},
		AutoFixMinSeverity: config.AutoFixMinSeverity{Review: types.FindingSeverityError},
	}

	var selected, deferred []string
	calls := 0
	step := &adaptiveCallStep{name: types.StepReview, fn: func(sctx *StepContext) (*StepOutcome, error) {
		calls++
		if calls == 1 {
			return &StepOutcome{NeedsApproval: true, AutoFixable: true, Findings: mixedSeverityReviewFindings}, nil
		}
		selected = findingIDsFromJSON(t, sctx.PreviousFindings)
		deferred = findingIDsFromJSON(t, sctx.DeferredFindings)
		// The rereview confirms the error is gone and re-reports the rest.
		return &StepOutcome{
			NeedsApproval:   true,
			AutoFixable:     true,
			ReviewedPaths:   mixedSeverityReviewPaths,
			ReviewablePaths: mixedSeverityReviewPaths,
			Findings: `{"findings":[` +
				`{"id":"review-2","severity":"warning","file":"b.go","description":"missing error wrap","action":"auto-fix"},` +
				`{"id":"review-3","severity":"info","file":"c.go","description":"could simplify","action":"auto-fix"}` +
				`],"summary":"2 findings"}`,
		}, nil
	}}

	exec := NewExecutor(database, p, cfg, nil, []Step{step}, nil)
	done, _ := startExecutor(t, exec, run, repo, t.TempDir())
	waitForStepStatus(t, database, run.ID, types.StepReview, types.StepStatusFixReview)

	if calls != 2 {
		t.Fatalf("step calls = %d, want 2 (initial + one auto-fix round): a warning must not start another round", calls)
	}
	if want := []string{"review-1"}; !slices.Equal(selected, want) {
		t.Fatalf("auto-fixed findings = %v, want %v", selected, want)
	}
	if want := []string{"review-2", "review-3"}; !slices.Equal(deferred, want) {
		t.Fatalf("deferred findings = %v, want %v", deferred, want)
	}
	steps, err := database.GetStepsByRun(run.ID)
	if err != nil || len(steps) != 1 || steps[0].FindingsJSON == nil {
		t.Fatalf("parked step = %+v, %v", steps, err)
	}
	parked, err := types.ParseFindingsJSON(*steps[0].FindingsJSON)
	if err != nil {
		t.Fatalf("parse parked findings: %v", err)
	}
	var severities []string
	for _, item := range parked.Items {
		severities = append(severities, item.Severity)
	}
	if !slices.Contains(severities, types.FindingSeverityWarning) || slices.Contains(severities, types.FindingSeverityError) {
		t.Fatalf("parked severities = %v, want the warning outstanding and the fixed error cleared", severities)
	}

	if err := exec.Respond(types.StepReview, types.ActionApprove, nil); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitExecutorDone(t, done)
}

// A review with only lower-severity findings under "error" starts no fix
// round at all and parks at the ordinary approval gate.
func TestExecutor_AutoFixMinSeverityErrorWithOnlyWarningsParksWithoutFixing(t *testing.T) {
	database, p, run, repo := setupTest(t)
	cfg := &config.Config{
		AutoFix:            config.AutoFix{Review: 3},
		AutoFixMinSeverity: config.AutoFixMinSeverity{Review: types.FindingSeverityError},
	}

	calls := 0
	step := &adaptiveCallStep{name: types.StepReview, fn: func(sctx *StepContext) (*StepOutcome, error) {
		calls++
		if sctx.Fixing {
			t.Error("a warning-only review must not start an automatic fix round")
		}
		return &StepOutcome{
			NeedsApproval: true,
			AutoFixable:   true,
			Findings:      `{"findings":[{"id":"review-1","severity":"warning","file":"b.go","description":"missing error wrap","action":"auto-fix"}],"summary":"1 finding"}`,
		}, nil
	}}

	exec := NewExecutor(database, p, cfg, nil, []Step{step}, nil)
	done, _ := startExecutor(t, exec, run, repo, t.TempDir())
	waitForStepStatus(t, database, run.ID, types.StepReview, types.StepStatusAwaitingApproval)

	if calls != 1 {
		t.Fatalf("step calls = %d, want 1", calls)
	}
	if err := exec.Respond(types.StepReview, types.ActionApprove, nil); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitExecutorDone(t, done)
}

// With "warning", errors and warnings are fixed automatically and info is
// left alone; an unselected info finding does not park.
func TestExecutor_AutoFixMinSeverityWarningFixesErrorsAndWarnings(t *testing.T) {
	database, p, run, repo := setupTest(t)
	cfg := &config.Config{
		AutoFix:            config.AutoFix{Review: 3},
		AutoFixMinSeverity: config.AutoFixMinSeverity{Review: types.FindingSeverityWarning},
	}

	var selected []string
	calls := 0
	step := &adaptiveCallStep{name: types.StepReview, fn: func(sctx *StepContext) (*StepOutcome, error) {
		calls++
		if calls == 1 {
			return &StepOutcome{NeedsApproval: true, AutoFixable: true, Findings: mixedSeverityReviewFindings}, nil
		}
		selected = findingIDsFromJSON(t, sctx.PreviousFindings)
		return &StepOutcome{
			AutoFixable:     true,
			ReviewedPaths:   mixedSeverityReviewPaths,
			ReviewablePaths: mixedSeverityReviewPaths,
			Findings:        `{"findings":[{"id":"review-3","severity":"info","file":"c.go","description":"could simplify","action":"auto-fix"}],"summary":"1 finding"}`,
		}, nil
	}}

	exec := NewExecutor(database, p, cfg, nil, []Step{step}, nil)
	if err := exec.Execute(context.Background(), run, repo, t.TempDir()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("step calls = %d, want 2: the remaining info finding must not start another round", calls)
	}
	if want := []string{"review-1", "review-2"}; !slices.Equal(selected, want) {
		t.Fatalf("auto-fixed findings = %v, want %v", selected, want)
	}
	updated, err := database.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != types.RunCompleted {
		t.Fatalf("run status = %q, want %q", updated.Status, types.RunCompleted)
	}
}

// The round budget is unchanged by the filter: an error the fixer cannot
// clear parks at fix_review once auto_fix.review rounds are spent.
func TestExecutor_AutoFixMinSeverityKeepsTheRoundBudget(t *testing.T) {
	database, p, run, repo := setupTest(t)
	cfg := &config.Config{
		AutoFix:            config.AutoFix{Review: 1},
		AutoFixMinSeverity: config.AutoFixMinSeverity{Review: types.FindingSeverityError},
	}

	calls := 0
	step := &adaptiveCallStep{name: types.StepReview, fn: func(sctx *StepContext) (*StepOutcome, error) {
		calls++
		return &StepOutcome{
			NeedsApproval:   true,
			AutoFixable:     true,
			ReviewedPaths:   []string{"a.go"},
			ReviewablePaths: []string{"a.go"},
			Findings:        `{"findings":[{"id":"review-1","severity":"error","file":"a.go","description":"nil dereference","action":"auto-fix"}],"summary":"1 finding"}`,
		}, nil
	}}

	exec := NewExecutor(database, p, cfg, nil, []Step{step}, nil)
	done, _ := startExecutor(t, exec, run, repo, t.TempDir())
	waitForStepStatus(t, database, run.ID, types.StepReview, types.StepStatusFixReview)

	if calls != 2 {
		t.Fatalf("step calls = %d, want 2 (initial + the one budgeted auto-fix round)", calls)
	}
	if err := exec.Respond(types.StepReview, types.ActionApprove, nil); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitExecutorDone(t, done)
}
