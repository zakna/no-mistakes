package deferredissues

import "testing"

func TestResolveIssueRepo_OverrideWinsAndCarriesItsOwnAuthorization(t *testing.T) {
	target := ResolveIssueRepo("zakna/tracker", "git@github.com:zakna/no-mistakes.git", "git@github.com:kunchenguid/no-mistakes.git", nil)
	if target.Repo != "zakna/tracker" {
		t.Fatalf("repo = %q, want the override", target.Repo)
	}
	if !target.Explicit {
		t.Fatal("an operator-named tracker is their own authorization")
	}
}

func TestResolveIssueRepo_ForkBeforeUpstream(t *testing.T) {
	target := ResolveIssueRepo("", "git@github.com:zakna/no-mistakes.git", "git@github.com:kunchenguid/no-mistakes.git", nil)
	if target.Repo != "zakna/no-mistakes" {
		t.Fatalf("repo = %q, want the fork", target.Repo)
	}
	if target.Explicit {
		t.Fatal("a resolved fork still needs the permission check")
	}
}

func TestResolveIssueRepo_UpstreamWhenThereIsNoFork(t *testing.T) {
	target := ResolveIssueRepo("", "", "https://github.com/olivier/firstmate-work.git", nil)
	if target.Repo != "olivier/firstmate-work" {
		t.Fatalf("repo = %q, want the upstream", target.Repo)
	}
}

func TestResolveIssueRepo_NonGitHubRemotesResolveToNothing(t *testing.T) {
	// The ACTOR case: the pipeline pushes to Bitbucket, so no slug can be
	// guessed. Only the override can name a tracker.
	target := ResolveIssueRepo("", "", "git@bitbucket.org:team/actor.git", nil)
	if target.Repo != "" {
		t.Fatalf("repo = %q, want nothing", target.Repo)
	}
	if target.Reason == "" {
		t.Fatal("an empty target must say why")
	}
}

func TestResolveIssueRepo_SSHAliasIsResolvedToItsRealHost(t *testing.T) {
	alias := func(string) string { return "github.com" }
	target := ResolveIssueRepo("", "git@gh-work:zakna/no-mistakes.git", "", alias)
	if target.Repo != "zakna/no-mistakes" {
		t.Fatalf("repo = %q, want the alias resolved to GitHub", target.Repo)
	}
}

func TestResolveIssueRepo_NoRemotesAtAllResolvesToNothing(t *testing.T) {
	if target := ResolveIssueRepo("", "", "", nil); target.Repo != "" || target.Reason == "" {
		t.Fatalf("target = %+v, want nothing with a reason", target)
	}
}
