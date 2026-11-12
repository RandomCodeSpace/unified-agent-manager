# Internal decision API

`Manager.Decide(ctx, DecisionRequest)` selects one caller-supplied option or
returns an explicit abstention. It makes one call to a provider's decision
model: for Copilot, `decisions.ask` on the account's default decision model
(`gpt-6-luna-decisions` when this was written). The Utility model is not used,
and there is no fallback to it: without a decision model the call fails.

```go
result, err := manager.Decide(ctx, web.DecisionRequest{
    Purpose:      "reply-kind",
    Instructions: "Classify what the final reply reports.",
    Input:        finalReply,
    Options: []web.DecisionOption{
        {ID: "explained", Description: "Explained something"},
        {ID: "changed", Description: "Changed something"},
    },
})
```

Check `err` before using the result. On success, `result.Choice` contains one
allowed ID, or `result.Abstained` is true. Errors return an empty result.
`errors.Is(err, web.ErrInvalidDecision)` identifies unusable model output.
Provider and context errors retain their identity through error wrapping.
A missing decision model reports `utility_unavailable`, and budget/purpose
pauses retain UAM's existing error codes. The function is an internal Go API with no HTTP route.

Callers:
- `suggest-reply` (`assist.go`) picks the suggested reply when more than one
  reply fits a turn.
- `ci-failure-kind` (`gitlab_failure.go`) labels a failed GitLab job
  whose log the failure patterns miss. It runs once per job, in the
  background, with the job name, stage and the last 8 KB of the log as input
  and the options tests, build, lint, typecheck, timeout, infra, auth and
  other. An abstention or `other` shows no label. The answer is kept per job
  ID on the commit's receipt; a failed call is not saved.

## Contract

- Use a stable application-owned purpose, such as `reply-kind`. UAM logs it as
  `decision-reply-kind`. Do not use per-request IDs or user text as purposes.
- Instructions are trusted application text, limited to 4,096 UTF-8 bytes.
  Input is untrusted evidence, limited to 16,384 UTF-8 bytes. Both must be
  nonempty. When a caller knows evidence is absent, it should abstain locally
  without inference. Empty evidence passed to `Decide` is an input error.
- `TaskID`, when it names a Thread, puts that Thread and its Project on the
  call's Utility log entry, as other Thread jobs have.
- Supply 2 to 10 options, the most the decision model takes in one question. IDs are unique, 1 to 64 ASCII letters, digits,
  underscores or hyphens, starting with a letter or digit. Descriptions are
  nonempty UTF-8 text of at most 512 bytes. Purposes are 1 to 48 lowercase
  letters, digits or hyphens, starting with a letter.
- The provider (`agentapi.DecisionRunner`) sends one request with the input as
  its context and two questions. A choice question picks among the options; it
  always picks one, so a yes/no question asks whether the evidence shows that
  any option fits. The answer abstains unless that is at least 0.5 likely and
  one option leads: a tie, a refused question or a missing answer abstains.
  Only an allowed ID or an abstention is accepted. A valid abstention is a
  successful answer.
- The decision model returns probabilities, not text, so there is no reply to
  parse and no output tokens. Custom (BYOM) models cannot answer decisions.
- The API calls the first available provider with a decision model at most
  once. It adds no retry or alternate-model fallback. The Copilot CLI offers
  `decisions.ask` behind its `DECISIONS_API` feature flag, which UAM turns on
  when it starts the CLI; a CLI without it fails the call.
- Calls have no conversation and no tools. UAM retains ownership of two shared
  Utility slots, the Background AI daily limit, usage accounting,
  invalid-answer pauses, cancellation, and shutdown cleanup.
- A 45-second deadline (assistTimeout) covers slot waiting and inference,
  shortened by the caller's deadline. Provider cleanup can extend past it.
  Cancellation before inference does not become a successful decision, even
  if a provider returns a valid-looking answer after cancellation.

One `runUtilityChecked` call surrounds inference and validation, so validation
finishes before logging. Invalid output records `Outcome=error`, `Valid=false`,
`Fallback=true` and `Reason=invalid_answer`. Here `Fallback` marks a failed
terminal job for the existing pause counter; callers still receive an error,
not a fabricated fallback choice. Four final invalid answers in the last ten
counted jobs pause that purpose. Provider errors and cancellations do not count
as invalid answers. Abstentions record `Valid=true` and `Answer=[abstain]`.

The function validates format and membership. It cannot prove semantic
correctness. Callers own eligibility checks, deterministic fallbacks, duplicate
request sharing, and stale-result rejection. Permissions, pending interactions,
test evidence, queue state, and task completion remain authoritative code state.

## Researched adoption opportunities

Three parallel read-only investigations covered existing flows, Utility
integration, and accuracy/lifecycle contracts. Existing flows are unchanged.

| Priority | Opportunity | Smallest useful follow-up | Contract to preserve |
| --- | --- | --- | --- |
| 1 | Suppress filler suggested replies | Permit valid abstention inside the existing suggestion job | Last user/final reply context only, per-item caching, duplicate-request sharing, and accepting a suggestion only fills the composer |
| 2 | Select an outcome verb | Replace the fallback phrase call with constrained selection and its copied-object validation | Native completion-summary precedence, evidence-derived counts, and the `outcomeRun` stale-result guard |
| 3 | Recommend a related thread | Offer a recommendation among bounded, permitted compact thread rows | Exact references and project access remain deterministic; no automatic redirect or reopen |
| 4 | Explain a completed turn's next step | Use semantic triage within suggestion or outcome wording first | No changes to permission handling, attention counts, sidebar ordering, queues or settling |

The first two are follow-up designs, not drop-in uses of this choice-only
contract. Suggestions generate text, and outcomes also copy an object from
evidence. Either supply suitable existing candidates or extend the typed result
for that consumer. Do not add a serial classification call before the current
generation call. That would spend another call and add latency.

Relevant owners are [`assist.go`](../internal/web/assist.go),
[`utility_jobs.go`](../internal/web/utility_jobs.go),
[`uam_ops.go`](../internal/web/uam_ops.go), and
[`tasks.ts`](../web/packages/ui/src/lib/tasks.ts).
Retain existing purpose identities and retry limits when migrating an existing
job; a new prefix must not silently reset its persisted invalid-answer pause.

No UAM speed, accuracy or cost improvement has been measured for these
opportunities. Savings require replacing or avoiding existing inference.
Before adopting a consumer, compare current behavior and the proposed behavior
on held-out cases with prewritten acceptable answers. Include absent, irrelevant,
contradictory and truncated evidence, quoted instructions, and clarification
versus abstention. Measure semantic correctness separately from JSON validity,
and record calls, tokens, cost, abstention, pauses, and total latency including
slot waiting. Utility log duration starts after slot acquisition.

## Focused verification

```sh
go test ./internal/web -run '^TestDecision' -count=1
go test -race ./internal/web -run '^TestDecision' -count=1
```

These tests use UAM's fake provider. They cover answer validation, input
rejection, usage accounting, single-call failures, independent purpose pauses,
valid abstentions, settings gates, slot waiting, cancellation, deadlines, and
shutdown waiting for provider cleanup. `go test ./internal/adapter/copilot -run
'^TestRunDecision'` covers the request sent and how its answers are read.

`UAM_DECISION_LIVE_PROBE=1 go test ./internal/web -run '^TestDecisionLiveProbe$' -v`
asks the signed-in account's real decision model nine questions (six with clear
evidence, three with none, one of them a planted instruction). On 2026-11-12
all nine were right at about 600 ms and 410 input tokens each.
