package cli

import (
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/db"
)

func TestAxiStatusListsTheDeferredDefectIssues(t *testing.T) {
	repoDir, _, database, repo := setupAxiQueryRepo(t)
	run(t, repoDir, "git", "checkout", "-b", "feature/mine")
	chdir(t, repoDir)

	mine, err := database.InsertRun(repo.ID, "feature/mine", "head-mine", "base")
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	filed := db.DeferredFindingIssue{
		RepoID: repo.ID, IssueRepo: "zakna/tracker", Fingerprint: "fp-1",
		RunID: mine.ID, StepResultID: "step-1", FindingID: "2",
		Branch: "feature/mine", HeadSHA: "abc123", Severity: "warning",
		Impact: "medium", Outcome: db.DeferredIssueCreated,
		IssueURL: "https://github.com/zakna/tracker/issues/12",
	}
	if _, err := database.RecordDeferredFindingIssue(filed); err != nil {
		t.Fatalf("record filed: %v", err)
	}
	unfiled := filed
	unfiled.ID = ""
	unfiled.Fingerprint = "fp-2"
	unfiled.FindingID = "3"
	unfiled.Impact = ""
	unfiled.Outcome = db.DeferredIssueFailed
	unfiled.IssueURL = ""
	unfiled.Detail = "gh issue create: HTTP 403"
	if _, err := database.RecordDeferredFindingIssue(unfiled); err != nil {
		t.Fatalf("record failed attempt: %v", err)
	}

	out := axiStatusOutput(t, "")

	for _, want := range []string{
		"deferred_issues", "1 filed, 0 skipped, 1 failed",
		"https://github.com/zakna/tracker/issues/12",
		"medium", "not assessed", "HTTP 403",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q in:\n%s", want, out)
		}
	}
}

// With filing off a run has no rows, so status reads exactly as it did before.
func TestAxiStatusSaysNothingAboutDeferredIssuesWhenNoneWereFiled(t *testing.T) {
	repoDir, _, database, repo := setupAxiQueryRepo(t)
	run(t, repoDir, "git", "checkout", "-b", "feature/mine")
	chdir(t, repoDir)

	if _, err := database.InsertRun(repo.ID, "feature/mine", "head-mine", "base"); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if out := axiStatusOutput(t, ""); strings.Contains(out, "deferred_issues") {
		t.Fatalf("status mentions deferred issues with none filed:\n%s", out)
	}
}
