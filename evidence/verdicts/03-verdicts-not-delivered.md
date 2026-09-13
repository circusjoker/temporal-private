# Verdicts 03 and 04 — dispatched, completed, never delivered

Two evaluator runs were dispatched and both finished, but neither report reached this
session. Recording that plainly rather than leaving the impression the work was graded.

| agent | dispatched against | criteria | state |
|---|---|---|---|
| `eval-e2e` | `54d548e51` + PROGRESS.md items 05–09 | E1–E8: MariaDB really the single store, basic functionality, visibility query + pagination coverage, Web UI, the agentic-sample claims, the persistence-suite counts, whether the two fixes in that commit are honest or target-moving, and any overclaim | ran ~35 min, went idle, report not delivered |
| `eval-schema2` | `9ab3a9f77..54d548e51` restricted to `schema/` and `common/persistence/sql/` | S1–S7: temporal-tree diff, install into a scratch db, column-by-column visibility equivalence against MySQL, the 11 dropped indexes, MySQL behaviour preservation across the dialect refactor, a sweep for MySQL-8-only SQL still reachable from the mariadb plugin | ran ~25 min, went idle, report not delivered |

Both were asked four times to resend via `SendMessage` — including a short-form request
capped at 1500 characters, in case message size was the cause. Nothing arrived. The first
evaluator (`eval-schema`, verdict 01) hit the same problem and only delivered after being
told explicitly to use `SendMessage`; these two did not, so this looks like a delivery
failure rather than the agents declining.

## What this does and does not leave

It does **not** leave these claims ungrounded — the underlying commands are in
`evidence/acceptance-rerun.md` and reproducible — but it does mean **nobody other than the
author has checked items 05–09 and the schema-equivalence questions.** Verdict 01 is the
only independent review in hand, and it found two real problems, which is a fair warning
about how much a self-check is worth here.

There is one thing worth flagging for whoever picks this up: `eval-e2e` clearly *did* run
against the live server — its workflows (`eval-sa-A`, `eval-sa-C`, type `EvalWorkflow`)
are in `temporal_visibility.executions_visibility`, and it re-ran the unmodified agentic
sample (the stored `litellm-gpt-oss-workflow-id` run is now a 35-event run of its own, not
the 23-event run described in PROGRESS.md). So it exercised the system; only its
conclusions are missing.

## Next session

Re-dispatch both with the criteria above, and instruct the agent **in the prompt** to
return its verdict via `SendMessage` to `team-lead`. Do not treat items 05–09 as
independently verified until one of them comes back.
