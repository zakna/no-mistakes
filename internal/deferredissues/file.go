package deferredissues

import (
	"context"
	"fmt"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/safeurl"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

// Store is the local record this package needs. The local record is the
// authority on "already filed": a forge search index can lag, a local row
// cannot.
type Store interface {
	FiledDeferredFindingIssue(issueRepo, fingerprint string) (*db.DeferredFindingIssue, error)
	RecordDeferredFindingIssue(record db.DeferredFindingIssue) (*db.DeferredFindingIssue, error)
}

// ImpactAssessor produces the first-triage impact for a batch of findings,
// keyed by finding ID. It is one call for the whole batch. An error, or a
// finding missing from the result, leaves that finding's impact unassessed and
// never stops filing.
type ImpactAssessor interface {
	AssessImpact(ctx context.Context, findings []types.Finding) (map[string]Assessment, error)
}

// Assessment is one finding's assessed impact.
type Assessment struct {
	Impact string
	Why    string
}

// Request is one filing batch: the findings a completed review step left
// unfixed, plus the run facts every issue reports.
type Request struct {
	RepoID       string
	RunID        string
	StepResultID string
	Branch       string
	HeadSHA      string
	// IssueRepo is the already-resolved target (see ResolveIssueRepo).
	IssueRepo string
	// PermissionChecked is true when the target came from the operator's
	// explicit override, which needs no push-permission check: naming the
	// repository IS the operator's authorization.
	PermissionChecked bool
	// GateAnswers maps a finding ID to how it was deferred (see GateApproved
	// and friends). A finding with no entry reports GateApproved.
	GateAnswers map[string]string
	Findings    []types.Finding
}

// Result is one finding's filing outcome, ready for the log and the record.
type Result struct {
	FindingID string
	Severity  string
	Impact    string
	Outcome   string // db.DeferredIssueCreated, Skipped, or Failed
	IssueURL  string
	Detail    string
}

// LogLine renders one outcome for the run log. It never includes the finding's
// description: the issue owns that text, and the log is shown in terminals.
func (r Result) LogLine() string {
	switch r.Outcome {
	case db.DeferredIssueCreated:
		return fmt.Sprintf("deferred finding %s (%s) filed as %s", r.FindingID, r.Severity, r.IssueURL)
	case db.DeferredIssueSkipped:
		if r.IssueURL != "" {
			return fmt.Sprintf("deferred finding %s (%s) already filed as %s; skipped", r.FindingID, r.Severity, r.IssueURL)
		}
		return fmt.Sprintf("deferred finding %s (%s) not filed: %s", r.FindingID, r.Severity, r.Detail)
	default:
		return fmt.Sprintf("deferred finding %s (%s) could not be filed: %s", r.FindingID, r.Severity, r.Detail)
	}
}

// Summary renders the one-line total for the run log.
func Summary(results []Result) string {
	var created, skipped, failed int
	for _, result := range results {
		switch result.Outcome {
		case db.DeferredIssueCreated:
			created++
		case db.DeferredIssueSkipped:
			skipped++
		default:
			failed++
		}
	}
	return fmt.Sprintf("deferred findings: %d filed, %d skipped, %d failed", created, skipped, failed)
}

// Filer files deferred findings. Every dependency is an interface so the whole
// path is testable without a forge, an agent, or a network.
type Filer struct {
	Store  Store
	Issues scm.IssueHost
	Impact ImpactAssessor
	// Log receives one user-visible line per finding plus a summary. Optional.
	Log func(string)
}

// File files every finding in the request and returns one result per finding, in
// request order. It never returns an error: a failure is an outcome.
func (f *Filer) File(ctx context.Context, req Request) []Result {
	results := make([]Result, 0, len(req.Findings))
	if len(req.Findings) == 0 {
		return results
	}
	if f.Issues == nil || strings.TrimSpace(req.IssueRepo) == "" {
		return f.recordAll(req, db.DeferredIssueSkipped, "no GitHub issue repository resolved for this repository; set file_deferred_findings.issue_repos")
	}
	if !req.PermissionChecked {
		allowed, err := f.Issues.CanFileIssues(ctx, req.IssueRepo)
		if err != nil {
			return f.recordAll(req, db.DeferredIssueSkipped, fmt.Sprintf("cannot check push permission on %s: %s", req.IssueRepo, safeurl.RedactText(err.Error())))
		}
		if !allowed {
			return f.recordAll(req, db.DeferredIssueSkipped, fmt.Sprintf("no push permission on %s; name a tracker in file_deferred_findings.issue_repos", req.IssueRepo))
		}
	}

	// Duplicates are settled before the impact call so a batch that is entirely
	// already-filed costs no agent tokens.
	pending := make([]types.Finding, 0, len(req.Findings))
	preset := make(map[string]Result, len(req.Findings))
	for _, finding := range req.Findings {
		fingerprint := Fingerprint(req.IssueRepo, finding)
		existing, err := f.Store.FiledDeferredFindingIssue(req.IssueRepo, fingerprint)
		if err == nil && existing != nil {
			preset[finding.ID] = Result{FindingID: finding.ID, Severity: finding.Severity, Outcome: db.DeferredIssueSkipped, IssueURL: existing.IssueURL, Detail: "already filed"}
			continue
		}
		if url, searchErr := f.Issues.FindIssueByMarker(ctx, req.IssueRepo, Marker(fingerprint)); searchErr == nil && url != "" {
			preset[finding.ID] = Result{FindingID: finding.ID, Severity: finding.Severity, Outcome: db.DeferredIssueSkipped, IssueURL: url, Detail: "already filed on the forge"}
			continue
		}
		pending = append(pending, finding)
	}

	assessments := f.assess(ctx, pending)

	for _, finding := range req.Findings {
		if result, ok := preset[finding.ID]; ok {
			results = append(results, f.record(req, finding, result))
			continue
		}
		results = append(results, f.record(req, finding, f.createOne(ctx, req, finding, assessments[finding.ID])))
	}
	if f.Log != nil {
		f.Log(Summary(results))
	}
	return results
}

// assess runs the single impact call. Any failure downgrades the whole batch to
// an unassessed impact instead of blocking the filing.
func (f *Filer) assess(ctx context.Context, findings []types.Finding) map[string]Assessment {
	if f.Impact == nil || len(findings) == 0 {
		return nil
	}
	assessments, err := f.Impact.AssessImpact(ctx, findings)
	if err != nil {
		if f.Log != nil {
			f.Log("deferred findings: impact not assessed: " + safeurl.RedactText(err.Error()))
		}
		return nil
	}
	return assessments
}

func (f *Filer) createOne(ctx context.Context, req Request, finding types.Finding, assessment Assessment) Result {
	fingerprint := Fingerprint(req.IssueRepo, finding)
	impact := strings.ToLower(strings.TrimSpace(assessment.Impact))
	switch impact {
	case ImpactHigh, ImpactMedium, ImpactLow:
	default:
		impact = ImpactUnassessed
	}
	labels := []scm.Label{
		{Name: LabelDefect, Color: "d73a4a", Description: "A defect no-mistakes found and did not fix"},
		{Name: SeverityLabel(finding.Severity), Color: "fbca04", Description: "Review severity reported by no-mistakes"},
	}
	if name := ImpactLabel(impact); name != "" {
		labels = append(labels, scm.Label{Name: name, Color: "0e8a16", Description: "First-triage impact assessed by no-mistakes"})
	}
	missing, err := f.Issues.EnsureLabels(ctx, req.IssueRepo, labels)
	if err != nil && f.Log != nil {
		f.Log("deferred findings: labels could not be ensured: " + safeurl.RedactText(err.Error()))
	}
	applied := make([]string, 0, len(labels))
	for _, label := range labels {
		if !contains(missing, label.Name) {
			applied = append(applied, label.Name)
		}
	}
	gateAnswer := req.GateAnswers[finding.ID]
	if gateAnswer == "" {
		gateAnswer = GateApproved
	}
	issue, err := f.Issues.CreateIssue(ctx, scm.IssueContent{
		Repo:  req.IssueRepo,
		Title: Title(finding),
		Body: Body(BodyInput{
			Finding: finding, Impact: impact, ImpactWhy: assessment.Why,
			GateAnswer: gateAnswer, RunID: req.RunID, Branch: req.Branch,
			HeadSHA: req.HeadSHA, IssueRepo: req.IssueRepo, Fingerprint: fingerprint,
		}),
		Labels: applied,
	})
	if err != nil {
		return Result{FindingID: finding.ID, Severity: finding.Severity, Impact: impact, Outcome: db.DeferredIssueFailed, Detail: safeurl.RedactText(err.Error())}
	}
	detail := ""
	if len(missing) > 0 {
		detail = "labels not applied: " + strings.Join(missing, ", ")
	}
	return Result{FindingID: finding.ID, Severity: finding.Severity, Impact: impact, Outcome: db.DeferredIssueCreated, IssueURL: issue.URL, Detail: detail}
}

// recordAll records the same outcome for every finding, used when the target
// repository itself cannot receive issues.
func (f *Filer) recordAll(req Request, outcome, detail string) []Result {
	results := make([]Result, 0, len(req.Findings))
	for _, finding := range req.Findings {
		results = append(results, f.record(req, finding, Result{
			FindingID: finding.ID, Severity: finding.Severity,
			Outcome: outcome, Detail: detail,
		}))
	}
	if f.Log != nil {
		f.Log(Summary(results))
	}
	return results
}

// record writes the local row and logs the line. A failed write is logged and
// dropped: the row is evidence, and losing evidence must not fail a run.
func (f *Filer) record(req Request, finding types.Finding, result Result) Result {
	if f.Log != nil {
		f.Log(result.LogLine())
	}
	if f.Store == nil {
		return result
	}
	_, err := f.Store.RecordDeferredFindingIssue(db.DeferredFindingIssue{
		RepoID:       req.RepoID,
		IssueRepo:    req.IssueRepo,
		Fingerprint:  Fingerprint(req.IssueRepo, finding),
		RunID:        req.RunID,
		StepResultID: req.StepResultID,
		FindingID:    finding.ID,
		Branch:       req.Branch,
		HeadSHA:      req.HeadSHA,
		Severity:     finding.Severity,
		Impact:       result.Impact,
		Outcome:      result.Outcome,
		IssueURL:     result.IssueURL,
		Detail:       result.Detail,
	})
	if err != nil && f.Log != nil {
		f.Log("deferred findings: local record not written: " + safeurl.RedactText(err.Error()))
	}
	return result
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
