package deferredissues

import (
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/scm/github"
)

// Target is the resolved issue repository plus why it was chosen.
type Target struct {
	// Repo is the "owner/name" (or "host/owner/name") slug, empty when nothing
	// can receive issues.
	Repo string
	// Explicit is true when the operator named this repository, which is their
	// authorization: no push-permission check is needed.
	Explicit bool
	// Reason explains an empty Repo, for the run log.
	Reason string
}

// ResolveIssueRepo picks the repository that receives deferred finding issues.
//
// The order is a fallback chain, most specific first:
//  1. the operator's explicit override for this repository;
//  2. the GitHub fork the pipeline pushes to, when there is one;
//  3. the upstream, when it is a GitHub repository.
//
// Anything else resolves to nothing, which is reported and logged rather than
// guessed. Only step 1 works for a repository whose pipeline pushes to a
// non-GitHub forge, which is exactly why the override exists.
func ResolveIssueRepo(override, forkURL, upstreamURL string, host func(string) string) Target {
	if slug := strings.TrimSpace(override); slug != "" {
		return Target{Repo: slug, Explicit: true}
	}
	if slug := gitHubSlug(forkURL, host); slug != "" {
		return Target{Repo: slug}
	}
	if slug := gitHubSlug(upstreamURL, host); slug != "" {
		return Target{Repo: slug}
	}
	return Target{Reason: "no GitHub fork or upstream for this repository; name a tracker in file_deferred_findings.issue_repos"}
}

// gitHubSlug returns the gh --repo slug for a remote that is on GitHub, or "".
// host resolves SSH host aliases; a nil host skips alias resolution.
func gitHubSlug(remoteURL string, host func(string) string) string {
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return ""
	}
	hostname := scm.ExtractHost(remoteURL)
	if host != nil {
		if resolved := strings.TrimSpace(host(remoteURL)); resolved != "" {
			hostname = resolved
		}
	}
	if !scm.IsGitHubHost(hostname) {
		return ""
	}
	return github.HostPrefixedSlugForHost(remoteURL, hostname)
}
