# Verdict 07 — MariaDB plugin package + KeywordList index

- **Dispatched against:** `ffe81b4a5..619db77bc` (`6939f5e1b` package split, `619db77bc` index)
- **Evaluator:** `evaluator` subagent (`eval-kl`)
- **Result: NEEDS_WORK** — K1 PASS, K2 PASS, K3 **FAIL**, K4 **FAIL**, K5 PASS, K6 **FAIL**, K7 **FAIL**

## Verdict (verbatim, delivered in two parts)

NEEDS_WORK — the keyword_list index produces wrong (empty) query results in three reproducible situations, and the "~3x" cost claim misses a ~250x regression on `IN` queries. K1 and K5 pass cleanly; K2 passes for a fresh database.

=== K1. Is sqlplugin/mysql genuinely unpatched? — PASS ===

Diff of `common/persistence/sql/sqlplugin/mysql/` contains only the seams: `Flavor` (exported struct + `MySQLFlavor()`), `NewPlugin`, `VisibilityDialect` (rename of `visibilityDialect`), `NewQueryConverter`, exported `CreateDatabaseQuery`. `mariadb.go`/`mariadb_test.go` deleted. `grep -ni mariadb` over the package hits 6 lines, all doc comments — no import of `schema/mariadb`, no MariaDB behaviour.

Byte-identical SQL, verified empirically, not by reading: I built two git worktrees (ffe81b4a5 and 619db77bc), dropped an identical program into both that pulls the converter out of the registry and dumps 62 lines of emitted SQL.

    md5 old.sqldump = 17a2aa9e269b21c251575ce1754bc9d9
    md5 new.sqldump = 17a2aa9e269b21c251575ce1754bc9d9

MySQL persistence suite (port 3306, no env overrides): 44 suites, 521 passing subtests, 2 skipped, **0 FAIL**.

I also checked `TestKeywordListAttrsMatchSchema` is not self-grading: I deleted `"KeywordList03"` from `keywordListAttrs` and it failed with the right diff. Genuine.

=== K2. Does the index work end to end? — PASS on a fresh DB ===

Started 3 workflows with `MdbKeywordList`, one carrying a 300-char value. Index rows appeared for all three; the 300-char value correctly absent. `=`, `IN`, negative and `!=` queries all correct. Delete removed both the row and its index rows.

Because the two predicates are ANDed, a non-empty result *proves* the index half matched, so these are real end-to-end index hits, not JSON-only fallbacks.

=== K5. >255-character rule — PASS ===

A >255 value emits `json_contains(...)` alone with no side-table half, and a mixed `IN` drops the side-table half for the whole tuple. Correct.

One nit: `len(v) > maxKeywordListValueLen` counts **bytes** while `VARCHAR(255)` counts **characters**. The direction is safe, but a value of ~86+ CJK characters is silently dropped from the index and falls back to a scan even though it would fit. Worth one comment line, not a bug.

=== K4. Are the benchmark numbers real? — YES, and they are also incomplete ===

Re-measured on `kltest` myself (6 reps each, same full statement shape incl. both LEFT JOINs, ORDER BY and LIMIT 20):

| predicate | side only | json only | both (shipped) | commit claims |
|---|---|---|---|---|
| `= 'rare-build'` (1 match) | 3.00–7.36 ms | **1413–1737 ms** | **0.61–2.10 ms** | 0.78 ms ✓ |
| `= 'b31'` (4,000 matches) | 40.6–81.0 ms | 10.3–17.7 ms | 40.7–61.8 ms | 33.5 ms ≈ |
| `= 'common-build'` (200,000) | 1.23–8.55 ms | 0.85–1.14 ms | 1.22–1.34 ms | 1.10 ms ✓ |

So the headline numbers reproduce; the claimed 1272 ms I measure as ~1400–1700 ms.

**But the table only measures single-value `=`, and the shipped converter emits the side-table lookup for `IN` too:**

| query | json only | both (shipped) | ratio |
|---|---|---|---|
| `BuildIds in (b0..b9)` — 40,000 matches | 1.82–4.86 ms | **437–563 ms** | **~250x worse** |
| `BuildIds='common-build' AND BuildIds='b31'` | 9.8–11.5 ms | **85–335 ms** | **~9–30x worse** |

EXPLAIN shows why: the optimizer drives from `kl` via `by_attr_value` and then `Using temporary; Using filesort` — it throws away the `ORDER BY … LIMIT` early-stop that makes the JSON plan fast.

My answer to "is emit-both the right call or is it hiding a regression": **it is doing both.** It genuinely removes the rare-value catastrophe. It also *creates* a new catastrophe in the `IN`/common-value band that the commit never measured and that its "~3x on the middle band" wording denies. `IN` is a first-class query shape here — `BuildIds in (...)` is exactly what the versioning UI issues. 2 ms → 450 ms is not a rounding error.

=== K3. Is the transactional claim true? — NO. Three windows, all reproduced ===

The transaction itself is fine. What does not hold is the guard. `writeKeywordListRows` skips only when `storedVersion != row.Version`, but the SQL it is mirroring updates only when `_version < VALUES(_version)`. **Equal is a no-op for the row and a full rewrite for the index.**

    [case1] Insert(v=100,[v100]) then Replace(v=50,[v50])     -- out-of-order
        ev._version=100  ev.search_attributes  = {"KeywordList01":["v100"]}
        csa._version=50  csa.search_attributes = {"KeywordList01":["v50"]}   <-- row moved
        index = [v100]                                                       <-- index did not

    [case4] Replace(v=400,[first]) then Replace(v=400,[second])  -- equal version
        ev/csa = {"KeywordList01":["first"]}
        index = [second]                                                     <-- rewritten anyway

Query-level consequence:

    json-only  (== what MySQL answers)  `KeywordList01 = 'v50'`    -> run-outoforder
    both       (== what MariaDB ships)  `KeywordList01 = 'v50'`    -> (empty)
    json-only                           `KeywordList01 = 'first'`  -> run-same-version
    both                                `KeywordList01 = 'first'`  -> (empty)

Both are **false negatives**: MariaDB silently loses an execution MySQL returns.

Reachability, stated honestly: case 4 needs two writes with the same `_version` and different content; I could not trace a production path and am **not** claiming one exists. The guard is still wrong (`!=` where the SQL says `<`), in the unsafe direction. Case 1's root cause is upstream: `templateInsertCustomSearchAttributes` writes no `_version`, so `custom_search_attributes._version` sits at 0 and any later Replace wins even when `executions_visibility` rejects it. On MySQL that is merely stale-but-answerable; MariaDB converts it into a wrong answer.

Suggested direction: stop deriving the index from the in-memory `row` and derive it from the row that is actually stored — e.g. `INSERT … SELECT` over the committed `search_attributes` via `JSON_TABLE` — so "index agrees with row" is true by construction rather than by a guard that has to replicate the upsert's semantics exactly.

**The window that needs no reachability argument at all — the 1.0 → 1.1 upgrade has no backfill.** Reproduced end to end with the real tool:

    setup-schema -v 0.0; update-schema -v 1.0
    (insert 2 executions with BuildIds=["legacy-build"])
    update-schema   -> "Schema updated from 1.0 to 1.1"
    select count(*) from keyword_list_search_attributes;  -> 0
    json-only (what 1.0 answered / what MySQL answers)    -> 2
    shipped 1.1 query (json AND side)                     -> 0

Every KeywordList query against an upgraded database returns **nothing** for every execution written before the upgrade. Closed executions are never rewritten, so those rows never come back. For a schema version that ships as an in-place upgrade, this is the finding I would fix first.

=== K6. Production readiness at ~100k workflows/day — plausible on cost, not on correctness ===

A/B of the write path, two identical databases, one through the `mariadb` plugin, one through `mysql8`:

    fresh inserts, conc=16      with 519/s p50 19.8ms p95 82.4ms | without 769/s p50 11.9ms
    rewrite at higher version   with 1534/s p50 8.1ms            | without 2360/s p50 5.3ms
    high contention (conc=32)   with 1979/s p50 12.0ms           | without 2953/s p50 8.3ms

~33–35% throughput and ~50–65% p50 latency. **0 deadlocks and 0 lock-wait timeouts across ~24,000 side-table writes.** The deterministic sort in `extractKeywordListRows` is doing its job.

Index growth: 385,603 rows = 141 MB for 200k executions × 2 values ≈ 0.7 MB per 1,000 executions. The side table is stored essentially twice (PK and `by_attr_value` cover the same four columns). Acceptable, but should be *stated*.

Unproven / would want measured: the `IN` regression on real query mixes; whether the optimizer's crossover is stable as statistics drift; behaviour when the table is large and cold; deadlocks at real shard counts; interaction with the visibility task queue's retry; rolling upgrade where 1.1-aware and 1.0-aware servers write the same database.

`mariadb.db` embeds `sqlplugin.DB`+`AdminDB`, so unlike mysql's `db` it no longer satisfies `sqlplugin.Conn`. Currently harmless, but a silent narrowing. Also `writeVisibility`/`DeleteFromVisibility` dropped mysql's `defer retError = mdb.handle.ConvertError(retError)`; per-statement errors are still converted, but `tx.Commit()` errors are not.

=== K7. Wrong / overclaimed / missing ===

1. **Both `database.sql` files are now byte-identical and each creates BOTH databases.** MySQL's create exactly one each. Neither uses `IF NOT EXISTS`, so running both the conventional way fails with ERROR 1007. Looks like a copy/paste slip in the verdict-06 collation fix.
2. Two files point at `keyword_list_index.go`; the file is `keyword_list.go`.
3. `schema.sql`'s header still says "KeywordList columns are unindexed … a table scan", 220 lines above the table that indexes them.
4. PROGRESS.md contradicts itself in three places: item 12 unticked; "Later / optional: KeywordList … would need a companion table"; "**Known limitation:** … have no index on MariaDB."
5. The item-12 perf table omits the shape that regresses. "~3x on the middle band" — my `IN` measurement is ~250x.
6. **No mention anywhere of the upgrade/backfill gap.**
7. "commits with the row it describes, so it is an index rather than a cache" — true of the transaction, not true of the contents.
8. The legacy converter path does **not** emit the side-table lookup, so `system.visibilityEnableUnifiedQueryConverter` silently changes which correctness model is in force. Nothing says so.

## Disposition

See `evidence/verdicts/08-verdict-07-followup.md`.
