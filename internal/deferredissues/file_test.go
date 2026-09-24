package deferredissues

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

type fakeStore struct {
	filed    map[string]*db.DeferredFindingIssue
	recorded []db.DeferredFindingIssue
	readErr  error
	writeErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{filed: map[string]*db.DeferredFindingIssue{}}
}

func (s *fakeStore) FiledDeferredFindingIssue(issueRepo, fingerprint string) (*db.DeferredFindingIssue, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.filed[issueRepo+"/"+fingerprint], nil
}

func (s *fakeStore) RecordDeferredFindingIssue(record db.DeferredFindingIssue) (*db.DeferredFindingIssue, error) {
	if s.writeErr != nil {
		return nil, s.writeErr
	}
	s.recorded = append(s.recorded, record)
	if record.Outcome == db.DeferredIssueCreated {
		s.filed[record.IssueRepo+"/"+record.Fingerprint] = &record
	}
	return &record, nil
}

type fakeIssues struct {
	created    []scm.IssueContent
	ensured    [][]scm.Label
	missing    []string
	createErr  error
	ensureErr  error
	markerHit  string
	markerErr  error
	permission bool
	permErr    error
	permCalls  int
}

func (f *fakeIssues) CreateIssue(_ context.Context, content scm.IssueContent) (*scm.Issue, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = append(f.created, content)
	return &scm.Issue{URL: "https://github.com/o/r/issues/1", Number: "1"}, nil
}

func (f *fakeIssues) EnsureLabels(_ context.Context, _ string, labels []scm.Label) ([]string, error) {
	f.ensured = append(f.ensured, labels)
	return f.missing, f.ensureErr
}

func (f *fakeIssues) FindIssueByMarker(_ context.Context, _, _ string) (string, error) {
	return f.markerHit, f.markerErr
}

func (f *fakeIssues) CanFileIssues(_ context.Context, _ string) (bool, error) {
	f.permCalls++
	return f.permission, f.permErr
}

type fakeImpact struct {
	result map[string]Assessment
	err    error
	calls  int
}

func (f *fakeImpact) AssessImpact(_ context.Context, _ []types.Finding) (map[string]Assessment, error) {
	f.calls++
	return f.result, f.err
}

func warning(id, file, text string) types.Finding {
	return types.Finding{ID: id, Severity: types.FindingSeverityWarning, File: file, Line: 42, Description: text}
}

func request(findings ...types.Finding) Request {
	return Request{
		RepoID: "repo-1", RunID: "run-1", StepResultID: "step-1",
		Branch: "feature", HeadSHA: "abc1234", IssueRepo: "zakna/no-mistakes",
		Findings: findings,
	}
}

func TestFile_CreatesOneIssuePerFindingWithFirstTriage(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: true}
	impact := &fakeImpact{result: map[string]Assessment{"1": {Impact: ImpactMedium, Why: "bursts of calls on outage"}}}
	filer := &Filer{Store: store, Issues: issues, Impact: impact}

	results := filer.File(context.Background(), request(warning("1", "internal/fetch/fetch.go", "The retry loop has no backoff")))

	if len(results) != 1 || results[0].Outcome != db.DeferredIssueCreated {
		t.Fatalf("results = %+v, want one created", results)
	}
	if len(issues.created) != 1 {
		t.Fatalf("created %d issues, want 1", len(issues.created))
	}
	created := issues.created[0]
	if created.Repo != "zakna/no-mistakes" {
		t.Fatalf("issue repo = %q", created.Repo)
	}
	if !strings.HasPrefix(created.Title, "[no-mistakes] warning:") {
		t.Fatalf("title = %q", created.Title)
	}
	for _, want := range []string{"The retry loop has no backoff", "Severity: warning",
		"Impact: medium - bursts of calls on outage", "internal/fetch/fetch.go:42",
		"Run: run-1", "Branch: feature", "Head commit: abc1234", "## Triage", markerPrefix} {
		if !strings.Contains(created.Body, want) {
			t.Fatalf("body missing %q:\n%s", want, created.Body)
		}
	}
	wantLabels := []string{LabelDefect, "severity:warning", "impact:medium"}
	for _, want := range wantLabels {
		if !contains(created.Labels, want) {
			t.Fatalf("labels %v missing %q", created.Labels, want)
		}
	}
	if len(store.recorded) != 1 || store.recorded[0].Outcome != db.DeferredIssueCreated || store.recorded[0].IssueURL == "" {
		t.Fatalf("recorded = %+v, want one created row with a URL", store.recorded)
	}
}

func TestFile_SameFindingIsNeverFiledTwice(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: true}
	filer := &Filer{Store: store, Issues: issues, Impact: &fakeImpact{}}
	finding := warning("1", "internal/fetch/fetch.go", "The retry loop has no backoff")

	filer.File(context.Background(), request(finding))

	// A later round, rerun, or branch reports the same defect with a moved line
	// and a new finding ID.
	moved := finding
	moved.ID = "3"
	moved.Line = 57
	second := request(moved)
	second.RunID = "run-2"
	second.Branch = "another-branch"
	results := filer.File(context.Background(), second)

	if len(issues.created) != 1 {
		t.Fatalf("created %d issues, want 1", len(issues.created))
	}
	if results[0].Outcome != db.DeferredIssueSkipped || results[0].IssueURL == "" {
		t.Fatalf("second attempt = %+v, want skipped with the existing URL", results[0])
	}
}

func TestFile_ForgeMarkerHitSuppressesAFileWithNoLocalRecord(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: true, markerHit: "https://github.com/o/r/issues/9"}
	filer := &Filer{Store: store, Issues: issues, Impact: &fakeImpact{}}

	results := filer.File(context.Background(), request(warning("1", "a.go", "same defect")))

	if len(issues.created) != 0 {
		t.Fatal("a marker hit must suppress creation")
	}
	if results[0].IssueURL != "https://github.com/o/r/issues/9" {
		t.Fatalf("result = %+v, want the forge URL", results[0])
	}
}

func TestFile_AlreadyFiledBatchCostsNoImpactCall(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: true, markerHit: "https://github.com/o/r/issues/9"}
	impact := &fakeImpact{}
	filer := &Filer{Store: store, Issues: issues, Impact: impact}

	filer.File(context.Background(), request(warning("1", "a.go", "same defect")))

	if impact.calls != 0 {
		t.Fatalf("impact called %d times, want 0", impact.calls)
	}
}

func TestFile_CreateFailureIsRecordedAndNeverBlocks(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: true, createErr: errors.New("gh issue create: HTTP 403")}
	var logged []string
	filer := &Filer{Store: store, Issues: issues, Impact: &fakeImpact{}, Log: func(line string) { logged = append(logged, line) }}

	results := filer.File(context.Background(), request(warning("1", "a.go", "defect")))

	if results[0].Outcome != db.DeferredIssueFailed || !strings.Contains(results[0].Detail, "403") {
		t.Fatalf("result = %+v, want a failed outcome carrying the error", results[0])
	}
	if len(store.recorded) != 1 || store.recorded[0].Outcome != db.DeferredIssueFailed {
		t.Fatalf("recorded = %+v, want one failed row", store.recorded)
	}
	if !strings.Contains(strings.Join(logged, "\n"), "could not be filed") {
		t.Fatalf("log missing the failure: %v", logged)
	}
	if !strings.Contains(strings.Join(logged, "\n"), "0 filed, 0 skipped, 1 failed") {
		t.Fatalf("log missing the summary: %v", logged)
	}
}

func TestFile_FailedAttemptDoesNotSuppressALaterRetry(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: true, createErr: errors.New("boom")}
	filer := &Filer{Store: store, Issues: issues, Impact: &fakeImpact{}}
	finding := warning("1", "a.go", "defect")

	filer.File(context.Background(), request(finding))
	issues.createErr = nil
	results := filer.File(context.Background(), request(finding))

	if results[0].Outcome != db.DeferredIssueCreated {
		t.Fatalf("retry = %+v, want created", results[0])
	}
}

func TestFile_ImpactFailureStillFilesWithoutAnImpactLabel(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: true}
	filer := &Filer{Store: store, Issues: issues, Impact: &fakeImpact{err: errors.New("agent timeout")}}

	results := filer.File(context.Background(), request(warning("1", "a.go", "defect")))

	if results[0].Outcome != db.DeferredIssueCreated {
		t.Fatalf("result = %+v, want created", results[0])
	}
	if results[0].Impact != ImpactUnassessed {
		t.Fatalf("impact = %q, want %q", results[0].Impact, ImpactUnassessed)
	}
	body := issues.created[0].Body
	if !strings.Contains(body, "Impact: not assessed") {
		t.Fatalf("body missing the unassessed impact:\n%s", body)
	}
	for _, label := range issues.created[0].Labels {
		if strings.HasPrefix(label, LabelImpactPrefix) {
			t.Fatalf("labels %v must carry no impact label", issues.created[0].Labels)
		}
	}
}

func TestFile_LabelFailureStillFilesTheIssue(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: true, missing: []string{"severity:warning"}}
	filer := &Filer{Store: store, Issues: issues, Impact: &fakeImpact{}}

	results := filer.File(context.Background(), request(warning("1", "a.go", "defect")))

	if results[0].Outcome != db.DeferredIssueCreated {
		t.Fatalf("result = %+v, want created", results[0])
	}
	if contains(issues.created[0].Labels, "severity:warning") {
		t.Fatalf("labels %v must drop the one that could not be ensured", issues.created[0].Labels)
	}
	if !contains(issues.created[0].Labels, LabelDefect) {
		t.Fatalf("labels %v must keep the defect label", issues.created[0].Labels)
	}
}

func TestFile_NoPushPermissionFilesNothingAndSaysWhy(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: false}
	filer := &Filer{Store: store, Issues: issues, Impact: &fakeImpact{}}

	results := filer.File(context.Background(), request(warning("1", "a.go", "defect")))

	if len(issues.created) != 0 {
		t.Fatal("no issue may be opened without permission")
	}
	if results[0].Outcome != db.DeferredIssueSkipped || !strings.Contains(results[0].Detail, "issue_repos") {
		t.Fatalf("result = %+v, want a skip naming the setting", results[0])
	}
}

func TestFile_UnansweredPermissionCheckCountsAsNoPermission(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: true, permErr: errors.New("gh: not logged in")}
	filer := &Filer{Store: store, Issues: issues, Impact: &fakeImpact{}}

	results := filer.File(context.Background(), request(warning("1", "a.go", "defect")))

	if len(issues.created) != 0 {
		t.Fatal("an unanswered permission question must not open an issue")
	}
	if results[0].Outcome != db.DeferredIssueSkipped {
		t.Fatalf("result = %+v, want skipped", results[0])
	}
}

func TestFile_ExplicitTargetSkipsThePermissionCheck(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{}
	filer := &Filer{Store: store, Issues: issues, Impact: &fakeImpact{}}
	req := request(warning("1", "a.go", "defect"))
	req.PermissionChecked = true

	results := filer.File(context.Background(), req)

	if issues.permCalls != 0 {
		t.Fatalf("permission checked %d times, want 0 for an operator-named tracker", issues.permCalls)
	}
	if results[0].Outcome != db.DeferredIssueCreated {
		t.Fatalf("result = %+v, want created", results[0])
	}
}

func TestFile_NoTargetRepositoryFilesNothing(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: true}
	filer := &Filer{Store: store, Issues: issues, Impact: &fakeImpact{}}
	req := request(warning("1", "a.go", "defect"))
	req.IssueRepo = ""

	results := filer.File(context.Background(), req)

	if len(issues.created) != 0 || results[0].Outcome != db.DeferredIssueSkipped {
		t.Fatalf("results = %+v, created = %d", results, len(issues.created))
	}
}

func TestFile_EmptyBatchDoesNothing(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: true}
	impact := &fakeImpact{}
	filer := &Filer{Store: store, Issues: issues, Impact: impact}

	results := filer.File(context.Background(), request())

	if len(results) != 0 || len(store.recorded) != 0 || impact.calls != 0 || issues.permCalls != 0 {
		t.Fatalf("an empty batch must touch nothing: results=%v recorded=%v impact=%d perm=%d",
			results, store.recorded, impact.calls, issues.permCalls)
	}
}

func TestFile_RecordWriteFailureStillFilesAndLogs(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: true}
	store.writeErr = errors.New("database is locked")
	var logged []string
	filer := &Filer{Store: store, Issues: issues, Impact: &fakeImpact{}, Log: func(line string) { logged = append(logged, line) }}

	results := filer.File(context.Background(), request(warning("1", "a.go", "defect")))

	if results[0].Outcome != db.DeferredIssueCreated {
		t.Fatalf("result = %+v, want created", results[0])
	}
	if !strings.Contains(strings.Join(logged, "\n"), "local record not written") {
		t.Fatalf("log missing the record failure: %v", logged)
	}
}

func TestFile_GateAnswerIsCopiedIntoTheIssue(t *testing.T) {
	store, issues := newFakeStore(), &fakeIssues{permission: true}
	filer := &Filer{Store: store, Issues: issues, Impact: &fakeImpact{}}
	req := request(warning("1", "a.go", "defect"))
	req.GateAnswers = map[string]string{"1": GateUnfixed}

	filer.File(context.Background(), req)

	if !strings.Contains(issues.created[0].Body, GateUnfixed) {
		t.Fatalf("body missing the gate answer:\n%s", issues.created[0].Body)
	}
}

func TestFingerprint_IdentityRules(t *testing.T) {
	base := warning("1", "a.go", "The retry loop has no backoff")
	moved := base
	moved.Line = 99
	moved.ID = "7"
	if Fingerprint("o/r", base) != Fingerprint("o/r", moved) {
		t.Fatal("a moved line or a new ID must keep the same identity")
	}
	reformatted := base
	reformatted.Description = "  the RETRY loop   has no backoff "
	if Fingerprint("o/r", base) != Fingerprint("o/r", reformatted) {
		t.Fatal("case and spacing must not change identity")
	}
	other := base
	other.File = "b.go"
	if Fingerprint("o/r", base) == Fingerprint("o/r", other) {
		t.Fatal("another file must be another identity")
	}
	if Fingerprint("o/r", base) == Fingerprint("other/tracker", base) {
		t.Fatal("another tracker must be another identity")
	}
	// The NUL separators keep concatenation unambiguous.
	left := types.Finding{File: "a", Description: "bc", Severity: "warning"}
	right := types.Finding{File: "ab", Description: "c", Severity: "warning"}
	if Fingerprint("o/r", left) == Fingerprint("o/r", right) {
		t.Fatal("field boundaries must be unambiguous")
	}
}
