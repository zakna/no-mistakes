package deferredissues

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/agent"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

// impactSchema is the only shape the impact turn may answer with. Keeping the
// answer typed is what stops a model from writing prose straight into an issue
// body: every value below is copied into the issue, so the schema bounds what
// can appear there.
const impactSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["assessments"],
  "properties": {
    "assessments": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "impact", "why"],
        "properties": {
          "id": {"type": "string"},
          "impact": {"type": "string", "enum": ["high", "medium", "low"]},
          "why": {"type": "string", "maxLength": 240}
        }
      }
    }
  }
}`

// AgentImpactAssessor asks one short agent turn to grade the impact of the
// findings about to be filed.
//
// It is a separate call rather than an extra field in the review schema on
// purpose: the review schema is shared with every adapter, with eval replay, and
// with recorded fixtures, so a new field there would change every run, including
// runs with filing switched off. See docs/adr/0002.
type AgentImpactAssessor struct {
	Agent agent.Agent
	// CWD is the run worktree, so the turn can read the named files.
	CWD string
	// Env carries invocation-scoped environment entries, as other steps pass them.
	Env []string
}

// AssessImpact returns one assessment per finding, keyed by finding ID.
func (a *AgentImpactAssessor) AssessImpact(ctx context.Context, findings []types.Finding) (map[string]Assessment, error) {
	if a == nil || a.Agent == nil || len(findings) == 0 {
		return nil, nil
	}
	result, err := a.Agent.Run(ctx, agent.RunOpts{
		Prompt:     impactPrompt(findings),
		CWD:        a.CWD,
		Env:        a.Env,
		JSONSchema: json.RawMessage(impactSchema),
		Purpose:    "deferred-issue-impact",
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("impact turn returned no result")
	}
	var payload struct {
		Assessments []struct {
			ID     string `json:"id"`
			Impact string `json:"impact"`
			Why    string `json:"why"`
		} `json:"assessments"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Text)), &payload); err != nil {
		return nil, fmt.Errorf("decode impact assessment: %w", err)
	}
	assessments := make(map[string]Assessment, len(payload.Assessments))
	for _, item := range payload.Assessments {
		assessments[strings.TrimSpace(item.ID)] = Assessment{
			Impact: strings.ToLower(strings.TrimSpace(item.Impact)),
			Why:    strings.TrimSpace(item.Why),
		}
	}
	return assessments, nil
}

// impactPrompt asks for a judgement and nothing else. The turn must not edit,
// commit, or run anything: the code under review already shipped, and this call
// exists only to put a first triage on an issue.
func impactPrompt(findings []types.Finding) string {
	var b strings.Builder
	b.WriteString(`You are triaging defects that a code review reported and that were NOT fixed.
They are about to be filed as issues for later triage.

For EACH finding, judge the IMPACT if the defect stays unfixed:
- high: users or data are affected, or the product can fail in normal use.
- medium: a real problem with a workaround, or it affects a narrow path.
- low: cosmetic, stylistic, or internal only.

Rules:
- Judge impact, not how hard the fix is, and not the severity you are given.
- "why" is at most two short sentences naming who or what is affected and when.
- Read the named file when it helps. Change NOTHING: no edits, no commits, no
  commands that modify the repository, no new files.
- Answer for every id, with the id exactly as given.

Findings:
`)
	for _, finding := range findings {
		fmt.Fprintf(&b, "\n- id: %s\n  severity: %s\n", finding.ID, strings.TrimSpace(finding.Severity))
		if loc := location(finding); loc != "" {
			fmt.Fprintf(&b, "  location: %s\n", loc)
		}
		fmt.Fprintf(&b, "  finding: %s\n", normalizeSpaceKeepCase(finding.Description))
	}
	return b.String()
}
