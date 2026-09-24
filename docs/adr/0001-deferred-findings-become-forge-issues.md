---
status: accepted
---

# Deferred review findings are filed as forge issues, not carried in the pipeline

A review finding that ships unfixed used to leave no durable record outside the run's own database rows, so a warning approved at the gate was lost in practice. We file one GitHub issue per deferred finding when the review step completes (approve, skip, or a completion that leaves only info findings), with a first triage of severity and impact, and we treat filing as best effort: it never fails, blocks, or delays a run.

## Considered Options

Filing at defer time was rejected: at that moment the operator can still answer `fix`, so an issue created then is wrong for every finding that is later fixed, and closing it again adds a second failure path.

Carrying the findings forward inside no-mistakes (a backlog of its own) was rejected because the forge already owns issue triage, and because a local backlog is invisible to everyone who is not running the daemon.

## Consequences

The pipeline gains a dependency on issue creation, so every error path must degrade to "logged, not filed". The issue body carries a versioned fingerprint marker, which makes the issue shape a contract with any later triage tool.
