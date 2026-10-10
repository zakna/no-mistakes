package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/scm"
)

var _ scm.IssueHost = (*Host)(nil)

// issueRepoArgs scopes a command to an explicit repository. Unlike repoArgs it
// never falls back to the host's own slug: a deferred finding issue may belong
// to a repository this host does not push to, so an empty target is a caller
// bug and must fail rather than silently file somewhere else.
func issueRepoArgs(repo string) ([]string, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return nil, fmt.Errorf("no issue repository given; refusing to let gh infer one")
	}
	return []string{"--repo", repo}, nil
}

// CreateIssue opens one issue with gh. The body is passed on stdin so a finding
// description can contain anything, including quotes and newlines.
func (h *Host) CreateIssue(ctx context.Context, content scm.IssueContent) (*scm.Issue, error) {
	repoArgs, err := issueRepoArgs(content.Repo)
	if err != nil {
		return nil, err
	}
	args := append([]string{"issue", "create"}, repoArgs...)
	args = append(args, "--title", content.Title, "--body-file", "-")
	for _, label := range content.Labels {
		if name := strings.TrimSpace(label); name != "" {
			args = append(args, "--label", name)
		}
	}
	cmd := h.cmd(ctx, "gh", args...)
	cmd.Stdin = strings.NewReader(content.Body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh issue create: %s: %w", strings.TrimSpace(string(out)), err)
	}
	// gh prints the issue URL as the last non-empty line of its output.
	issue := &scm.Issue{URL: lastURLLine(string(out))}
	if issue.URL == "" {
		return nil, fmt.Errorf("gh issue create: no issue URL in output: %s", strings.TrimSpace(string(out)))
	}
	if idx := strings.LastIndex(issue.URL, "/"); idx >= 0 {
		if _, convErr := strconv.Atoi(issue.URL[idx+1:]); convErr == nil {
			issue.Number = issue.URL[idx+1:]
		}
	}
	return issue, nil
}

// lastURLLine returns the last line of out that looks like an http(s) URL.
func lastURLLine(out string) string {
	found := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			found = line
		}
	}
	return found
}

// EnsureLabels creates each missing label and leaves existing ones exactly as
// they are. `gh label create` is deliberately called WITHOUT --force: --force
// overwrites the color and description of a label the maintainer already
// curated. gh reports an existing label as an error, which is treated as
// success here.
func (h *Host) EnsureLabels(ctx context.Context, repo string, labels []scm.Label) ([]string, error) {
	repoArgs, err := issueRepoArgs(repo)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, label := range labels {
		name := strings.TrimSpace(label.Name)
		if name == "" {
			continue
		}
		args := append([]string{"label", "create", name}, repoArgs...)
		if label.Color != "" {
			args = append(args, "--color", label.Color)
		}
		if label.Description != "" {
			args = append(args, "--description", label.Description)
		}
		out, runErr := h.cmd(ctx, "gh", args...).CombinedOutput()
		if runErr == nil || labelAlreadyExists(string(out)) {
			continue
		}
		missing = append(missing, name)
	}
	return missing, nil
}

// labelAlreadyExists recognizes gh's "already exists" refusal, which means the
// label is present and usable - the outcome EnsureLabels wants.
func labelAlreadyExists(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "already exists")
}

// FindIssueByMarker searches issues in repo for a body marker. GitHub's search
// index is updated asynchronously, so an empty result means "not found in the
// index", never "never filed"; callers use the local record as their authority.
func (h *Host) FindIssueByMarker(ctx context.Context, repo, marker string) (string, error) {
	repoArgs, err := issueRepoArgs(repo)
	if err != nil {
		return "", err
	}
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return "", fmt.Errorf("no marker given")
	}
	args := append([]string{"issue", "list"}, repoArgs...)
	args = append(args, "--state", "all", "--search", marker+" in:body", "--limit", "1", "--json", "url")
	out, err := h.cmd(ctx, "gh", args...).Output()
	if err != nil {
		return "", fmt.Errorf("gh issue list: %w", err)
	}
	var items []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(out, &items); err != nil {
		return "", fmt.Errorf("decode gh issue list: %w", err)
	}
	if len(items) == 0 {
		return "", nil
	}
	return strings.TrimSpace(items[0].URL), nil
}

// CanFileIssues reports whether the authenticated user has write access to repo.
// A read that fails, or a permission value gh does not report, answers false:
// opening a public issue cannot be undone quietly, so an unanswered question
// must not be read as permission.
func (h *Host) CanFileIssues(ctx context.Context, repo string) (bool, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return false, fmt.Errorf("no issue repository given; refusing to let gh infer one")
	}
	// `gh issue *` takes --repo, but `gh repo view` takes the repository as a
	// positional argument and rejects --repo as an unknown flag.
	out, err := h.cmd(ctx, "gh", "repo", "view", repo, "--json", "viewerPermission").Output()
	if err != nil {
		// Output keeps stdout clean for the JSON decode and captures stderr on
		// the exit error, so gh's own reason reaches the recorded skip detail.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
				return false, fmt.Errorf("gh repo view: %s: %w", stderr, err)
			}
		}
		return false, fmt.Errorf("gh repo view: %w", err)
	}
	var payload struct {
		ViewerPermission string `json:"viewerPermission"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return false, fmt.Errorf("decode gh repo view: %w", err)
	}
	switch strings.ToUpper(strings.TrimSpace(payload.ViewerPermission)) {
	case "ADMIN", "MAINTAIN", "WRITE":
		return true, nil
	case "":
		return false, fmt.Errorf("gh repo view reported no viewerPermission for %s", repo)
	default:
		return false, nil
	}
}
