package db

import "testing"

func deferredRecord(repoID, runID, fingerprint, outcome string) DeferredFindingIssue {
	return DeferredFindingIssue{
		RepoID: repoID, IssueRepo: "zakna/tracker", Fingerprint: fingerprint,
		RunID: runID, StepResultID: "step-1", FindingID: "1",
		Branch: "feature", HeadSHA: "abc123", Severity: "warning",
		Impact: "medium", Outcome: outcome,
		IssueURL: "https://github.com/zakna/tracker/issues/1",
	}
}

func deferredTestRun(t *testing.T, d *DB) (repoID, runID string) {
	t.Helper()
	repo, err := d.InsertRepoWithID("repo-1", "/work/repo", "https://github.com/zakna/nm.git", "main")
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.InsertRun(repo.ID, "feature", "head", "base")
	if err != nil {
		t.Fatal(err)
	}
	return repo.ID, run.ID
}

func TestDeferredFindingIssue_CreatedRowIsFoundByContentKey(t *testing.T) {
	d := openTestDB(t)
	repoID, runID := deferredTestRun(t, d)

	if _, err := d.RecordDeferredFindingIssue(deferredRecord(repoID, runID, "fp-1", DeferredIssueCreated)); err != nil {
		t.Fatalf("record: %v", err)
	}
	filed, err := d.FiledDeferredFindingIssue("zakna/tracker", "fp-1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if filed == nil || filed.IssueURL != "https://github.com/zakna/tracker/issues/1" {
		t.Fatalf("filed = %+v, want the created row", filed)
	}
	// Another tracker is another question, even for the same fingerprint.
	other, err := d.FiledDeferredFindingIssue("someone/else", "fp-1")
	if err != nil || other != nil {
		t.Fatalf("other tracker = %+v, err = %v, want nothing", other, err)
	}
}

func TestDeferredFindingIssue_SecondCreatedRowReturnsTheStoredOne(t *testing.T) {
	d := openTestDB(t)
	repoID, runID := deferredTestRun(t, d)

	first, err := d.RecordDeferredFindingIssue(deferredRecord(repoID, runID, "fp-1", DeferredIssueCreated))
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	duplicate := deferredRecord(repoID, runID, "fp-1", DeferredIssueCreated)
	duplicate.IssueURL = "https://github.com/zakna/tracker/issues/99"
	second, err := d.RecordDeferredFindingIssue(duplicate)
	if err != nil {
		t.Fatalf("duplicate record = error %v, want the stored row", err)
	}
	if second.ID != first.ID || second.IssueURL != first.IssueURL {
		t.Fatalf("duplicate returned %+v, want the stored row %+v", second, first)
	}
}

// Only a created row occupies the duplicate key, so a failed or skipped attempt
// never blocks a retry.
func TestDeferredFindingIssue_FailedAndSkippedRowsDoNotOccupyTheKey(t *testing.T) {
	d := openTestDB(t)
	repoID, runID := deferredTestRun(t, d)

	for _, outcome := range []string{DeferredIssueFailed, DeferredIssueSkipped, DeferredIssueFailed} {
		record := deferredRecord(repoID, runID, "fp-1", outcome)
		record.IssueURL = ""
		record.Detail = "gh issue create: HTTP 403"
		if _, err := d.RecordDeferredFindingIssue(record); err != nil {
			t.Fatalf("record %s: %v", outcome, err)
		}
	}
	if filed, err := d.FiledDeferredFindingIssue("zakna/tracker", "fp-1"); err != nil || filed != nil {
		t.Fatalf("filed = %+v, err = %v, want nothing filed yet", filed, err)
	}
	if _, err := d.RecordDeferredFindingIssue(deferredRecord(repoID, runID, "fp-1", DeferredIssueCreated)); err != nil {
		t.Fatalf("retry after failures: %v", err)
	}
	filed, err := d.FiledDeferredFindingIssue("zakna/tracker", "fp-1")
	if err != nil || filed == nil {
		t.Fatalf("filed = %+v, err = %v, want the created row", filed, err)
	}
}

func TestDeferredFindingIssuesByRun_ListsEveryAttempt(t *testing.T) {
	d := openTestDB(t)
	repoID, runID := deferredTestRun(t, d)

	for _, fingerprint := range []string{"fp-1", "fp-2"} {
		if _, err := d.RecordDeferredFindingIssue(deferredRecord(repoID, runID, fingerprint, DeferredIssueCreated)); err != nil {
			t.Fatal(err)
		}
	}
	failed := deferredRecord(repoID, runID, "fp-3", DeferredIssueFailed)
	failed.IssueURL = ""
	if _, err := d.RecordDeferredFindingIssue(failed); err != nil {
		t.Fatal(err)
	}
	records, err := d.DeferredFindingIssuesByRun(runID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3", len(records))
	}
	if records, err := d.DeferredFindingIssuesByRun("other-run"); err != nil || len(records) != 0 {
		t.Fatalf("other run = %+v, err = %v, want nothing", records, err)
	}
}
