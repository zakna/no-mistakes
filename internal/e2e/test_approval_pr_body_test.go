//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/types"
)

const sandboxBlockedSummary = "The live check could not run from the sandbox."

func writeInconclusiveTestScenario(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inconclusive-test-scenario.yaml")
	content := `actions:
  - match: "You are validating a code change by driving the product itself."
    text: "live check blocked"
    structured:
      findings: []
      summary: "live check blocked"
      tested: ["fakeagent: attempted host probe"]
      testing_summary: "` + sandboxBlockedSummary + `"
      artifacts: []
      verdict: inconclusive
      scenarios:
        - name: "host answers the read-only probe"
          result: untested
          live: false
          evidence: "fakeagent: not driven"
          reason: "no host access from the sandbox"
  - match: "Draft a pull request title and summary for the full branch delta."
    text: "pr summary"
    structured:
      title: "feat: add host probe"
      body: |
        ## What Changed

        - Add the host probe.
  - text: "no issues found"
    structured:
      findings: []
      summary: "no issues found"
      risk_level: low
      risk_rationale: "synthetic clean response"
      risk_scope: source-or-external
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestTestApprovalReasonPublishedInPRBody drives the real CLI, daemon, and
// gate: Test parks on an inconclusive verdict, the operator approves it with
// `axi respond --reason`, and the GitHub PR create call receives the body.
func TestTestApprovalReasonPublishedInPRBody(t *testing.T) {
	for _, tc := range []struct {
		name, reason, wantLine string
		forbid                 []string
	}{
		{
			name:     "reason",
			reason:   "Ran the read-only host verification by hand; all checks passed",
			wantLine: "- Approved with exception: Ran the read-only host verification by hand; all checks passed\n",
		},
		{
			// Adversarial: a multi-line reason with markup must stay one
			// escaped bullet and never inject raw HTML or a new heading.
			name:     "markup-reason",
			reason:   "verified by hand\n## Injected heading\n<script>alert(1)</script>",
			wantLine: "- Approved with exception: verified by hand ## Injected heading ",
			forbid:   []string{"\n## Injected heading", "<script>"},
		},
		{
			name:   "no-reason",
			forbid: []string{"Approved with", "recorded before this approval"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHarness(t, SetupOpts{Agent: "claude", Scenario: writeInconclusiveTestScenario(t)})
			ctx := context.Background()
			parentURL := "https://github.com/example/no-mistakes.git"
			forkURL := "https://github.com/example-fork/no-mistakes.git"
			forkDir := filepath.Join(filepath.Dir(h.UpstreamDir), "fork.git")
			if err := os.MkdirAll(forkDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if out, err := h.runGit(ctx, forkDir, "init", "--bare", "--initial-branch=main"); err != nil {
				t.Fatalf("init fork: %v\n%s", err, out)
			}
			if out, err := h.runGit(ctx, h.WorkDir, "push", forkDir, "main"); err != nil {
				t.Fatalf("seed fork: %v\n%s", err, out)
			}
			configureGitURLRewrite(t, h, parentURL, h.UpstreamDir)
			configureGitURLRewrite(t, h, forkURL, forkDir)
			if out, err := h.runGit(ctx, h.WorkDir, "remote", "set-url", "origin", parentURL); err != nil {
				t.Fatalf("set origin: %v\n%s", err, out)
			}
			ghLog := filepath.Join(filepath.Dir(h.AgentLog), "gh-approval-reason.log")
			t.Setenv("FAKEAGENT_GH_MODE", "fork-pr")
			t.Setenv("FAKEAGENT_GH_LOG", ghLog)
			t.Setenv("FAKEAGENT_GH_PARENT", "example/no-mistakes")
			if out, err := h.Run("init", "--fork-url", forkURL); err != nil {
				t.Fatalf("init: %v\n%s", err, out)
			}

			branch := "feature/approval-" + tc.name
			h.CommitChange(branch, "probe.go", "package probe\n", "add host probe")
			h.PushToGate(branch)
			waitForStepStatus(t, h, branch, types.StepTest, types.StepStatusAwaitingApproval, 60*time.Second)
			args := []string{"axi", "respond", "--step", "test", "--action", "approve"}
			if tc.reason != "" {
				args = append(args, "--reason", tc.reason)
			}
			if out, err := h.Run(args...); err != nil {
				t.Fatalf("respond: %v\n%s", err, out)
			}
			run := h.WaitForRun(branch, 90*time.Second)
			if run.Status != types.RunCompleted {
				t.Fatalf("run status = %s (error=%v)", run.Status, run.Error)
			}

			body := createdPRBody(t, readGHStubInvocations(t, ghLog))
			if dir := os.Getenv("NM_E2E_EVIDENCE_DIR"); dir != "" {
				_ = os.WriteFile(filepath.Join(dir, "pr-body-"+tc.name+".md"), []byte(body), 0o644)
			}
			start := strings.Index(body, "## Testing\n")
			if start < 0 {
				t.Fatalf("no Testing section:\n%s", body)
			}
			section := body[start:]
			if next := strings.Index(section[len("## Testing\n"):], "\n## "); next >= 0 {
				section = section[:len("## Testing\n")+next]
			}
			for _, bad := range tc.forbid {
				if strings.Contains(section, bad) {
					t.Fatalf("Testing section contains %q:\n%s", bad, section)
				}
			}
			if tc.reason == "" {
				if !strings.Contains(section, sandboxBlockedSummary) {
					t.Fatalf("control run lost the agent summary:\n%s", section)
				}
				return
			}
			if !strings.HasPrefix(section, "## Testing\n\n"+tc.wantLine[:len(tc.wantLine)]) {
				t.Fatalf("approval reason must lead the Testing section, want %q:\n%s", tc.wantLine, section)
			}
			caveat := "- The summary and scenario results below were recorded before this approval.\n\n" + sandboxBlockedSummary
			if !strings.Contains(section, caveat) {
				t.Fatalf("pre-approval summary must follow the caveat as its own paragraph:\n%s", section)
			}
		})
	}
}
