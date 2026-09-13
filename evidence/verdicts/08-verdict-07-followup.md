# Follow-up to verdict 07

Verdict 07 returned **NEEDS_WORK**: K1 PASS, K2 PASS, K3 FAIL, K4 FAIL, K5 PASS,
K6 FAIL, K7 FAIL. Every finding was real. This records what was done.

## K3 — three false negatives, all fixed

### 1. The 1.0 → 1.1 upgrade had no backfill (the worst; needed no reachability argument)

`v1.1/backfill_keyword_list.sql` now populates the side table from existing rows, each
attribute read from the table whose generated column a query reads. Reproduced the
evaluator's exact scenario against the real tool:

```
setup-schema -v 0.0 ; update-schema -v 1.0
insert 2 executions with BuildIds=["legacy-build"] and KeywordList01=["legacy-kw"]
update-schema                       -> "Schema updated from 1.0 to 1.1"
count(keyword_list_search_attributes) -> 5    (was 0)

json only  (what MySQL answers)      -> run-legacy-1, run-legacy-2
shipped 1.1 query (json AND side)    -> run-legacy-1, run-legacy-2   <- was empty
custom SA through the side table     -> run-legacy-1, run-legacy-2
```

### 2 & 3. The version guard was wrong; the index no longer depends on one

The guard skipped on `storedVersion != row.Version` while the upsert updates on
`_version < VALUES(_version)` — equal was a no-op for the row and a rewrite for the
index, the exact case the commit claimed to prevent. Rather than fix the comparison, the
index is now derived from the search attributes **actually stored**, read back inside the
transaction, each keyword list taken from its own source table. "Index agrees with
column" is now true by construction.

Re-ran the evaluator's two probes through the real plugin:

```
case1  Insert(v=100,[v100]) then Replace(v=50,[v50])
  before: ev(v100)=[v100]  csa(v50)=[v50]  index=[v100]   -> query returned empty
  after : ev(v100)=[v100]  csa(v50)=[v50]  index=[v50]    -> json-only and shipped agree

case4  Replace(v=400,[first]) then Replace(v=400,[second])
  before: row=[first]  index=[second]                     -> query returned empty
  after : row=[first]  index=[first]                      -> json-only and shipped agree
```

The probe asserts `db.(sqlplugin.Conn)`, which also exercises the K6 parity fix below.

## K4 — the `IN` regression is gone, and the claim is corrected

`IN` no longer emits the side-table lookup. It now performs exactly as it did before the
side table existed. My own figure of 28 ms was a cold-cache measurement and does not
reproduce; verdict 09 measured 2.03 ms median on the 200k-row `kltest` set against
654 ms median with the lookup re-attached, so the fix is better than written here. Only single-value `=` uses the index, which is
where the 1400 ms → 1 ms win is. PROGRESS.md's "~3x on the middle band" wording is
replaced with the measured `IN` numbers.

The evaluator's judgement — "it is doing both: it removes one catastrophe and creates
another" — was right, and the fix is to stop creating the second one rather than to
re-word the claim.

## K7 — all corrected

- Both `database.sql` files were identical and each created *both* databases without
  `IF NOT EXISTS` (a copy/paste slip in the verdict-06 collation fix). Each now creates
  its own; verified the two run in sequence with no ERROR 1007.
- `schema.sql`'s header said KeywordList columns are unindexed, 220 lines above the table
  that indexes them. Corrected, including the `IN` caveat.
- Two comments pointed at a nonexistent `keyword_list_index.go`; the file is
  `keyword_list.go`.
- PROGRESS.md's three contradictions (unticked item 12, "Later / optional … would need a
  companion table", "**Known limitation:** … have no index") removed.
- The backfill gap, the storage cost, the `tx.Commit()` error divergence and the legacy
  converter's different correctness model are now all stated.

## K5 nit and K6 parity

- Length is now counted in characters rather than bytes, so a 255-character CJK value is
  indexed instead of silently falling back to a scan.
- The wrapper now satisfies `sqlplugin.Conn` as MySQL's db does; it had silently dropped
  those methods.
- `tx.Commit()` errors remain unclassified — the `DatabaseHandle` that converts them is
  not reachable from this package. Stated rather than fixed.

## Re-verification

```
persistence suites (TestMariaDB)     44 suites / 477 subtests / 0 FAIL
mariadb unit tests                    ok (incl. the 9 converter cases and 7 extraction cases)
tools/tests -run TestMariaDB          ok
install from scratch                  temporal 1.19 / temporal_visibility 1.1
smoke workflow                        "Hello, MariaDB! doubled=42 nudges=1", COMPLETED
side table populated                  BuildIds + KeywordList02 rows present
queries                               = 1, IN 1, negative 0, BuildIds 2
agentic check (mock)                  COMPLETED, tool called, output in history
```

Only Cassandra suites fail in `./common/persistence/...`, for lack of a Cassandra server.
