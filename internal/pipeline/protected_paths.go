package pipeline

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/types"
)

const protectedPathFindingID = "protected-path-refusal"

// ReviewQuestionsUnreadableFindingID is the single synthetic finding the review
// step emits when the reviewer's question history could not be read in full.
// It is one fixed finding rather than a class of them, so it is keyed by ID
// exactly as the protected-path refusal above is, and deliberately carries no
// review-question category: that category summons the answer-first gate help,
// and an answer is precisely what the daemon refuses while the history is
// unreadable.
const ReviewQuestionsUnreadableFindingID = "review-questions-unreadable"

// IsReservedFindingID reports whether an ID carries pipeline-owned semantics.
func IsReservedFindingID(id string) bool {
	switch id {
	case protectedPathFindingID, types.FindingIDTestAgentTimeout, types.FindingIDTestAgentUnvalidatedWork, ReviewQuestionsUnreadableFindingID:
		return true
	default:
		return false
	}
}

// HasProtectedPathRefusal identifies gates that require an explicit response.
func HasProtectedPathRefusal(findingsJSON string) bool {
	return hasFindingID(findingsJSON, protectedPathFindingID)
}

// HasUnvalidatedWorkRefusal identifies a Test budget-cut gate whose worktree
// holds work no Test turn validated. Approve is refused there because the
// steps after Test would commit and publish that work; fix validates it.
func HasUnvalidatedWorkRefusal(findingsJSON string) bool {
	return hasFindingID(findingsJSON, types.FindingIDTestAgentUnvalidatedWork)
}

func hasFindingID(findingsJSON, id string) bool {
	findings, _ := types.ParseFindingsJSON(findingsJSON)
	for _, finding := range findings.Items {
		if finding.ID == id {
			return true
		}
	}
	return false
}

// HasUnansweredReviewQuestion identifies gates an automatic resolver must leave
// alone because only an answer can settle them.
//
// It is the JSON-string sibling of types.HasReviewQuestion, and it exists so
// that EVERY auto-resolve path reads one predicate instead of repeating the
// parse-then-check pair. There are two such paths - `axi`'s --yes and the TUI's
// yolo mode - and the carve-out first landed on only one of them, which left the
// stated property ("--yes leaves an open question awaiting an explicit answer")
// true of `axi` and false of the TUI. A shared predicate is what makes a third
// path inherit the rule rather than reintroduce the bug.
//
// It deliberately does NOT constrain a human's explicit approve or fix: the
// design permits resolving a gate over an open question knowingly. Only the
// automatic paths stand aside.
func HasUnansweredReviewQuestion(findingsJSON string) bool {
	findings, err := types.ParseFindingsJSON(findingsJSON)
	if err != nil {
		return false
	}
	return types.HasReviewQuestion(findings)
}

// HasUnreadableReviewQuestionHistory identifies gates an automatic resolver
// must leave alone because the reviewer's question history could not be read in
// full, so the decision this gate asks for cannot be made from the findings.
//
// Without it the marker is an ordinary actionable ask-user finding: gateResolution
// selects its id and returns ActionFix, the fixer is handed "decide this gate
// yourself" as work it cannot do, the rereview re-emits the identical marker,
// and the resulting fix_review gate is approved as already-fixed - so a
// possibly-dropped major question reaches nobody and the park costs a fix round
// instead of buying a human decision. Same carve-out shape, and the same one
// predicate for every automatic path, as HasUnansweredReviewQuestion above; a
// human's own approve, fix or skip stays allowed.
func HasUnreadableReviewQuestionHistory(findingsJSON string) bool {
	findings, err := types.ParseFindingsJSON(findingsJSON)
	if err != nil {
		return false
	}
	for _, finding := range findings.Items {
		if finding.ID == ReviewQuestionsUnreadableFindingID {
			return true
		}
	}
	return false
}

// gateRefusal records why a gate rejects an answer, and which answers it
// rejects.
type gateRefusal struct {
	// reason is empty when the gate accepts every answer.
	reason string
	// blocksSkip refuses skip as well as approve. A skip ships the finding
	// exactly like an approval does, so a requirement that a defect must be
	// fixed has to refuse both; the older refusals below only guard what an
	// approval itself would publish.
	blocksSkip bool
}

// refuses reports whether this refusal rejects action.
func (g gateRefusal) refuses(action types.ApprovalAction) bool {
	if g.reason == "" {
		return false
	}
	return action == types.ActionApprove || (g.blocksSkip && action == types.ActionSkip)
}

// approvalRefusal reports why an answer is rejected at a gate.
//
// requireErrorsFixed is the operator's trusted review.require_errors_fixed
// setting. It is off by default, which leaves every gate exactly as it was.
func approvalRefusal(step types.StepName, findingsJSON string, requireErrorsFixed bool) gateRefusal {
	switch {
	case HasProtectedPathRefusal(findingsJSON):
		return gateRefusal{reason: fmt.Sprintf("cannot approve a protected-path refusal: resolve the reported edit, then use fix to retry %s; approval would skip unfinished work", step)}
	case HasUnvalidatedWorkRefusal(findingsJSON):
		return gateRefusal{reason: fmt.Sprintf("cannot approve %s: the run worktree holds work no Test turn validated and approval would publish it; inspect it as the findings describe, then use fix to validate it, or abort", step)}
	case requireErrorsFixed && step == types.StepReview && hasErrorFindingsJSON(findingsJSON):
		return gateRefusal{
			reason: fmt.Sprintf("cannot approve or skip %s: review.require_errors_fixed is on and %s; use fix to repair them, or abort", step,
				errorFindingsSummary(findingsJSON)),
			blocksSkip: true,
		}
	}
	return gateRefusal{}
}

// hasErrorFindingsJSON reports whether any outstanding finding is an error.
// Unreadable findings answer true: the operator asked for errors to be fixed,
// so a payload nobody can read must not pass as "no errors".
func hasErrorFindingsJSON(findingsJSON string) bool {
	if findingsJSON == "" {
		return false
	}
	findings, err := types.ParseFindingsJSON(findingsJSON)
	if err != nil {
		return true
	}
	for _, item := range findings.Items {
		if item.Severity == types.FindingSeverityError {
			return true
		}
	}
	return false
}

// errorFindingsSummary names the error findings that hold the gate, so the
// answer says what to repair rather than only that it was refused.
func errorFindingsSummary(findingsJSON string) string {
	findings, err := types.ParseFindingsJSON(findingsJSON)
	if err != nil {
		return "the findings could not be read"
	}
	var ids []string
	for _, item := range findings.Items {
		if item.Severity == types.FindingSeverityError {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return "an error finding is outstanding"
	}
	if len(ids) == 1 {
		return "error finding " + ids[0] + " is outstanding"
	}
	return "error findings " + strings.Join(ids, ", ") + " are outstanding"
}

// requireErrorsFixed reports whether the operator requires every error finding
// to be repaired before the review gate accepts approve or skip
// (review.require_errors_fixed, trusted default branch only). Off by default,
// and off whenever no config is loaded.
func (e *Executor) requireErrorsFixed() bool {
	if e == nil || e.config == nil {
		return false
	}
	return e.config.Review.RequireErrorsFixed
}

type ProtectedPathError struct {
	Path string
	Rule string
}

func (e *ProtectedPathError) Error() string {
	return fmt.Sprintf("refusing automatic commit: dirty protected path %q matches protected_paths rule %q; index and worktree preserved, inspect and resolve the edit before retrying", e.Path, e.Rule)
}

func ProtectedPathOutcome(err error) *StepOutcome {
	var refusal *ProtectedPathError
	if !errors.As(err, &refusal) {
		return nil
	}
	findings, _ := types.MarshalFindingsJSON(types.Findings{
		Summary: "Automatic commit refused for a protected path",
		Items: []types.Finding{{
			ID:          protectedPathFindingID,
			Severity:    "error",
			File:        refusal.Path,
			Description: err.Error(),
			Action:      types.ActionAskUser,
		}},
	})
	return &StepOutcome{NeedsApproval: true, Findings: findings}
}
