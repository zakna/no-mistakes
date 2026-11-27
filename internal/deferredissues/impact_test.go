package deferredissues

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/agent"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

type impactOutputAgent struct {
	result *agent.Result
}

func (a impactOutputAgent) Name() string { return "impact-output-test" }

func (a impactOutputAgent) Run(context.Context, agent.RunOpts) (*agent.Result, error) {
	return a.result, nil
}

func (impactOutputAgent) Close() error { return nil }

func TestAssessImpactReadsStructuredOutput(t *testing.T) {
	assessor := &AgentImpactAssessor{Agent: impactOutputAgent{
		result: &agent.Result{Output: json.RawMessage(`{"assessments":[{"id":"finding-1","impact":"medium","why":"affects users during retries"}]}`)},
	}}

	got, err := assessor.AssessImpact(context.Background(), []types.Finding{{ID: "finding-1"}})
	if err != nil {
		t.Fatalf("AssessImpact() error = %v", err)
	}
	if assessment := got["finding-1"]; assessment.Impact != ImpactMedium || assessment.Why != "affects users during retries" {
		t.Fatalf("assessment = %+v, want medium impact and its rationale", assessment)
	}
}
