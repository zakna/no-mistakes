package github

import (
	"context"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/scm"
)

// Every test here drives the real gh argument builder through the package's own
// fake gh process. No command reaches github.com, so no issue is ever created.

func issueTestHost(t *testing.T, responses map[string]githubTestResponse) *Host {
	t.Helper()
	return New(githubTestCmdFactory(responses), func() bool { return true }, "", "test/repo")
}

func TestCreateIssue_PassesTheBodyOnStdinAndReturnsTheURL(t *testing.T) {
	const body = "Finding text with \"quotes\"\nand a second line"
	host := issueTestHost(t, map[string]githubTestResponse{
		`gh issue create --repo zakna/tracker --title [no-mistakes] warning: no backoff --body-file - --label defect --label severity:warning`: {
			stdout:    "Creating issue in zakna/tracker\n\nhttps://github.com/zakna/tracker/issues/12\n",
			wantStdin: body,
		},
	})

	issue, err := host.CreateIssue(context.Background(), scm.IssueContent{
		Repo:   "zakna/tracker",
		Title:  "[no-mistakes] warning: no backoff",
		Body:   body,
		Labels: []string{"defect", "severity:warning"},
	})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if issue.URL != "https://github.com/zakna/tracker/issues/12" {
		t.Fatalf("URL = %q", issue.URL)
	}
	if issue.Number != "12" {
		t.Fatalf("Number = %q, want 12", issue.Number)
	}
}

// A finding description can itself contain a URL, so the LAST URL line is the
// issue, never the first.
func TestCreateIssue_TakesTheLastURLLine(t *testing.T) {
	host := issueTestHost(t, map[string]githubTestResponse{
		`gh issue create --repo zakna/tracker --title t --body-file -`: {
			stdout: "see https://example.com/docs/backoff\nhttps://github.com/zakna/tracker/issues/7\n",
		},
	})
	issue, err := host.CreateIssue(context.Background(), scm.IssueContent{Repo: "zakna/tracker", Title: "t", Body: "b"})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if issue.URL != "https://github.com/zakna/tracker/issues/7" {
		t.Fatalf("URL = %q, want the issue URL", issue.URL)
	}
}

func TestCreateIssue_NoURLInOutputIsAnError(t *testing.T) {
	host := issueTestHost(t, map[string]githubTestResponse{
		`gh issue create --repo zakna/tracker --title t --body-file -`: {stdout: "done\n"},
	})
	if _, err := host.CreateIssue(context.Background(), scm.IssueContent{Repo: "zakna/tracker", Title: "t", Body: "b"}); err == nil {
		t.Fatal("an output with no issue URL must be an error")
	}
}

// An empty target must never let gh infer a repository from the working
// directory: the issue would land somewhere nobody asked for.
func TestIssueCalls_RefuseAnEmptyTargetRepository(t *testing.T) {
	host := issueTestHost(t, nil)
	ctx := context.Background()

	if _, err := host.CreateIssue(ctx, scm.IssueContent{Title: "t", Body: "b"}); err == nil {
		t.Fatal("CreateIssue with no repo must fail")
	}
	if _, err := host.EnsureLabels(ctx, "", []scm.Label{{Name: "defect"}}); err == nil {
		t.Fatal("EnsureLabels with no repo must fail")
	}
	if _, err := host.FindIssueByMarker(ctx, "", "marker"); err == nil {
		t.Fatal("FindIssueByMarker with no repo must fail")
	}
	if ok, err := host.CanFileIssues(ctx, ""); ok || err == nil {
		t.Fatal("CanFileIssues with no repo must fail")
	}
}

func TestEnsureLabels_ExistingLabelIsSuccessAndIsNotOverwritten(t *testing.T) {
	create := `gh label create defect --repo zakna/tracker --color d73a4a --description Reported by no-mistakes`
	host := issueTestHost(t, map[string]githubTestResponse{
		create: {stderr: "HTTP 422: Validation Failed (label already exists)", code: 1},
	})
	if strings.Contains(create, "--force") {
		t.Fatal("gh label create must not use --force: it overwrites a curated label")
	}

	missing, err := host.EnsureLabels(context.Background(), "zakna/tracker",
		[]scm.Label{{Name: "defect", Color: "d73a4a", Description: "Reported by no-mistakes"}})
	if err != nil {
		t.Fatalf("EnsureLabels() error = %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none: the label is present and usable", missing)
	}
}

func TestEnsureLabels_ReportsOnlyTheLabelsItCouldNotEnsure(t *testing.T) {
	host := issueTestHost(t, map[string]githubTestResponse{
		`gh label create defect --repo zakna/tracker`:      {stdout: "created\n"},
		`gh label create impact:high --repo zakna/tracker`: {stderr: "HTTP 403: Forbidden", code: 1},
	})
	missing, err := host.EnsureLabels(context.Background(), "zakna/tracker",
		[]scm.Label{{Name: "defect"}, {Name: "impact:high"}})
	if err != nil {
		t.Fatalf("EnsureLabels() error = %v", err)
	}
	if len(missing) != 1 || missing[0] != "impact:high" {
		t.Fatalf("missing = %v, want only impact:high", missing)
	}
}

func TestFindIssueByMarker_SearchesBodiesAndReportsAMiss(t *testing.T) {
	const marker = "no-mistakes-finding: v1 abc123"
	cmd := `gh issue list --repo zakna/tracker --state all --search ` + marker + ` in:body --limit 1 --json url`
	host := issueTestHost(t, map[string]githubTestResponse{
		cmd: {stdout: `[{"url":"https://github.com/zakna/tracker/issues/3"}]`},
	})
	url, err := host.FindIssueByMarker(context.Background(), "zakna/tracker", marker)
	if err != nil {
		t.Fatalf("FindIssueByMarker() error = %v", err)
	}
	if url != "https://github.com/zakna/tracker/issues/3" {
		t.Fatalf("url = %q", url)
	}

	// An empty result is "not in the search index", not an error: the local
	// record is the authority on what was filed.
	miss := issueTestHost(t, map[string]githubTestResponse{cmd: {stdout: `[]`}})
	url, err = miss.FindIssueByMarker(context.Background(), "zakna/tracker", marker)
	if err != nil || url != "" {
		t.Fatalf("url = %q, err = %v, want an empty miss", url, err)
	}
}

func TestCanFileIssues_OnlyWriteLevelPermissionsAnswerYes(t *testing.T) {
	cmd := `gh repo view --repo zakna/tracker --json viewerPermission`
	cases := []struct {
		permission string
		want       bool
	}{
		{"ADMIN", true}, {"MAINTAIN", true}, {"WRITE", true},
		{"TRIAGE", false}, {"READ", false}, {"NONE", false},
	}
	for _, tc := range cases {
		host := issueTestHost(t, map[string]githubTestResponse{
			cmd: {stdout: `{"viewerPermission":"` + tc.permission + `"}`},
		})
		got, err := host.CanFileIssues(context.Background(), "zakna/tracker")
		if err != nil {
			t.Fatalf("CanFileIssues(%s) error = %v", tc.permission, err)
		}
		if got != tc.want {
			t.Fatalf("CanFileIssues(%s) = %v, want %v", tc.permission, got, tc.want)
		}
	}
}

// An unanswered permission question must never read as permission.
func TestCanFileIssues_FailsClosed(t *testing.T) {
	cmd := `gh repo view --repo zakna/tracker --json viewerPermission`
	for name, response := range map[string]githubTestResponse{
		"command failed":  {stderr: "gh: not logged in", code: 1},
		"unreadable json": {stdout: "not json"},
		"no permission":   {stdout: `{"viewerPermission":""}`},
	} {
		host := issueTestHost(t, map[string]githubTestResponse{cmd: response})
		ok, err := host.CanFileIssues(context.Background(), "zakna/tracker")
		if ok {
			t.Fatalf("%s: CanFileIssues = true, want false", name)
		}
		if err == nil {
			t.Fatalf("%s: error = nil, want the reason reported", name)
		}
	}
}
