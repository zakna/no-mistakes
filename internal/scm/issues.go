package scm

import (
	"context"
	"strings"
)

// IssueContent is one issue to open in a repository.
type IssueContent struct {
	// Repo is the "owner/name" (or "host/owner/name") target. It is explicit
	// rather than taken from the host's own slug because deferred finding
	// issues may be filed in a repository that is neither the upstream the
	// pipeline pushes to nor a GitHub repository at all.
	Repo   string
	Title  string
	Body   string
	Labels []string
}

// Issue is the created issue.
type Issue struct {
	URL    string
	Number string
}

// Label is a repository label that must exist before it can be applied.
type Label struct {
	Name        string
	Color       string // six hex digits, no leading '#'
	Description string
}

// IssueHost is an optional interface for providers that can open issues.
//
// It is deliberately separate from Host: issue filing is not part of the gate
// pipeline's provider contract, and a provider that cannot open issues must
// keep working with no issue support at all.
type IssueHost interface {
	// CreateIssue opens one issue. Labels that do not exist are the caller's
	// problem: use EnsureLabels first.
	CreateIssue(ctx context.Context, content IssueContent) (*Issue, error)
	// EnsureLabels creates the labels that are missing from repo and leaves the
	// ones that exist untouched, including their color and description. It
	// returns the names it could not ensure rather than an error, so a caller
	// can still file the issue without them.
	EnsureLabels(ctx context.Context, repo string, labels []Label) (missing []string, err error)
	// FindIssueByMarker returns the URL of an existing issue whose body
	// contains marker, or "" when there is none. The provider's search index
	// can lag behind issue creation, so a caller must treat an empty result as
	// "unknown", never as proof that nothing was filed.
	FindIssueByMarker(ctx context.Context, repo, marker string) (string, error)
	// CanFileIssues reports whether the authenticated user may push to repo,
	// which is the permission this pipeline requires before it opens an issue
	// in a repository the operator did not explicitly name. An error means the
	// question could not be answered and the caller must treat it as "no".
	CanFileIssues(ctx context.Context, repo string) (bool, error)
}

// IsGitHubHost reports whether an already-resolved hostname is GitHub
// (github.com, a github.com subdomain, or a GitHub Enterprise Server host the
// gh CLI is configured for). It is the host test issue filing needs: unlike
// DetectProvider it consults no SSH configuration, because the caller has
// resolved the host already.
func IsGitHubHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(stripPort(host)))
	if host == "" {
		return false
	}
	if detectHostedProvider(host) == ProviderGitHub {
		return true
	}
	if ghKnowsHost(host) {
		return true
	}
	return detectLegacyProviderHost(host) == ProviderGitHub
}
