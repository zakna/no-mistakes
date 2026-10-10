// Package deferredissues files review findings that shipped unfixed as forge
// defect issues, so no defect is lost when the review gate is approved or
// skipped.
//
// Everything here is best effort by design. A caller must be able to run the
// whole package, have every step fail, and still complete its run: the returned
// outcomes describe what happened and are written to the run log and the local
// record, never to an error that fails a step.
package deferredissues

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/types"
)

// Labels applied to every filed issue. The names, and the marker below, are the
// whole contract between this pipeline and any later triage tool: it finds work
// by label and identity by marker, and depends on nothing else in this code.
const (
	LabelDefect         = "defect"
	LabelSeverityPrefix = "severity:"
	LabelImpactPrefix   = "impact:"
)

// markerPrefix opens the hidden identity comment at the end of an issue body.
// The version lets the format change later without breaking a reader of the
// older shape.
const markerPrefix = "<!-- no-mistakes-finding: v1 "

// Impact levels recorded by the first triage.
const (
	ImpactHigh   = "high"
	ImpactMedium = "medium"
	ImpactLow    = "low"
	// ImpactUnassessed is recorded when the impact call did not produce a
	// usable answer. Filing continues: a defect with no impact is still a
	// defect, and holding the issue back would lose it.
	ImpactUnassessed = "not assessed"
)

// GateAnswer describes how a finding came to be deferred. It is copied into the
// issue so a reader knows whether a person accepted the defect or a fix round
// failed to clear it.
const (
	GateApproved  = "approved without fix"
	GateSkipped   = "review step skipped"
	GateCompleted = "completed without a gate (info only)"
	GateUnfixed   = "selected for fix, not verified fixed"
)

// Fingerprint is the identity of a finding for duplicate suppression.
//
// The line number is deliberately excluded: adding code above a defect moves
// its line without changing the defect. The issue repository is included so the
// same finding filed in two different trackers stays two records. The NUL
// separators keep concatenation unambiguous, so file "a" + text "bc" cannot
// collide with file "ab" + text "c".
func Fingerprint(issueRepo string, finding types.Finding) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(issueRepo),
		strings.TrimSpace(finding.File),
		normalizeText(finding.Description),
		strings.ToLower(strings.TrimSpace(finding.Severity)),
	}, "\x00")))
	return hex.EncodeToString(sum[:])[:32]
}

// normalizeText lowercases and collapses whitespace so cosmetic reformatting of
// the same sentence keeps one identity. A genuinely reworded finding hashes
// differently and is filed again; merging near-duplicates is triage work, not
// pipeline work.
func normalizeText(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(text)), " ")
}

// Marker is the hidden identity comment placed at the end of an issue body.
func Marker(fingerprint string) string {
	return markerPrefix + fingerprint + " -->"
}

// SeverityLabel and ImpactLabel build the label names from a finding's grade and
// its assessed impact.
func SeverityLabel(severity string) string {
	return LabelSeverityPrefix + strings.ToLower(strings.TrimSpace(severity))
}

func ImpactLabel(impact string) string {
	impact = strings.ToLower(strings.TrimSpace(impact))
	switch impact {
	case ImpactHigh, ImpactMedium, ImpactLow:
		return LabelImpactPrefix + impact
	default:
		// An unassessed impact gets no label: a tool filtering on impact must
		// not see a value the triage call never produced.
		return ""
	}
}

// Title is the issue title for a finding. It stays on one line and is bounded so
// a long description cannot produce an unreadable title.
func Title(finding types.Finding) string {
	text := strings.TrimSpace(strings.SplitN(normalizeSpaceKeepCase(finding.Description), ". ", 2)[0])
	if len(text) > 80 {
		text = strings.TrimSpace(text[:80]) + "..."
	}
	severity := strings.ToLower(strings.TrimSpace(finding.Severity))
	if severity == "" {
		severity = "finding"
	}
	if text == "" {
		text = "review finding with no description"
	}
	return fmt.Sprintf("[no-mistakes] %s: %s", severity, text)
}

func normalizeSpaceKeepCase(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// BodyInput is everything an issue body reports besides the finding itself.
type BodyInput struct {
	Finding     types.Finding
	Impact      string
	ImpactWhy   string
	GateAnswer  string
	RunID       string
	Branch      string
	HeadSHA     string
	IssueRepo   string
	Fingerprint string
}

// Body renders the issue body: the finding verbatim, the first triage, where the
// defect was found, and a triage checklist for later. The hidden marker closes
// the body so a duplicate search can find this issue again.
func Body(in BodyInput) string {
	var b strings.Builder
	b.WriteString("## Finding\n")
	b.WriteString(strings.TrimSpace(in.Finding.Description))
	b.WriteString("\n\n## First triage\n")
	fmt.Fprintf(&b, "- Severity: %s\n", strings.TrimSpace(in.Finding.Severity))
	impact := strings.TrimSpace(in.Impact)
	if impact == "" {
		impact = ImpactUnassessed
	}
	if why := strings.TrimSpace(in.ImpactWhy); why != "" {
		fmt.Fprintf(&b, "- Impact: %s - %s\n", impact, why)
	} else {
		fmt.Fprintf(&b, "- Impact: %s\n", impact)
	}
	b.WriteString("\n## Where\n")
	if location := location(in.Finding); location != "" {
		fmt.Fprintf(&b, "- Location: `%s`\n", location)
	}
	fmt.Fprintf(&b, "- Gate answer: %s\n", strings.TrimSpace(in.GateAnswer))
	fmt.Fprintf(&b, "- Run: %s\n", strings.TrimSpace(in.RunID))
	fmt.Fprintf(&b, "- Branch: %s\n", strings.TrimSpace(in.Branch))
	fmt.Fprintf(&b, "- Head commit: %s\n", strings.TrimSpace(in.HeadSHA))
	b.WriteString("\n## Triage\n")
	b.WriteString("- [ ] Confirmed\n- [ ] Priority set\n- [ ] Planned\n\nNotes:\n\n")
	b.WriteString(Marker(in.Fingerprint))
	b.WriteString("\n")
	return b.String()
}

func location(finding types.Finding) string {
	file := strings.TrimSpace(finding.File)
	if file == "" {
		return ""
	}
	if finding.Line > 0 {
		return fmt.Sprintf("%s:%d", file, finding.Line)
	}
	return file
}
