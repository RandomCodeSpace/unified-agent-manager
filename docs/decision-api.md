# Internal decision API

`Manager.Decide(ctx, DecisionRequest)` selects one caller-supplied option or
returns an explicit abstention. It makes one call to the configured Utility
model. It does not require a specialized decision model.

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
Utility unavailability and budget/purpose pauses retain UAM's existing error
codes. The function is an internal Go API with no HTTP route.

Callers: `ci-failure-kind` (`gitlab_failure.go`) labels a failed GitLab job
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
- Supply 2 to 32 options. IDs are unique, 1 to 64 ASCII letters, digits,
  underscores or hyphens, starting with a letter or digit. Descriptions are
  nonempty UTF-8 text of at most 512 bytes. Purposes are 1 to 48 lowercase
  letters, digits or hyphens, starting with a letter.
- The model is asked for exactly `{"choice":"allowed-id"}` or
  `{"choice":null}`. The answer is read like other Utility jobs: `<think>`
  blocks, a preamble or a code fence are tolerated; unknown keys, a second
  object, trailing prose, refusals, invalid UTF-8, and answers exceeding 2,048
  bytes are rejected. Only an allowed ID or null is accepted. A valid
  abstention is a successful answer.
- The request also carries a closed JSON Schema (`choice` is one of the option
  IDs or null, no other keys), which the Copilot provider sends as a strict
  `json_schema` response format. The schema is a hint: the answer is still
  validated as above. Custom (BYOM) models get no schema, since their
  endpoints were not probed for it, and rely on the prompt alone.
- The API calls the selected Utility provider at most once. It adds no retry
  or alternate-model fallback. The existing Utility model selection and
  explicit opt-out remain authoritative. Compatibility means models runnable
  through UAM's configured `UtilityRunner`, including supported BYOM models.
- Calls are tool-free and use the provider's restricted throwaway conversation.
  UAM retains ownership of credentials, two shared Utility slots, daily budgets,
  usage accounting, invalid-answer pauses, cancellation, and shutdown cleanup.
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

These tests use UAM's fake Utility provider. They cover strict output validation,
input rejection, configured model changes, usage accounting, single-call failures,
independent purpose pauses, valid abstentions, settings gates, slot waiting,
cancellation, deadlines, and shutdown waiting for provider cleanup. They do not
establish live model compatibility, semantic accuracy, or production latency.
