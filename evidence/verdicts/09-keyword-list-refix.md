# Verdict 09 — re-check of the verdict 07 fixes

- **Dispatched against:** `619db77bc..ade0a5043`
- **Evaluator:** `evaluator` subagent (`eval-kl2`)
- **Result: NEEDS_WORK** — R1 PASS, R2 PASS, R3 PASS, R4 PASS, **R5 FAIL**, R6 PASS, **R7 FAIL**

The substance held: the three false negatives and the `IN` regression are genuinely
fixed and the evaluator could not break them. The failures are a stale cost claim, stale
prose, committed junk, and a non-idempotent migration.

## Verdict (verbatim, delivered in four messages)

NEEDS_WORK — R1 PASS, R2 PASS, R3 PASS, R4 PASS, R5 FAIL, R6 PASS, R7 FAIL. The three false negatives and the IN regression are genuinely fixed; I could not break them. What fails is a stale cost claim and stale prose/junk.

R5 FAIL. The write path changed but PROGRESS.md still quotes my old A/B as current. Re-ran it (mariadb vs mysql8 plugin, same MariaDB 11.4, both at schema 1.1), conc=16: replace-at-higher-version 1510/s vs 3212/s (-53%, was ~33%), p50 7.4 vs 4.2ms (+76%, was ~50-65%); contended 2057 vs 4178/s (-51%), p50 5.8 vs 2.8ms (+107%). conc=32 the same shape (-37%/-52%). Fresh inserts only -22..24%. Deadlocks and lock-wait timeouts: 0 across ~45,000 side-table writes. So the cost did move, on exactly the shapes visibility does most.

R7 FAIL, three items:
(a) PROGRESS.md §12 still describes the design you removed as if shipped: "the stored _version is read back inside the transaction and the index is only rebuilt when our write is the one that landed". It reads search_attributes, not _version, and always rebuilds. visibility.go's header still says "only the extra table and the version guard around it are new" — there is no version guard.
(b) Two junk files committed: common/persistence/tests/test_b59fae9e33974949ad7d7c57 (647KB SQLite db, added in ade0a5043 itself) and develop/mariadb/__pycache__/agentic_check.cpython-312.pyc.
(c) The 1.1 migration is not idempotent. If the backfill commits but the version bump fails, the rerun dies on "Duplicate entry ... for key 'PRIMARY'" and stays at 1.0 forever — the tool only tolerates "already exist"/"not found". Reproduced. INSERT IGNORE fixes it.

### R1 BACKFILL — PASS, and stronger than your write-up claims

I did the 1.0 -> seed -> 1.1 upgrade with the real tool on a scratch DB (`r1upg`, 5,010 executions) carrying keyword lists in all three tables. Every adversarial case is right: a 300-char value skipped, duplicates collapsed, NULL search_attributes no rows, an execution in executions_visibility but not custom, an empty array, a JSON null, quotes. The decisive test is one you did not run: I pointed the shipped backfill statement at `kltest` (191,845 executions, 400,000 live-written index rows) inserting into a scratch table, then set-diffed it against the index the write path actually produced. 0 rows in backfill-not-live, 0 in live-not-backfill. The backfill and the write path agree exactly on 400,000 rows. It took 12.3s for 191,845 executions — worth putting that number in the migration comment next to the "run it in a window" advice, since it is the only calibration an operator has.

### R2 DERIVE-FROM-STORED — PASS

case1 now gives ev=[v100], csa=[v50], index BuildIds=v100 + KeywordList01=v50 — each attribute following its own table, which is exactly right, and both queries now agree with json-only. case4 index=[first]. I added eight more: Insert-then-Insert, Replace losing to a newer version, Insert/Replace/Insert/Replace interleaved, delete-then-write, write-then-delete, long/dup/CJK in one row, 16 writers over 40 runs, and 24 writers hammering one run_id 100 times each. 3,360 concurrent writes, 0 errors, and a global check comparing the side table against all 11 source columns via JSON_TABLE found no disagreement in either direction. I could not produce a state where the index and the column a query reads disagree.

### R3 IN — PASS

Dumped the SQL the real converter emits: `IN` is json_overlaps alone, `=` is json_contains AND the semi-join, `!=`/`NOT IN` are the NOT form alone, all unchanged for the negatives. On kltest, `BuildIds in (b0..b9)`: 2.03ms median shipped vs 654ms median with the lookup re-attached. That is back at the pre-side-table baseline I measured before (1.8-4.9ms) — but it is not the 28ms that evidence/verdicts/08 claims. I cannot reproduce 28ms; the fix is better than you wrote. `= 'rare-build'` 2.38ms shipped vs 1388ms json-only, so the selective win survives.

One thing to keep honest: `= 'b31'` (4,000 matches) is 45.8ms shipped vs 11.3ms json-only. `=` still costs ~4x on the middle band. §12's table does still disclose that, so it is not a moved target — but the comment you added in query_converter.go says "A single-value `=` does not have that problem", and it does, just 60x less than IN did.

### R4 PREDICATES — PASS

I checked the running server binary actually contains the fix before trusting it (`strings` finds "unable to read back stored search attributes" and "mariadb: transaction handle", and finds no trace of the old version-guard path). Then through the CLI on `default`: `=` on a normal value, on a value with a quote, on one with a backslash, on a 300-char value, on 255 CJK characters, on a duplicated value; `IN`, `IN` mixing a short and a 300-char value, `NOT IN`, `!=`, and negatives. All correct. The 300-char case is the interesting one — not in the index, converter drops the side half, workflow still found. The CJK-255 case is indexed and found through the index. Custom (KeywordList02 via MdbKeywordList) and executions (BuildIds) went through the server; chasm I could only reach at the plugin layer, where TemporalKeywordList01/02 are written, backfilled and queried correctly.

### R5 WRITE COST — FAIL

Two fresh databases at schema 1.1 on the same MariaDB 11.4, one driven through the `mariadb` plugin and one through `mysql8`, 400 writes per worker, warmed first:

    conc=16   fresh inserts      2127/s p50 6.3ms  |  2801/s p50 4.1ms   -24%  +54%
              replace higher ver 1510/s p50 7.4ms  |  3212/s p50 4.2ms   -53%  +76%
              contended 100 rows 2057/s p50 5.8ms  |  4178/s p50 2.8ms   -51% +107%
    conc=32   fresh inserts      1663/s p50 11.6ms |  2138/s p50 9.4ms   -22%  +23%
              replace higher ver 2194/s p50 10.1ms |  3472/s p50 6.2ms   -37%  +63%
              contended 100 rows 1842/s p50 10.7ms |  3801/s p50 6.0ms   -52%  +78%

Deadlocks 0, lock-wait timeouts 0, other errors 0, across ~45,000 side-table writes. The deterministic sort is still doing its job and I have no concern there. The problem is the bullet that says "Write cost measured by the evaluator: ~33% throughput and ~50-65% p50" — that measured the old path. The new path adds a three-table SELECT with two LEFT JOINs per write, and on replace-at-higher-version and contended rewrites, which is what a visibility task queue actually does all day, it is now about half the throughput and roughly double the p50. Either re-quote these numbers or say the figure is unverified for the new path; citing my old measurement for a path I never measured is the thing your own PROGRESS.md rules call out.

### R6 — PASS on all four

The two database.sql files each create their own database now; I ran them back to back through the client and both landed with utf8mb4_uca1400_nopad_ai_ci, no ERROR 1007. The Conn parity fix is real — `storeDB` embeds `sqlplugin.Conn`, and my probe asserts `db.(sqlplugin.Conn)` at runtime and it holds. Character-vs-byte length is right at every layer: utf8.RuneCountInString in Go, CHAR_LENGTH in the backfill, and end to end a 255-character CJK value is indexed (765 bytes in a VARCHAR(255)) while 256 is skipped, both through the backfill and through the server. The keyword_list.go filename corrections and the schema.sql header both landed and the header's new IN caveat is accurate.

### R7 — the three items above, plus two smaller ones

The stale prose is the one I would fix first, because it is the same failure verdict 07 flagged as K7.4 and it is now worse: §12 describes the removed mechanism in detail, and the correction that contradicts it sits 60 lines below. A reader who stops at §12 learns a design that does not exist. Same for the visibility.go header.

The non-idempotent migration: reproduced on a scratch DB. Put the table in place with a row in it, leave schema_version at 1.0, run update-schema — the CREATE TABLE is forgiven ("already exist") and then the backfill dies with Duplicate entry for key 'PRIMARY' and the database is stuck at 1.0 until someone truncates by hand. tools/common/schema/updatetask.go:142 only forgives "already exist" and "not found". This is a new failure mode: it exists because this is the first migration in the tree that carries DML. INSERT IGNORE, or ON DUPLICATE KEY UPDATE value=value, closes it.

Two smaller ones, neither blocking:
- Nothing tests the new sourceTable mapping. I changed KeywordList03 from sourceCustom to sourceExecutions and the whole mariadb package still passed. TestKeywordListAttrsMatchSchema guards the names, which is no longer the load-bearing half — a wrong source silently reintroduces exactly the class of false negative this commit was written to kill, and would pass CI. The mapping is correct today; I checked all 11 against schema.sql, and I also cross-checked the backfill's 11 UNION branches against keywordListAttrs programmatically — no mismatch. It is the guard that is missing, not the data.
- manifest.json lost its trailing newline.

### On the working-tree churn

The checkout was not mine. I ran no `git checkout` or `git switch` at any point; my only git write was one `git worktree add --detach`, and its own reflog has exactly one entry, 0000 -> ade0a5043 at 22:04:35, and never moved. The main tree's HEAD log shows the churn came from elsewhere, including `22:16:33 main -> gnhf/-28c68d` and two commits on that branch — unrelated Traditional Chinese documentation work.

Which of my checks ran after the 22:02:51 flip: none of the substantive ones. Everything from R1 through R7 ran at 22:04 or later, entirely out of the worktree at ade0a5043 or against databases.

## Disposition

Every finding accepted and fixed in `34e0494aa` and the commit that follows this file.

- **R5** — PROGRESS.md now carries the re-measured table (-53% throughput / +76% p50 on
  replace, -51% / +107% contended) and says explicitly that the earlier ~33% figure was
  measured against the previous write path.
- **R7 (a)** — `visibility.go:28` and PROGRESS.md §12 rewritten; neither mentions a
  version guard, because there is none.
- **R7 (b)** — both junk files removed and `.gitignore` extended so `git add -A` cannot
  sweep them back.
- **R7 (c)** — fixed, but the diagnosis needed correcting: the first statement to fail on
  a re-run is `CREATE INDEX` with `Duplicate key name 'by_attr_value'` (error 1061), not
  the backfill's primary key. Both halves are now idempotent (`IF NOT EXISTS` on the
  table and the index, `INSERT IGNORE` on the backfill), verified by rolling the version
  back to 1.0 with rows present and re-running: no errors, version reaches 1.1, no
  duplicate rows.
- **sourceTable guard** — `TestKeywordListAttrSourcesMatchSchema` added. It parses which
  `CREATE TABLE` block each JSON generated column falls in and compares against
  `keywordListAttrs`. Proven to catch the evaluator's exact experiment: flipping
  KeywordList03 to `sourceExecutions` now fails with "attributed to the wrong table".
- **`IN` = 28 ms** — corrected in verdict 08; the reproducible figure is ~2 ms.
- **`=` comment** — corrected; it does pay the same cost in kind, ~4x at 4,000 matches,
  which the comment now states alongside why it is still worth it.
- **Backfill calibration** — 12.3 s for 191,845 executions, and the set-identity result,
  are now in the migration comment.
- **manifest.json** — trailing newline restored.

On the working-tree churn: the evaluator is right and I was wrong to attribute it to it.
Its worktree reflog has a single entry post-dating the breaking flip. A different session
took the shared tree and committed twice on a `gnhf/-28c68d` branch.
