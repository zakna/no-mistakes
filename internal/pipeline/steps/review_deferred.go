package steps

import (
	"context"
	"strings"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/deferredissues"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

// deferredFilingBudget bounds the whole filing pass. A slow or hanging forge
// must not hold a completed step open: the run is already decided, and the
// findings it could not file are recorded as failed and retried at the next
// gate answer.
const deferredFilingBudget = 90 * time.Second

var _ pipeline.DeferredFindingsFiler = (*ReviewStep)(nil)

// FileDeferredFindings files the review findings that shipped unfixed as GitHub
// defect issues, so an approved or skipped warning is never lost.
//
// It never returns an error and never fails the step (see
// pipeline.DeferredFindingsFiler): every problem becomes a logged outcome and a
// local record row.
func (s *ReviewStep) FileDeferredFindings(sctx *pipeline.StepContext, findingsJSON string, answers map[string]string) {
	if sctx == nil || sctx.Config == nil || !sctx.Config.FilesDeferredReviewFindings() {
		return
	}
	findings, err := types.ParseFindingsJSON(findingsJSON)
	if err != nil || len(findings.Items) == 0 {
		return
	}
	logfn := func(line string) {
		if sctx.Log != nil {
			sctx.Log(line + "\n")
		}
	}

	target := deferredissues.ResolveIssueRepo(
		sctx.Config.IssueRepoOverrideFor(sctx.Repo.UpstreamURL),
		sctx.Repo.ForkURL,
		sctx.Repo.UpstreamURL,
		func(remote string) string { return resolvedHost(sctx, remote) },
	)
	if target.Repo == "" {
		// Nothing can receive issues. Say so once, with the findings named, so
		// the defects are at least visible in the run log.
		logfn("deferred findings not filed: " + target.Reason)
		for _, item := range findings.Items {
			logfn("deferred finding " + item.ID + " (" + item.Severity + ") stays unfiled")
		}
		return
	}

	ctx, cancel := context.WithTimeout(sctx.Ctx, deferredFilingBudget)
	defer cancel()

	filer := &deferredissues.Filer{
		Store:  sctx.DB,
		Issues: s.issueHost(sctx),
		Impact: &deferredissues.AgentImpactAssessor{Agent: sctx.Agent, CWD: sctx.WorkDir, Env: sctx.Env},
		Log:    logfn,
	}
	if filer.Issues == nil {
		logfn("deferred findings not filed: this build cannot open issues on the resolved repository")
		return
	}
	headSHA, _ := stepGitHeadSHA(sctx)
	filer.File(ctx, deferredissues.Request{
		RepoID:            sctx.Repo.ID,
		RunID:             sctx.Run.ID,
		StepResultID:      sctx.StepResultID,
		Branch:            sctx.Run.Branch,
		HeadSHA:           headSHA,
		IssueRepo:         target.Repo,
		PermissionChecked: target.Explicit,
		GateAnswers:       gateAnswers(answers),
		Findings:          findings.Items,
	})
}

// issueHost builds the GitHub host that opens issues. Issue filing always goes
// through GitHub, even when the pipeline itself pushes to another forge, so the
// host is built directly rather than from the run's own provider.
func (s *ReviewStep) issueHost(sctx *pipeline.StepContext) scm.IssueHost {
	host, _ := buildHost(sctx, scm.ProviderGitHub)
	issues, ok := host.(scm.IssueHost)
	if !ok {
		return nil
	}
	// buildHost scopes gh to the run's own repository, which is irrelevant
	// here: every issue call carries its own target repository.
	return issues
}

// gateAnswers translates the executor's deferral reasons into the wording that
// appears in an issue body.
func gateAnswers(answers map[string]string) map[string]string {
	if len(answers) == 0 {
		return nil
	}
	translated := make(map[string]string, len(answers))
	for id, reason := range answers {
		switch strings.TrimSpace(reason) {
		case pipeline.DeferralSkipped:
			translated[id] = deferredissues.GateSkipped
		case pipeline.DeferralCompleted:
			translated[id] = deferredissues.GateCompleted
		case pipeline.DeferralUnfixed:
			translated[id] = deferredissues.GateUnfixed
		default:
			translated[id] = deferredissues.GateApproved
		}
	}
	return translated
}
