# PROGRESS — MariaDB as Temporal's single store

## Goal (session directive)
Make Temporal run with **MariaDB as the single store** (persistence + visibility).
Acceptance:
1. Basic functionality works (server boots, namespace, workflow start→complete, list/describe).
2. Temporal Web UI can operate against it.
3. The official agentic-AI sample actually runs to completion.

## Done
- [x] 00 Recon + environment baseline (mariadb:11.4 container `temporal-dev-mariadb`, port 3306, root/root)
- [x] 01 Probed MariaDB 11.4 for every construct the MySQL8 schema uses (findings below)
- [x] 02 `schema/mariadb/v11/` — temporal (verbatim MySQL v8) + rewritten visibility schema
- [x] 03 `mariadb` plugin registered from the `mysql` package + dialect-split query converters
- [x] 04 `make install-schema-mariadb` installs both schemas cleanly
      Proof: temporal db = 40 tables @ schema version 1.19,
             temporal_visibility = 5 tables @ schema version 1.0

- [x] 05 Server boots with MariaDB as default+visibility store; workflow start→complete
      Proof: `temporal operator cluster health` = SERVING; smoke workflow exercising
      activity + timer + child workflow + signal + query + SA upsert returned
      "Hello, MariaDB! doubled=42 nudges=1", status COMPLETED.
- [x] 06 Visibility query coverage — 14 query shapes over all 7 SA types, positives and
      negatives, plus 4-page pagination over 25 workflows with no gaps or duplicates.
- [x] 07 Web UI operates — list (27), detail + 30-event history, search attributes tab,
      and a `MdbKeywordList = 'alpha' AND MdbInt > 40` filter returning exactly 1 row.
- [x] 08 Official agentic AI sample (samples-python openai_agents/model_providers,
      gpt-oss:20b on local Ollama) runs to completion against the MariaDB server.

- [x] 09 Full MariaDB persistence coverage: `common/persistence/tests/mariadb_test.go`
      mirrors `mysql_test.go` one-for-one (44 suites). Both stores now report
      identical numbers: 44/44 suites, 477 subtests PASS, 0 FAIL, 2 SKIP
      (`TestRenameNamespaceCassandra`, `TestListConcreteExecutions` — skipped on
      MySQL too, so store-agnostic).

## In progress
- [ ] 10 Evaluator verdicts saved under `evidence/verdicts/`

## Notes / findings
- Recon (2026-09-13): main `schema/mysql/v8/temporal/schema.sql` (415 lines) uses **no**
  MySQL-8-only features — no JSON columns, no generated columns, no expression indexes.
  So the execution/persistence store is the easy half.
- `schema/mysql/v8/visibility/schema.sql` is the hard half. MySQL-8-only constructs found:
  - expression indexes: `(COALESCE(close_time, CAST(... AS DATETIME)))` in ~15 indexes
  - multi-valued indexes: `(CAST(col AS CHAR(255) ARRAY))` for KeywordList SAs
  - `GENERATED ALWAYS AS (search_attributes->"$.X")` with `CONVERT_TZ`/`REGEXP_REPLACE`
  - `FULLTEXT` index on STORED generated TEXT columns
- `sqlplugin/mysql/query_converter.go` emits MySQL-8-only `x member of (col)` and
  `json_overlaps(...)`; MariaDB has JSON_CONTAINS always, JSON_OVERLAPS only 10.9+.
- Plugin registration sites to touch: `cmd/server/main.go`, `cmd/tools/sql/main.go`,
  `tools/sql/main.go`, `common/persistence/visibility/defs.go`,
  `common/persistence/visibility/store/sql/query_converter_legacy_factory.go`.
- Expected-schema-version wiring lives in `sqlplugin/<x>/db.go` -> `schema/<x>/version.go`.
- Local docker has `mariadb:11.4` already pulled. Reusing it (11.4 LTS).

### MariaDB 11.4 probe results (verified against the running container)
Rejected by MariaDB, needed a rewrite:
- `->` and `->>` JSON operators — not supported at all -> `JSON_EXTRACT` / `JSON_UNQUOTE`
- `CAST(x AS JSON)` — not supported -> pass the JSON document as a string literal
- `x MEMBER OF (arr)` — not supported -> `JSON_CONTAINS(arr, JSON_QUOTE(x))`
- expression indexes `CREATE INDEX i ON t ((expr))` — not supported -> generated column
- multi-valued indexes `(CAST(col AS CHAR(255) ARRAY))` — not supported -> index dropped
- `COALESCE(ct, CAST('9999-12-31 23:59:59' AS DATETIME))` in a generated column — rejected
  with "Function or expression ... cannot be used in the GENERATED ALWAYS AS clause";
  dropping the inner CAST makes it accepted *and* indexable.
- `JSON_VALUE(doc,'$.b')` on a JSON boolean returns `1`/`0`, not `'true'`/`'false'`, so
  `JSON_VALUE(...) = 'true'` silently yields 0. Boolean columns use
  `JSON_UNQUOTE(JSON_EXTRACT(...)) = 'true'` instead.

Accepted unchanged (so no workaround was needed):
- `JSON_OVERLAPS`, `JSON_CONTAINS`, `JSON_QUOTE`, `JSON_VALUE`, `CONVERT_TZ`, `REGEXP_REPLACE`
- generated columns (virtual) over JSON, and secondary indexes on them, incl. `DESC`
- `FULLTEXT` index on a STORED generated column + `MATCH ... AGAINST ... NATURAL LANGUAGE MODE`
- `ON DUPLICATE KEY UPDATE ... VALUES(col)`
- every one of the 20 MySQL v8 *temporal* (execution) migrations, applied in version order

### Design decisions
- The `mariadb` plugin lives **inside** package `sqlplugin/mysql` rather than its own
  package: MariaDB speaks the MySQL wire protocol and shares every CRUD statement, and
  a separate package could not reach the unexported `db`/session/converter types without
  duplicating thousands of lines. Precedent: `sqlplugin/postgresql` hosts both
  `postgres12` and `postgres12_pgx`.
- Dialect differences are injected, not inherited: `queryConverter` embeds a
  `visibilityDialect` interface. Go has no virtual dispatch, so overriding methods by
  embedding a struct would leave `BuildSelectStmt` calling the MySQL versions.
- `schema/mariadb/v11/temporal` is a verbatim copy of `schema/mysql/v8/temporal`
  (version lineage kept at 1.19). The visibility schema had to be rewritten, so it
  starts its own lineage at 1.0.
- **Known limitation:** KeywordList search attributes (BuildIds, BinaryChecksums,
  TemporalChangeVersion, KeywordList01-03, ...) have no index on MariaDB. Queries on
  them are correct but scan. 11 indexes are dropped relative to MySQL; they are listed
  in the header of `schema/mariadb/v11/visibility/schema.sql`.

### Agentic AI sample — what actually happened
The official sample is `samples-python/openai_agents/model_providers` (`run_gpt_oss_worker.py`
+ `run_gpt_oss_workflow.py`), which is written for a local Ollama server, so it needs no
API key. Ran with `gpt-oss:20b`.

- **Unmodified sample against MariaDB:** workflow reached
  `WORKFLOW_EXECUTION_COMPLETED` (23 events, 3 `invoke_model_activity` round trips), but
  the agent's `get_weather` tool call failed and the haiku said so.
- **Cause (not ours):** openai-agents 0.19.4 runs *synchronous* `@function_tool`s through
  `asyncio.to_thread` -> `loop.run_in_executor`, which Temporal's deterministic workflow
  event loop rejects with `NotImplementedError`. Surfaced by re-running the sample with
  `failure_error_function=None`, which un-swallows the exception.
- **Control:** the same unmodified sample on a stock `temporal server start-dev` (sqlite)
  failed *worse* — the tool failed identically and the run died at
  "Max turns (10) exceeded". So the tool failure is store-independent.
- **Same agent, no Temporal:** tool call succeeds. Confirms model and Ollama are fine.
- **With the tool changed to `async def` (one word):** full agentic loop on MariaDB,
  17 events, `get_weather {"city":"Tokyo"}` -> "The weather in Tokyo is sunny." recorded
  in MariaDB-backed history, `WORKFLOW_EXECUTION_COMPLETED`, haiku reflects the tool
  result ("Bright sun bathes the city, Tokyo glows in golden light").

### Not MariaDB-specific, confirmed
- `ORDER BY` in a list query is rejected by `visibility_store.go:966` for *all* SQL
  visibility stores, not just MariaDB.

### Two real MySQL/MariaDB differences the persistence suite caught
The first MariaDB run failed 2 of 44 suites. MySQL 8.0.29 (container on 3307) passed
both, so each was investigated rather than waved through. `go run /tmp/dbprobe/main.go`
ran the same statements against both engines:

| probe | MariaDB 11.4.13 | MySQL 8.0.29 |
|---|---|---|
| `INSERT ... ON DUPLICATE KEY UPDATE`, 5 updated rows, `clientFoundRows=true` | RowsAffected 10 | RowsAffected 10 |
| `'2026-09-13 08:21:20.7'` into a second-precision `TIMESTAMP` | `08:21:20` (truncates) | `08:21:21` (rounds) |

1. **`TestMariaDBHistoryExecutionChasmSuite` — a test-harness bug, not a store bug.**
   The suite skipped its RowsAffected assertion with
   `strings.Contains(strings.ToLower(s.T().Name()), "mysql")`. "TestMariaDB..." does not
   contain "mysql", so the assertion ran — against a store that double-counts exactly
   like MySQL (probe row 1: both return 10). Replaced the test-name substring check with
   an explicit `WithDoubleCountedUpdatedRows()` option set by the MySQL and MariaDB call
   sites. The assertion is unchanged and still runs for PostgreSQL and SQLite.
2. **`TestMariaDBClusterMetadataPersistence` — a genuine engine difference.**
   `cluster_membership` stores second-precision `TIMESTAMP`s, and the test compares
   `req.SessionStart.Round(time.Second)` against the stored value, which only holds for
   an engine that rounds. Fixed in *our* schema rather than in the shared test:
   MariaDB's `cluster_membership` uses `TIMESTAMP(6)`, so nothing is lost and the shared
   assertion passes unmodified. The second failure in that suite
   (`TestClusterMembershipUpsertCanPageRead`, 101 rows instead of 100) was a cascade —
   the first failure skipped its `waitForPrune`, leaving a member behind; it passes now.

   This means `schema/mariadb/v11/temporal` is **no longer** a byte-for-byte copy of
   `schema/mysql/v8/temporal`: those three `TIMESTAMP` columns are the only difference.

### Test-environment note
`go test ./tools/tests/` fails 4 CQL/Cassandra suites, but it fails them identically on
the unmodified tree (verified with `git stash`) — no Cassandra is running here.
