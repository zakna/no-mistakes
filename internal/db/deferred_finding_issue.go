package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// Outcomes recorded for one attempt at filing a deferred review finding.
const (
	// DeferredIssueCreated means the issue exists because this attempt created it.
	DeferredIssueCreated = "created"
	// DeferredIssueSkipped means nothing was created on purpose: the finding was
	// already filed, or the target repository could not receive issues.
	DeferredIssueSkipped = "skipped"
	// DeferredIssueFailed means the attempt could not complete. It does not
	// occupy the duplicate key, so a later gate answer retries the finding.
	DeferredIssueFailed = "failed"
)

// DeferredFindingIssue is one attempt at filing one deferred review finding.
type DeferredFindingIssue struct {
	ID           string
	RepoID       string
	IssueRepo    string
	Fingerprint  string
	RunID        string
	StepResultID string
	FindingID    string
	Branch       string
	HeadSHA      string
	Severity     string
	Impact       string
	Outcome      string
	IssueURL     string
	// Detail is the skip reason or the error text. It is never the finding's own
	// description: the issue itself owns that.
	Detail    string
	CreatedAt int64
}

// FiledDeferredFindingIssue returns the already-created issue for a fingerprint
// in an issue repository, or nil when the finding was never filed there.
//
// This is the authoritative duplicate check. It is keyed by content, not by run
// or branch, so the same defect reported by a later round, a rerun, or another
// branch resolves to the issue that already exists.
func (d *DB) FiledDeferredFindingIssue(issueRepo, fingerprint string) (*DeferredFindingIssue, error) {
	row := d.sql.QueryRow(`
		SELECT id, repo_id, issue_repo, fingerprint, run_id, step_result_id, finding_id,
		       branch, head_sha, severity, impact, outcome, issue_url, detail, created_at
		FROM deferred_finding_issues
		WHERE issue_repo = ? AND fingerprint = ? AND outcome = ?`,
		issueRepo, fingerprint, DeferredIssueCreated)
	var record DeferredFindingIssue
	err := row.Scan(&record.ID, &record.RepoID, &record.IssueRepo, &record.Fingerprint,
		&record.RunID, &record.StepResultID, &record.FindingID, &record.Branch,
		&record.HeadSHA, &record.Severity, &record.Impact, &record.Outcome,
		&record.IssueURL, &record.Detail, &record.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read filed deferred finding issue: %w", err)
	}
	return &record, nil
}

// RecordDeferredFindingIssue appends one attempt.
//
// A created row that collides with an existing created row for the same
// (issue repository, fingerprint) is not an error: it means a concurrent or
// retried attempt already recorded that issue, so the stored row wins and is
// returned. Nothing else in the pipeline depends on this write, so a caller may
// log a failure and continue.
func (d *DB) RecordDeferredFindingIssue(record DeferredFindingIssue) (*DeferredFindingIssue, error) {
	if record.ID == "" {
		record.ID = newID()
	}
	if record.CreatedAt == 0 {
		record.CreatedAt = now()
	}
	_, err := d.sql.Exec(`
		INSERT INTO deferred_finding_issues (
			id, repo_id, issue_repo, fingerprint, run_id, step_result_id, finding_id,
			branch, head_sha, severity, impact, outcome, issue_url, detail, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.RepoID, record.IssueRepo, record.Fingerprint, record.RunID,
		record.StepResultID, record.FindingID, record.Branch, record.HeadSHA,
		record.Severity, record.Impact, record.Outcome, record.IssueURL,
		record.Detail, record.CreatedAt)
	if err != nil {
		if record.Outcome == DeferredIssueCreated {
			if existing, lookupErr := d.FiledDeferredFindingIssue(record.IssueRepo, record.Fingerprint); lookupErr == nil && existing != nil {
				return existing, nil
			}
		}
		return nil, fmt.Errorf("record deferred finding issue: %w", err)
	}
	return &record, nil
}

// DeferredFindingIssuesByRun returns every attempt made during a run, oldest
// first. It backs the run log summary and axi's step view.
func (d *DB) DeferredFindingIssuesByRun(runID string) ([]*DeferredFindingIssue, error) {
	rows, err := d.sql.Query(`
		SELECT id, repo_id, issue_repo, fingerprint, run_id, step_result_id, finding_id,
		       branch, head_sha, severity, impact, outcome, issue_url, detail, created_at
		FROM deferred_finding_issues
		WHERE run_id = ?
		ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list deferred finding issues: %w", err)
	}
	defer rows.Close()
	var records []*DeferredFindingIssue
	for rows.Next() {
		var record DeferredFindingIssue
		if err := rows.Scan(&record.ID, &record.RepoID, &record.IssueRepo, &record.Fingerprint,
			&record.RunID, &record.StepResultID, &record.FindingID, &record.Branch,
			&record.HeadSHA, &record.Severity, &record.Impact, &record.Outcome,
			&record.IssueURL, &record.Detail, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan deferred finding issue: %w", err)
		}
		records = append(records, &record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list deferred finding issues: %w", err)
	}
	return records, nil
}
