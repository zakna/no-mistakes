package cli

import (
	"fmt"

	toon "github.com/toon-format/toon-go"

	"github.com/kunchenguid/no-mistakes/internal/db"
)

// deferredIssueRow is one attempt at filing a deferred review finding.
type deferredIssueRow struct {
	Finding  string `toon:"finding"`
	Severity string `toon:"severity"`
	Impact   string `toon:"impact"`
	Outcome  string `toon:"outcome"`
	Issue    string `toon:"issue"`
	Detail   string `toon:"detail"`
}

// deferredIssueFields renders the defect issues a run filed for the review
// findings that shipped unfixed.
//
// This is how a supervising agent closes the loop right after it answers a
// review gate: it sees at once that the warning it accepted became a tracked
// defect, and it sees any finding that could not be filed. A read error is
// reported as text rather than hidden, because silence here would read as "no
// defects were deferred". Nothing is emitted when the feature is off, because
// the run then has no rows at all.
func deferredIssueFields(database *db.DB, runID string) []toon.Field {
	if database == nil || runID == "" {
		return nil
	}
	records, err := database.DeferredFindingIssuesByRun(runID)
	if err != nil {
		return []toon.Field{{Key: "deferred_issues", Value: fmt.Sprintf("could not be read: %v", err)}}
	}
	if len(records) == 0 {
		return nil
	}
	var created, skipped, failed int
	rows := make([]deferredIssueRow, 0, len(records))
	for _, record := range records {
		switch record.Outcome {
		case db.DeferredIssueCreated:
			created++
		case db.DeferredIssueSkipped:
			skipped++
		case db.DeferredIssueFailed:
			failed++
		}
		impact := record.Impact
		if impact == "" {
			impact = "not assessed"
		}
		rows = append(rows, deferredIssueRow{
			Finding:  record.FindingID,
			Severity: record.Severity,
			Impact:   impact,
			Outcome:  record.Outcome,
			Issue:    record.IssueURL,
			Detail:   record.Detail,
		})
	}
	return []toon.Field{
		{Key: "deferred_issues", Value: fmt.Sprintf("%d filed, %d skipped, %d failed", created, skipped, failed)},
		{Key: "deferred_issue_details", Value: rows},
	}
}
