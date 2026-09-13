# Long-running conventions for this project

## Always start here
Read `PROGRESS.md` — it is the handoff from the previous session and the acceptance
checklist. If it does not exist, create it with `## Done` / `## In progress` /
`## Next` / `## Notes`. Then `git log --oneline -10` and one smoke-test run, so you
know the tree works before you touch it.

## One item at a time
Finish one `PROGRESS.md` item before starting another. New task mid-session goes
into `## Next`.

## Proof before passing
Run the acceptance command, open its output, confirm it shows what you claim.

Four things look green and mean nothing:

- **`exit code` 0** — a run that skipped everything or served a cached result exits 0 too. Check how many ran and how many were skipped.
- **A test written to match the code you just wrote** — it only proves your two halves agree.
- **An assertion matching a value you hardcoded nearby** — that tests your constant. Match on a string the system produces, and keep it out of your test.
- **No error message** — silence is not success. Look for the positive signal.

## Do not grade your own work
Before claiming an item is done, and before ticking it off, dispatch the `evaluator`
agent (`subagent_type: evaluator`). Give it the criteria and the diff range — not
your reasoning about why the work is correct.

Report the verdict verbatim, first line included; never soften a `NEEDS_WORK`. To
disagree, run something — not re-read your own diff. After fixing, verify again: an
un-rerun fix is a claim like any other.

Then save it: write the reply as it came back to `evidence/verdicts/NN-<item>.md`,
with the commit it was dispatched against and which criterion it covered. Verbatim,
not a summary. A verdict that lives only in this conversation is gone at the next
compaction, and nothing afterwards can check what the review actually said.

## An honest negative counts as done
"Cannot be made to work, here is the error and what was tried" is a finished item.

What is not allowed is moving the target: weakening an assertion, shrinking scope,
adding a skip, deleting a case, or softening a caveat you already wrote. If the bar
is wrong, say so as its own claim.

## Criteria the evaluator can check
Split each item until one passing bit cannot hide the rest. Map every item to a
command; if it maps to none, say so and name what evidence stands in.

## Keep `PROGRESS.md` current
Tick what is done, note what is next, record what you learned and which assumptions
turned out wrong. The next session reads it cold.

Wording matches evidence: a number you could `grep` is not an estimate, a probe
shows a syntax is rejected but never that no alternative exists, and anything
untested is "unverified" rather than absent.

## Commit often
Commit at meaningful checkpoints; `git add` new files yourself. Nothing commits for you.

## If you're told to stop
A steering message from the human outranks your current plan.

