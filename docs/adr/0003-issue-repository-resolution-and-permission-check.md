---
status: accepted
---

# The issue repository is resolved by a fallback chain, and an unowned upstream is never filed to

Deferred finding issues go to the first match of: an explicit override from the operator's global config (keyed by upstream URL), the GitHub fork, then the upstream when it is on GitHub. Anything else is skipped and logged. Before filing to an upstream, the authenticated user's push permission is checked, and a check that cannot answer counts as no permission.

## Considered Options

A repository-config override was rejected for the override's home: the tracker is a personal choice, and `.no-mistakes.yaml` is committed and shared with everyone working on the repository. The global map also lets a Bitbucket-pushed repository track its defects on GitHub.

Filing to the upstream unconditionally was rejected: a contributor's run would open issues on a maintainer's repository.

## Consequences

Issue creation is a side effect that cannot be undone quietly, so the permission check runs before it and fails closed. A repository with no fork, no override, and no push permission on its upstream files nothing and says so in the log.
