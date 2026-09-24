package steps

import (
	"context"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/deferredissues"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
)

const deferredTestFindings = `{"findings":[{"id":"1","severity":"warning","file":"a.go","description":"The retry loop has no backoff"}]}`

func deferredStepContext(t *testing.T, cfg *config.Config, repo *db.Repo, log func(string)) *pipeline.StepContext {
	t.Helper()
	return &pipeline.StepContext{
		Ctx:     context.Background(),
		Run:     &db.Run{ID: "run-1", Branch: "feature"},
		Repo:    repo,
		Config:  cfg,
		WorkDir: t.TempDir(),
		Log:     log,
	}
}

func filingOnConfig() *config.Config {
	return config.Merge(&config.GlobalConfig{
		FileDeferredFindings: config.FileDeferredFindingsRaw{Review: config.FileDeferredFindingsGitHubIssue},
	}, &config.RepoConfig{})
}

// A defect is never lost silently: with no repository able to receive issues,
// the step names every finding it could not file in the run log.
func TestReviewStep_NoIssueRepositoryLogsEveryUnfiledFinding(t *testing.T) {
	var logged []string
	sctx := deferredStepContext(t, filingOnConfig(),
		&db.Repo{ID: "repo-1", UpstreamURL: "git@bitbucket.org:team/actor.git"},
		func(line string) { logged = append(logged, line) })

	(&ReviewStep{}).FileDeferredFindings(sctx, deferredTestFindings, nil)

	text := strings.Join(logged, "")
	if !strings.Contains(text, "not filed") {
		t.Fatalf("log = %q, want the reason", text)
	}
	if !strings.Contains(text, "deferred finding 1") || !strings.Contains(text, "stays unfiled") {
		t.Fatalf("log = %q, want the finding named", text)
	}
}

func TestReviewStep_FilingOffTouchesNothing(t *testing.T) {
	var logged []string
	off := config.Merge(&config.GlobalConfig{}, &config.RepoConfig{})
	sctx := deferredStepContext(t, off,
		&db.Repo{ID: "repo-1", UpstreamURL: "git@bitbucket.org:team/actor.git"},
		func(line string) { logged = append(logged, line) })

	(&ReviewStep{}).FileDeferredFindings(sctx, deferredTestFindings, nil)

	if len(logged) != 0 {
		t.Fatalf("log = %v, want nothing with filing off", logged)
	}
}

func TestReviewStep_UnreadableOrEmptyFindingsTouchNothing(t *testing.T) {
	var logged []string
	sctx := deferredStepContext(t, filingOnConfig(),
		&db.Repo{ID: "repo-1", UpstreamURL: "git@bitbucket.org:team/actor.git"},
		func(line string) { logged = append(logged, line) })

	step := &ReviewStep{}
	step.FileDeferredFindings(sctx, "not json", nil)
	step.FileDeferredFindings(sctx, `{"findings":[]}`, nil)

	if len(logged) != 0 {
		t.Fatalf("log = %v, want nothing to file", logged)
	}
}

func TestGateAnswers_TranslateEveryDeferralReason(t *testing.T) {
	answers := gateAnswers(map[string]string{
		"1": pipeline.DeferralApproved,
		"2": pipeline.DeferralSkipped,
		"3": pipeline.DeferralCompleted,
		"4": pipeline.DeferralUnfixed,
		"5": "something else",
	})
	want := map[string]string{
		"1": deferredissues.GateApproved,
		"2": deferredissues.GateSkipped,
		"3": deferredissues.GateCompleted,
		"4": deferredissues.GateUnfixed,
		"5": deferredissues.GateApproved,
	}
	for id, expected := range want {
		if answers[id] != expected {
			t.Fatalf("answer %s = %q, want %q", id, answers[id], expected)
		}
	}
	if gateAnswers(nil) != nil {
		t.Fatal("no answers must stay nil")
	}
}
