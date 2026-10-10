package pipeline

import (
	"log/slog"

	"github.com/kunchenguid/no-mistakes/internal/types"
)

// DeferredFindingsFiler is an optional interface for a step whose unfixed
// findings must be filed somewhere durable when the step completes anyway.
//
// It follows ApprovalOverrideVerifier's shape for the same reason: the gate
// lives in the executor, but everything a step needs to talk to its forge lives
// in the steps package, so the executor asks the step to do the work. Today only
// the review step implements it; every other step is unaffected.
//
// Implementations must never return an error and never block: filing a deferred
// finding is evidence, not a gate. A failure is reported in the run log and the
// local record.
type DeferredFindingsFiler interface {
	// FileDeferredFindings files the findings in findingsJSON. answers maps a
	// finding ID to how it was deferred; an ID with no entry was approved.
	FileDeferredFindings(sctx *StepContext, findingsJSON string, answers map[string]string)
}

// Deferral reasons handed to a filer. They mirror the gate answers a person can
// give, and are plain strings so the steps package owns their wording.
const (
	DeferralApproved  = "approved"
	DeferralSkipped   = "skipped"
	DeferralCompleted = "completed"
	DeferralUnfixed   = "unfixed"
)

// fileDeferredFindings hands a completing step's unfixed findings to its filer.
//
// It is called at every place a step completes with findings still outstanding:
// an approved gate, a skipped gate, and a clean completion that leaves
// non-blocking findings (info findings never park, so this is their only path).
// A step that does not implement the interface, a run whose config leaves filing
// off, and an empty finding set all do nothing at all.
func (e *Executor) fileDeferredFindings(step Step, sctx *StepContext, findingsJSON, deferral string, selectedIDs []string) {
	if e == nil || step == nil || sctx == nil || findingsJSON == "" {
		return
	}
	if e.config == nil || !e.config.FilesDeferredReviewFindings() {
		return
	}
	filer, ok := step.(DeferredFindingsFiler)
	if !ok {
		return
	}
	answers := deferralAnswers(findingsJSON, deferral, selectedIDs)
	defer func() {
		// A filer is best-effort evidence collection at the very end of a step.
		// A panic there must not take the run down with it.
		if recovered := recover(); recovered != nil {
			slog.Warn("filing deferred findings panicked", "step", step.Name(), "error", recovered)
		}
	}()
	filer.FileDeferredFindings(sctx, findingsJSON, answers)
}

// deferralAnswers labels each finding with how it was deferred. A finding the
// operator selected for a fix round that never verified it keeps its own answer:
// nobody accepted that defect, a fix simply did not land.
func deferralAnswers(findingsJSON, deferral string, selectedIDs []string) map[string]string {
	findings, err := types.ParseFindingsJSON(findingsJSON)
	if err != nil {
		return nil
	}
	selected := make(map[string]bool, len(selectedIDs))
	for _, id := range selectedIDs {
		selected[id] = true
	}
	answers := make(map[string]string, len(findings.Items))
	for _, item := range findings.Items {
		switch {
		case selected[item.ID]:
			answers[item.ID] = DeferralUnfixed
		default:
			answers[item.ID] = deferral
		}
	}
	return answers
}
