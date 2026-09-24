package config

import "testing"

func TestFileDeferredFindings_UnsetIsOff(t *testing.T) {
	cfg := Merge(&GlobalConfig{}, &RepoConfig{})
	if cfg.FilesDeferredReviewFindings() {
		t.Fatal("filing must be off when no config sets it")
	}
	if cfg.FileDeferredFindings.Review != FileDeferredFindingsOff {
		t.Fatalf("resolved mode = %q, want %q", cfg.FileDeferredFindings.Review, FileDeferredFindingsOff)
	}
}

func TestFileDeferredFindings_GlobalEnablesAndRepoOverrides(t *testing.T) {
	global := &GlobalConfig{FileDeferredFindings: FileDeferredFindingsRaw{Review: FileDeferredFindingsGitHubIssue}}
	if cfg := Merge(global, &RepoConfig{}); !cfg.FilesDeferredReviewFindings() {
		t.Fatal("global github-issue must enable filing")
	}
	repoOff := &RepoConfig{FileDeferredFindings: RepoFileDeferredFindingsRaw{Review: FileDeferredFindingsOff}}
	if cfg := Merge(global, repoOff); cfg.FilesDeferredReviewFindings() {
		t.Fatal("a repository must be able to switch filing off against a global default")
	}
	repoOn := &RepoConfig{FileDeferredFindings: RepoFileDeferredFindingsRaw{Review: FileDeferredFindingsGitHubIssue}}
	if cfg := Merge(&GlobalConfig{}, repoOn); !cfg.FilesDeferredReviewFindings() {
		t.Fatal("a repository must be able to switch filing on with no global value")
	}
}

func TestFileDeferredFindings_UnknownValueFailsTheConfig(t *testing.T) {
	if _, err := LoadGlobalFromBytes([]byte("file_deferred_findings:\n  review: github-issues\n")); err == nil {
		t.Fatal("an unknown global mode must fail the config closed")
	}
	if _, err := parseRepoConfig([]byte("file_deferred_findings:\n  review: yes\n")); err == nil {
		t.Fatal("an unknown repo mode must fail the config closed")
	}
}

func TestFileDeferredFindings_IssueReposAreGlobalOnlyAndValidated(t *testing.T) {
	global, err := LoadGlobalFromBytes([]byte(`
file_deferred_findings:
  review: github-issue
  issue_repos:
    git@bitbucket.org:team/actor.git: zakna/ACTOR-ui-tests
`))
	if err != nil {
		t.Fatalf("load global: %v", err)
	}
	cfg := Merge(global, &RepoConfig{})
	if got := cfg.IssueRepoOverrideFor("git@bitbucket.org:team/actor.git"); got != "zakna/ACTOR-ui-tests" {
		t.Fatalf("override = %q, want zakna/ACTOR-ui-tests", got)
	}
	if got := cfg.IssueRepoOverrideFor("git@github.com:someone/other.git"); got != "" {
		t.Fatalf("unrelated remote returned %q, want empty", got)
	}

	// A repository cannot name its own tracker: the key does not exist there.
	repo, err := parseRepoConfig([]byte("file_deferred_findings:\n  issue_repos:\n    a: b/c\n"))
	if err != nil {
		t.Fatalf("parse repo config: %v", err)
	}
	if cfg := Merge(&GlobalConfig{}, repo); cfg.IssueRepoOverrideFor("a") != "" {
		t.Fatal("a pushed repo config must not be able to name the issue repository")
	}

	if _, err := LoadGlobalFromBytes([]byte("file_deferred_findings:\n  issue_repos:\n    remote: not-a-slug\n")); err == nil {
		t.Fatal("a malformed slug must fail the config closed")
	}
}

func TestReviewRequireErrorsFixed_DefaultsOffAndOverlays(t *testing.T) {
	if cfg := Merge(&GlobalConfig{}, &RepoConfig{}); cfg.Review.RequireErrorsFixed {
		t.Fatal("the gate requirement must default to off")
	}
	yes, no := true, false
	global := &GlobalConfig{Review: GlobalReviewRaw{RequireErrorsFixed: &yes}}
	if cfg := Merge(global, &RepoConfig{}); !cfg.Review.RequireErrorsFixed {
		t.Fatal("a global value must apply")
	}
	repo := &RepoConfig{Review: ReviewRaw{RequireErrorsFixed: &no}}
	if cfg := Merge(global, repo); cfg.Review.RequireErrorsFixed {
		t.Fatal("a trusted repository value must win over the global value")
	}
}

func TestReviewRequireErrorsFixed_IsTrustedDefaultBranchOnly(t *testing.T) {
	yes := true
	pushed := &RepoConfig{Review: ReviewRaw{RequireErrorsFixed: &yes}}
	// A pushed branch cannot turn the requirement ON or OFF: the whole review
	// block comes from the trusted copy.
	effective := EffectiveRepoConfig(pushed, &RepoConfig{}, false)
	if effective.Review.RequireErrorsFixed != nil {
		t.Fatal("a pushed review block must not reach the effective config")
	}
	trusted := &RepoConfig{Review: ReviewRaw{RequireErrorsFixed: &yes}}
	noValue := false
	pushedOff := &RepoConfig{Review: ReviewRaw{RequireErrorsFixed: &noValue}}
	effective = EffectiveRepoConfig(pushedOff, trusted, true)
	if effective.Review.RequireErrorsFixed == nil || !*effective.Review.RequireErrorsFixed {
		t.Fatal("the trusted requirement must survive a pushed branch that switches it off, even with the commands opt-in")
	}
}
