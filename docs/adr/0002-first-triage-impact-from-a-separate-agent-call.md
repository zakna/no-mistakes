---
status: accepted
---

# Impact for a first triage comes from a separate agent call, not the review schema

A deferred finding issue records severity (the reviewer's own grade) and impact. Impact is produced by one short, schema-validated agent call per filing batch, made only when there is something to file, and a failure files the issue with "impact: not assessed".

## Considered Options

Adding an `impact` field to the review findings schema would judge impact with full review context and cost no extra call. It was rejected here because that schema is shared by every adapter, by eval replay, by recorded fixtures, and by the fake agent: the change would alter every run, including runs with this feature off, and would be a permanent divergence this fork pays for at each upstream merge.

## Consequences

Impact is judged with less context than the reviewer had. The call is one small function behind a seam, so it can be replaced by a reviewer-schema field later without touching the filing logic.
