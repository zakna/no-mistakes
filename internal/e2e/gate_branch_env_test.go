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

const gateScanStep = types.StepName("gate.test.scan")

// TestGateBranchEnvJourney proves a repository gate command is told which
// branch it validates and which base that branch is diffed against, through a
// real daemon run: the PR targets develop (not origin/HEAD), and a fix commit
// moves HEAD past the pushed tip before the gate's re-check. The gate records
// what it saw outside the worktree so every execution can be compared.
func TestGateBranchEnvJourney(t *testing.T) {
	h := NewHarness(t, SetupOpts{Agent: "claude", Scenario: scanGateScenario(t)})
	seen := filepath.Join(t.TempDir(), "gate-env.log")

	trusted := `ignore_patterns:
  - 'vendor/**'
pr:
  base_branch: develop
gates:
  - name: scan
    after: test
    command: >-
      echo "branch=$NO_MISTAKES_BRANCH base=$NO_MISTAKES_BASE_SHA head=$(git rev-parse HEAD)" >> ` + seen + ` &&
      grep -q '^- internal/pricing$' ARCHITECTURE.md
auto_fix:
  gates:
    scan: 2
`
	h.CommitChange("main", "ARCHITECTURE.md", architectureRegistry, "maintainer: add the package registry")
	pushMainRepoConfig(t, h, trusted)

	ctx := context.Background()
	h.CommitChange("develop", "DEVELOP.md", "develop-only work\n", "develop: diverge from main")
	if out, err := h.runGit(ctx, h.WorkDir, "push", "origin", "develop"); err != nil {
		t.Fatalf("push origin develop: %v\n%s", err, out)
	}
	h.CommitChange("main", "MAIN.md", "main moved on\n", "main: advance past develop's fork point")
	if out, err := h.runGit(ctx, h.WorkDir, "push", "origin", "main"); err != nil {
		t.Fatalf("push origin main: %v\n%s", err, out)
	}
	developTip := h.UpstreamBranchSHA("develop")
	mainTip := h.UpstreamBranchSHA("main")

	if out, err := h.Run("init"); err != nil {
		t.Fatalf("nm init: %v\n%s", err, out)
	}

	const branch = "feature/sonar-scan"
	if out, err := h.runGit(ctx, h.WorkDir, "checkout", "-b", branch, "develop"); err != nil {
		t.Fatalf("branch off develop: %v\n%s", err, out)
	}
	h.CommitChange(branch, "internal/pricing/pricing.go", "package pricing\n\nfunc Quote() int { return 1 }\n", "add pricing package")
	h.PushToGate(branch)

	fw := h.AddWorktree(branch)
	parked := waitForStepStatus(t, h, branch, gateScanStep, types.StepStatusAwaitingApproval, 180*time.Second)
	if parked == nil {
		t.Fatalf("run never parked at %s", gateScanStep)
	}
	fixOut, err := h.RunInDir(fw, "axi", "respond", "--action", "fix", "--findings", firstFindingID(t, parked, gateScanStep))
	if err != nil {
		t.Fatalf("axi respond --action fix: %v\n%s", err, fixOut)
	}
	final := h.WaitForRun(branch, 180*time.Second)
	if final.Status != types.RunCompleted {
		t.Fatalf("run status = %s, want completed (error=%q)", final.Status, deref(final.Error))
	}

	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("gate never recorded its env: %v", err)
	}
	t.Logf("EVIDENCE develop tip=%s main tip=%s pushed head=%s", developTip, mainTip, final.HeadSHA)
	t.Logf("EVIDENCE gate executions:\n%s", raw)

	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		t.Fatalf("gate ran %d time(s), want an initial check and a re-check after the fix", len(lines))
	}
	heads := map[string]bool{}
	for _, line := range lines {
		fields := map[string]string{}
		for _, kv := range strings.Fields(line) {
			k, v, _ := strings.Cut(kv, "=")
			fields[k] = v
		}
		if fields["branch"] != branch {
			t.Errorf("gate saw NO_MISTAKES_BRANCH=%q, want %q (%s)", fields["branch"], branch, line)
		}
		if fields["base"] != developTip {
			t.Errorf("gate saw NO_MISTAKES_BASE_SHA=%q, want develop tip %s (main tip is %s) (%s)", fields["base"], developTip, mainTip, line)
		}
		heads[fields["head"]] = true
	}
	if len(heads) < 2 {
		t.Errorf("gate re-check ran at the same HEAD; the fix commit never moved HEAD: %v", lines)
	}
}

// scanGateScenario is customGatesScenario with the fix turn bound to the scan gate.
func scanGateScenario(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(customGatesScenario(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "scan-gate-scenario.yaml")
	content := strings.Replace(string(raw), `repository gate "package-registry"`, `repository gate "scan"`, 1)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
