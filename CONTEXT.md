# no-mistakes

A local git gate: a push runs a validation pipeline (review, test, document, lint, then push, PR, and CI) before the change reaches its real remote.

## Language

### Review findings

**Finding**:
One defect or concern a pipeline step reports, with a severity (`error`, `warning`, `info`), an optional file and line, and an action.
_Avoid_: issue (reserved for the forge issue), comment, problem

**Outstanding finding**:
A review finding that is still open at the review gate: no later review round verified it fixed, and no gate answer resolved it.
_Avoid_: open finding, pending finding

**Deferred finding**:
An outstanding finding the review step completes without fixing, because the gate was approved or skipped, or because only info findings remained.
_Avoid_: ignored finding, accepted finding

**Deferred finding issue**:
The forge issue filed for one deferred finding, labeled as a defect and carrying its first triage.
_Avoid_: bug report, ticket

**First triage**:
The severity and impact recorded on a deferred finding issue when it is filed. Later triage (priority, planning) happens outside the pipeline.
_Avoid_: classification, grading

### Repositories

**Upstream**:
The repository a run's PR targets (`repos.upstream_url`).

**Fork**:
The repository a run pushes its branch to when fork routing is set (`repos.fork_url`).

**Issue repository**:
The GitHub repository that receives deferred finding issues. It can differ from both the upstream and the fork, and from the forge the pipeline pushes to.
_Avoid_: tracker repo, target repo
