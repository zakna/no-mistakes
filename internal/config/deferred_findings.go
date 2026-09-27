package config

import (
	"fmt"
	"strings"
)

// Modes for file_deferred_findings.review. An empty value means "not set",
// which resolves to FileDeferredFindingsOff.
const (
	// FileDeferredFindingsOff keeps today's behavior: a deferred finding is
	// reported at the gate and on the PR, and nothing is filed anywhere.
	FileDeferredFindingsOff = "off"
	// FileDeferredFindingsGitHubIssue files one GitHub issue per deferred
	// review finding when the review step completes without fixing it.
	FileDeferredFindingsGitHubIssue = "github-issue"
)

// FileDeferredFindingsRaw is the YAML representation of the deferred-finding
// filing settings in the operator's GLOBAL config.
//
// IssueRepos is global-only on purpose. It answers "which tracker do I keep my
// defects in", which is a property of the operator rather than of the
// repository, and .no-mistakes.yaml is committed and shared with everyone
// working on that repository. Keeping the map here also lets a repository
// whose pipeline pushes to a non-GitHub forge (Bitbucket, for example) still
// track its defects on GitHub.
type FileDeferredFindingsRaw struct {
	Review string `yaml:"review"`
	// IssueRepos maps a repository's upstream remote URL to the "owner/name"
	// GitHub slug that receives its deferred finding issues.
	IssueRepos map[string]string `yaml:"issue_repos"`
}

// RepoFileDeferredFindingsRaw is the YAML representation of the same settings
// in a repository's .no-mistakes.yaml. Only the mode is honored there; see
// FileDeferredFindingsRaw for why the issue-repository map is global-only.
type RepoFileDeferredFindingsRaw struct {
	Review string `yaml:"review"`
}

// FileDeferredFindings is the resolved deferred-finding filing config.
type FileDeferredFindings struct {
	// Review is FileDeferredFindingsOff or FileDeferredFindingsGitHubIssue.
	Review string
	// IssueRepos maps upstream remote URL to "owner/name". Nil when unset.
	IssueRepos map[string]string
}

// validateFileDeferredFindingsMode fails the config closed on an unrecognized
// mode. A silently ignored typo ("github-issues") would leave every deferred
// finding unfiled while the operator believes filing is on, which is exactly
// the lost defect this feature exists to prevent.
func validateFileDeferredFindingsMode(mode string) error {
	switch normalizeFileDeferredFindingsMode(mode) {
	case "", FileDeferredFindingsOff, FileDeferredFindingsGitHubIssue:
		return nil
	}
	return fmt.Errorf("file_deferred_findings.review: %q is not valid (want %q or %q)", mode, FileDeferredFindingsOff, FileDeferredFindingsGitHubIssue)
}

func normalizeFileDeferredFindingsMode(mode string) string {
	return strings.ToLower(strings.TrimSpace(mode))
}

// validateFileDeferredFindingsRaw validates the global block, including the
// issue-repository map: an entry that is not an "owner/name" slug cannot be
// passed to gh --repo, and accepting it here would only move the failure to
// filing time where it is reported as a skipped defect.
func validateFileDeferredFindingsRaw(raw FileDeferredFindingsRaw) error {
	if err := validateFileDeferredFindingsMode(raw.Review); err != nil {
		return err
	}
	for remote, slug := range raw.IssueRepos {
		if strings.TrimSpace(remote) == "" {
			return fmt.Errorf("file_deferred_findings.issue_repos: an entry has an empty repository URL")
		}
		if !isIssueRepoSlug(slug) {
			return fmt.Errorf("file_deferred_findings.issue_repos[%q]: %q is not an \"owner/name\" GitHub slug", remote, slug)
		}
	}
	return nil
}

// isIssueRepoSlug reports whether value is an "owner/name" pair, optionally
// host-prefixed as "host/owner/name" (the form gh --repo needs for GitHub
// Enterprise Server).
func isIssueRepoSlug(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) != 2 && len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return false
		}
	}
	return true
}

// resolveFileDeferredFindings merges the global block with the repository's
// mode. The mode follows the same global-then-repo overlay as auto_fix: a
// repository may switch filing on or off for itself, and an unset value at
// both levels leaves the feature off.
func resolveFileDeferredFindings(global FileDeferredFindingsRaw, repo RepoFileDeferredFindingsRaw) FileDeferredFindings {
	resolved := FileDeferredFindings{Review: FileDeferredFindingsOff}
	if mode := normalizeFileDeferredFindingsMode(global.Review); mode != "" {
		resolved.Review = mode
	}
	if mode := normalizeFileDeferredFindingsMode(repo.Review); mode != "" {
		resolved.Review = mode
	}
	if len(global.IssueRepos) > 0 {
		resolved.IssueRepos = make(map[string]string, len(global.IssueRepos))
		for remote, slug := range global.IssueRepos {
			resolved.IssueRepos[strings.TrimSpace(remote)] = strings.TrimSpace(slug)
		}
	}
	return resolved
}

// FilesDeferredReviewFindings reports whether a completed review step must file
// its unfixed findings as issues.
func (c *Config) FilesDeferredReviewFindings() bool {
	if c == nil {
		return false
	}
	return c.FileDeferredFindings.Review == FileDeferredFindingsGitHubIssue
}

// IssueRepoOverrideFor returns the operator's explicit issue repository for a
// repository's upstream remote URL, or "" when none is configured. Matching is
// exact after trimming, plus a case-insensitive retry so a host typed in
// another case still matches.
func (c *Config) IssueRepoOverrideFor(remoteURL string) string {
	if c == nil || len(c.FileDeferredFindings.IssueRepos) == 0 {
		return ""
	}
	remote := strings.TrimSpace(remoteURL)
	if slug, ok := c.FileDeferredFindings.IssueRepos[remote]; ok {
		return slug
	}
	for candidate, slug := range c.FileDeferredFindings.IssueRepos {
		if strings.EqualFold(candidate, remote) {
			return slug
		}
	}
	return ""
}

// resolveBoolOverride returns the repository value when set, else the global
// value when set, else false.
func resolveBoolOverride(global, repo *bool) bool {
	if repo != nil {
		return *repo
	}
	if global != nil {
		return *global
	}
	return false
}
