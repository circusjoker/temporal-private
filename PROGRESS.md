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
- [x] 02 `schema/mariadb/v11/` — temporal (the MySQL v8 schema apart from three
      `cluster_membership` TIMESTAMP(6) columns) + rewritten visibility schema
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

- [x] 10 Evaluator verdicts — all 3 delivered, all NEEDS_WORK, all addressed
  - `evidence/verdicts/01-schema-and-plugin.md` — NEEDS_WORK on `0f09df5f8`. Both findings
    were real; both are fixed and re-verified in
    `evidence/verdicts/02-verdict-01-followup.md`.
  - `evidence/verdicts/04-end-to-end.md` — NEEDS_WORK on items 05-09. E1-E4, E6, E7
    PASS; E5 and E8 fail. Found a real committed port conflict and several claims that
    outran their evidence.
  - `evidence/verdicts/05-schema-and-plugin-2.md` — NEEDS_WORK on the schema/plugin.
    S1-S6 PASS (exactly, in several cases); S7 fails on a stale claim shipped in source
    and an incomplete enumeration that was missing the collation difference.
  - `evidence/verdicts/06-verdicts-04-05-followup.md` — what was done about each finding,
    with the re-verification after the fixes.
  - `evidence/verdicts/03-verdicts-not-delivered.md` — kept as the record of the delivery
    problem: both reports arrived only after four explicit `SendMessage` requests.

## Next (requested after the first acceptance pass)
- [x] 11 MariaDB becomes its own plugin package rather than edits inside `sqlplugin/mysql`
- [ ] 12 A real answer for unindexed KeywordList search attributes
- [ ] 13 The agentic check runs without an LLM (mock model), and still against Ollama

### 11 — MariaDB is now an extension, not a patch
`sqlplugin/mysql` is 4,478 non-test lines and its `db` implements **184 methods**, so a
fully standalone package would mean duplicating all of it. But `sqlplugin.Plugin` is a
**two-method interface**, so the fix was to make `mysql` extensible instead of
MariaDB-aware:

`mysql` now exports four seams and knows nothing about MariaDB (it no longer even imports
`schema/mariadb/v11`, which it previously did — the dependency pointed the wrong way):

| seam | purpose |
|---|---|
| `mysql.Flavor` | plugin name, schema lineages, CREATE DATABASE template |
| `mysql.NewPlugin(flavor, converter)` | build a plugin for any MySQL-protocol server |
| `mysql.VisibilityDialect` | the visibility SQL that differs |
| `mysql.NewQueryConverter(dialect)` | everything else in the converter, shared |

`common/persistence/sql/sqlplugin/mariadb/` is **171 non-test lines** and holds every
MariaDB-specific decision: plugin name, schema versions, the NO PAD collation, and the
`close_time_or_max` / `json_contains` / `json_overlaps` dialect.

Cost of the move, stated plainly: `cmd/server/main.go` and `cmd/tools/sql/main.go` now
need a blank import of the package, the way postgres and sqlite already do. Previously
MariaDB registered for free from mysql's `init()`. That is the conventional trade and
worth it.

The legacy visibility converter stays in `visibility/store/sql` next to the MySQL,
PostgreSQL and SQLite ones — that package's converters are all colocated by design, and
its helpers are unexported.

Regression check after the move: MariaDB 44/477/0 and MySQL 44/477/0, CLI suites ok, unit
tests across `persistence/sql`, `visibility` and `searchattribute` all green, server boots
and reports `visibility_plugin_name: mariadb`.

## Later / optional
- Nothing blocking. Optional follow-ups, none of which affect the acceptance criteria:
  - KeywordList search attributes are unindexed on MariaDB (11 dropped multi-valued
    indexes). Correct but scanning; would need a companion table or a generated
    normalized column to index.
  - The Bool-on-off-contract-JSON divergence (verdict 05 S7-3) is unverified rather than
    ruled out.
  - MariaDB support is only exercised by the visibility functional suites; the rest of
    `tests/` has never been run with `-persistenceDriver=mariadb`.

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
- Plugin registration sites considered at plan time: `cmd/server/main.go`,
  `cmd/tools/sql/main.go`, `tools/sql/main.go`, `common/persistence/visibility/defs.go`,
  `common/persistence/visibility/store/sql/query_converter_legacy_factory.go`.
  **Only the last two needed changing.** The first three already import the
  `sqlplugin/mysql` package, and `mariadb.go` registers the plugin from that package's
  own `init()`, so they pick it up with no edit. Not a gap.
- Expected-schema-version wiring lives in `sqlplugin/<x>/db.go` -> `schema/<x>/version.go`.
- Local docker has `mariadb:11.4` already pulled. Reusing it (11.4 LTS).

### MariaDB 11.4 probe results (verified against the running container)
Rejected by MariaDB, needed a rewrite:
- `->` and `->>` JSON operators — not supported at all -> `JSON_EXTRACT` / `JSON_UNQUOTE`
- `CAST(x AS JSON)` — not supported -> pass the JSON document as a string literal
- `x MEMBER OF (arr)` — not supported -> `JSON_CONTAINS(arr, JSON_QUOTE(x))`
- expression indexes `CREATE INDEX i ON t ((expr))` — not supported -> generated column
- multi-valued indexes `(CAST(col AS CHAR(255) ARRAY))` — not supported -> index dropped
- `COALESCE(ct, CAST('9999-12-31 23:59:59' AS DATETIME))` in a generated column — the
  column itself is *accepted* and computes correct values; it is `CREATE INDEX` on it
  that fails, with "Function or expression ... cannot be used in the GENERATED ALWAYS AS
  clause" (ERROR 1901). Dropping the inner CAST makes the column indexable.
  (Corrected after verdict 01 C6; re-probed independently.)
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
- `schema/mariadb/v11/temporal` is the MySQL v8 tree with a single deliberate change —
  three `cluster_membership` columns are TIMESTAMP(6) (see the TIMESTAMP section below).
  Version lineage kept at 1.19. The visibility schema had to be rewritten, so it starts
  its own lineage at 1.0.
- **Known limitation:** KeywordList search attributes (BuildIds, BinaryChecksums,
  TemporalChangeVersion, KeywordList01-03, ...) have no index on MariaDB. Queries on
  them are correct but scan. 11 indexes are dropped relative to MySQL; they are listed
  in the header of `schema/mariadb/v11/visibility/schema.sql`.

### Agentic AI sample — what actually happened
The official sample is `samples-python/openai_agents/model_providers` (`run_gpt_oss_worker.py`
+ `run_gpt_oss_workflow.py`), which is written for a local Ollama server, so it needs no
API key. Ran with `gpt-oss:20b`.

- **Unmodified sample against MariaDB:** workflow reached
  `WORKFLOW_EXECUTION_COMPLETED`, but
  the agent's `get_weather` tool call failed and the haiku said so. **Event and
  round-trip counts vary run to run** because the model decides how many turns to take:
  observed so far: 23 events / 3 model activities, 35 / 5 (verdict 04), and 29 on the
  final re-run. All COMPLETED. Do not treat those numbers as a fixed expectation —
  three runs, three counts.
- **Cause (not ours):** openai-agents 0.19.4 runs *synchronous* `@function_tool`s through
  `asyncio.to_thread` -> `loop.run_in_executor`, which Temporal's deterministic workflow
  event loop rejects with `NotImplementedError`. Surfaced by re-running the sample with
  `failure_error_function=None`, which un-swallows the exception.
- **Control:** the same unmodified sample on a stock `temporal server start-dev`
  (in-memory) fails the tool identically, so the tool failure is store-independent.
  My run additionally died at "Max turns (10) exceeded"; verdict 04 re-ran the control
  and got a COMPLETED run of 41 events with no "Max turns" at all. **The tool failure
  reproduces; the "died at Max turns" detail does not** — it was a single-run artifact
  and should not have been stated as fact.
- **Same agent, no Temporal:** tool call succeeds. Confirms model and Ollama are fine.
- **With the tool made `async def` (plus a `-> str` annotation):** full agentic loop on
  MariaDB, 17 events, `get_weather {"city":"Tokyo"}` -> "The weather in Tokyo is sunny."
  recorded in MariaDB-backed history, `WORKFLOW_EXECUTION_COMPLETED`, haiku reflects the
  tool result. Verdict 04 independently reproduced the 17 events.

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

### Making MariaDB usable, not just possible
- `docker/config_template.yaml`: the `mysql8` branch now also matches `mariadb`, so the
  official image accepts `DB=mariadb` with the existing `MYSQL_*` env vars. Verified by
  rendering the template for both values (`/tmp/tmplcheck/main.go`): each produces
  `pluginName: "mariadb"` for both the default and the visibility datastore.
- `make start-mariadb`, `config/development-mariadb.yaml`, a `mariadb` service in
  `develop/docker-compose/docker-compose.yml`, and CONTRIBUTING/tools-sql-README entries.
- The embedded-schema path works too, not just `-d <dir>`:
  `--schema-name mariadb/v11/visibility` installs 5 tables at version 1.0, and
  `temporal-sql-tool setup-schema --help` now lists `mariadb/v11/temporal` and
  `mariadb/v11/visibility`.

### Functional tests (the real in-process cluster), MariaDB vs MySQL
`tests/testcore/flag.go` now accepts `-persistenceDriver=mariadb` and counts it as a SQL
visibility store, so the functional suites can run against it. The three visibility
suites -- the part most at risk from the rewritten schema and query converter -- were run
against both engines:

```
CGO_ENABLED=0 go test ./tests/ -tags disable_grpc_modules,test_dep \
  -run 'TestAdvancedVisibilitySuite$|TestAdvancedVisibilitySuiteLegacy$|TestWorkflowVisibilityTestSuite$' \
  -persistenceType=sql -persistenceDriver=<driver> -count=1 -v
```

| driver | suites | subtests | fail | skip |
|---|---|---|---|---|
| mariadb | 3 PASS | 65 PASS | 0 | 2 |
| mysql8 (MYSQL_PORT=3307) | 3 PASS | 65 PASS | 0 | 2 |

Same two skips on both (`TestListWorkflow_OrderBy` in each of the two advanced-visibility
suites) — the ORDER BY restriction that is store-agnostic, noted above.

`TestAdvancedVisibilitySuiteLegacy` matters here: it forces the *legacy* query converter
path, which the unified converter's default (`visibilityEnableUnifiedQueryConverter=true`)
otherwise hides. Both MariaDB converters are therefore exercised.

Note: functional tests need `-tags test_dep`; without it every suite panics with
"testhooks.Set called but TestHooks are not enabled", regardless of driver.

### temporal-sql-tool CLI coverage
`tools/tests/mariadb_cli_test.go` mirrors `mysql_cli_test.go`. All 5 suites pass against
MariaDB (`go test ./tools/tests/ -run TestMariaDB -count=1 -v`): connection, handler
config validation, `TestSetupSchema`, `TestUpdateSchema` + both dry-runs (which walk the
whole versioned migration chain), and `TestVerifyCompatibleVersion` (which exercises the
schema-version check the server does at boot).

### Audit of remaining plugin-name switches
Grepped every `mysql.PluginName` / `"mysql8"` site to find dispatches that would skip
MariaDB:
- `service/frontend/operator_handler.go` (add/remove search attributes) asks only
  "is this Elasticsearch?" and otherwise takes the SQL path, so `mariadb` needs nothing —
  consistent with the 7 custom search attributes registering successfully end to end.
- `common/persistence/persistence-tests/persistence_test_base.go` had two switches that
  `panic("unknown sql store driver")`. They are only reached when DBPort/DBHost are
  unset, which `GetMariaDBTestClusterOption()` never leaves unset, so nothing was broken
  — but the latent panic is closed anyway.
- Everything else is test fixtures or the `--pl` flag default.

### `make start-mariadb` verified for real
The server was stopped and restarted through the Makefile target (not the `--env` form
used earlier): healthy in 2s, `workflow count` and a `MdbBool = true` filter both answered
from MariaDB. `evidence/acceptance-rerun.md` is the raw output of a full acceptance pass
against that server.

### Corrections after verdicts 04 and 05 (both NEEDS_WORK)

**Ports — a real defect, now fixed.** The `mariadb` service added to
`develop/docker-compose/docker-compose.yml` bound host 3306, which `mysql` already
binds, so `docker compose up` could not start both ("Bind for 0.0.0.0:3306 failed: port
is already allocated"). MariaDB now uses host **3307**, and everything that talks to it
follows: `config/development-mariadb.yaml`, `make install-schema-mariadb` (via a
`MARIADB_PORT` variable), and the test harness.

**`MYSQL_PORT` no longer selects the engine for both suites.** `MARIADB_SEEDS` /
`MARIADB_PORT` (default 3307) now exist alongside the MySQL ones, and the MariaDB test
config, cluster option and CLI tests use them. Previously `MYSQL_PORT=3307 go test -run
TestMariaDB` would have run the MariaDB plugin against MySQL 8 and still reported 44/477
— a passing result that lied about what it tested. Both suites now pass with **no env
overrides at all**, each against its own engine.

**Collation — a real semantic difference, now pinned.** MariaDB 11.4 defaults utf8mb4 to
`utf8mb4_uca1400_ai_ci`, which is PAD SPACE; MySQL 8's `utf8mb4_0900_ai_ci` is NO PAD.
Under PAD SPACE two values differing only by trailing whitespace are the *same* value, so
a workflow id, namespace name or task queue name that MySQL accepts would collide on a
unique key. Databases created for the `mariadb` plugin are now
`COLLATE utf8mb4_uca1400_nopad_ai_ci`. Proof on the real `namespaces` table after
reinstall: inserting `'padtest'` and then `'padtest '` now stores **both** rows, as MySQL
does, where before the second was `ERROR 1062 Duplicate entry`. Accent- and
case-insensitivity are unchanged. Requires MariaDB 10.10+.

**Evidence destroyed mid-session (recorded, not hidden).** `make install-schema-mariadb`
starts with `temporal-sql-tool ... drop -f`. Re-running it to apply the TIMESTAMP(6)
change — and again for the collation change — wiped every workflow behind the item 06,
07 and 08 numbers. Those items were re-derived by re-running, both by verdict 04 and
here; the specific "list (27)" count in item 07 is gone and not reproducible. Any count
in this file that came from a specific run should be read as illustrative.

**One evaluator sub-claim was wrong.** Verdict 04 finding 6 says
`docker/config_template.yaml` "only wires `defaultStore`, not the visibility store".
Rendering the template for `DB=mariadb` produces `pluginName: "mariadb"` **twice**, at
template lines 52 (`default`) and 82 (`visibility`). The same finding calls the file
uncommitted; it was committed in `fe641e323`, before the verdict was written but after
the commit the evaluator was pointed at.

**Closed: the Bool divergence cannot be reached through the server.** Verdict 05 (S7-3)
found that the MariaDB Bool column disagrees with MySQL on *off-contract* JSON — a JSON
number `1` gives 0 where MySQL gives 1, and `"true"`/`null` store a value where MySQL
raises ERROR 3156. It rated this LOW but could not prove no path writes such a value.
Traced and pinned:

    prepareSearchAttributesForDb (visibility_store.go:781)
      -> searchattribute.Decode(request.SearchAttributes, &saTypeMap, false)
        -> sadefs.DecodeValue(payload, INDEXED_VALUE_TYPE_BOOL, ...)
          -> decodeValueTyped[bool]  -- json.Unmarshal into *bool, then []bool

A Bool-typed attribute therefore yields a Go `bool` or an error; on error `Decode` sets
the value to nil and `prepareSearchAttributesForDb` deletes nil values before the write.
So a Bool column only ever receives JSON `true`/`false`, or nothing.
`common/searchattribute/bool_decode_invariant_test.go` locks this down with 10 cases
(number 1/0, string "true"/"false", null, object, bare string all dropped; true/false and
a single-element list preserved) and says in its comment that a failure means the MariaDB
schema needs revisiting, not just the test. **No functional impact.**

### MariaDB stands alone — MySQL container stopped
`docker stop temporal-dev-mysql` (port 3306 closed, nothing else listening), then the
whole acceptance re-run against MariaDB only:

```
cluster health                      SERVING
a brand-new smoke workflow          "Hello, MariaDB! doubled=42 nudges=1", COMPLETED
web UI                              HTTP 200, list + history answering
persistence suites (TestMariaDB)    44 suites / 477 subtests / 0 FAIL / 2 SKIP
temporal-sql-tool CLI (TestMariaDB) ok
functional visibility suites        3 suites / 65 subtests / 0 FAIL
```

The MySQL container is **only** needed to run the `TestMySQL*` control suites — with it
stopped those fail to connect, which is expected, not a regression. Nothing about running
Temporal on MariaDB requires it.

### 12 — KeywordList search attributes are indexed again
MariaDB has no multi-valued indexes, so the 11 `CAST(col AS CHAR(255) ARRAY)` indexes
MySQL uses for KeywordList attributes could not be created and every such predicate
scanned. Replaced with a normalized side table, `keyword_list_search_attributes`
(visibility schema 1.1), one row per (execution, attribute, value).

**Maintained transactionally, not as a cache.** MariaDB now owns its visibility write
path (`sqlplugin/mariadb/visibility.go`) precisely so the side table commits with the row
it describes. Visibility upserts are version-guarded — a stale upsert is a no-op — so the
stored `_version` is read back inside the transaction and the index is only rebuilt when
our write is the one that landed. Otherwise the index would describe a row that is not
there.

**Queries emit both predicates, ANDed**, and let the optimizer choose. Measured on
200,000 executions in one namespace (`kltest`, 400,000 index rows):

| predicate selectivity | side table only | JSON scan only | both (shipped) |
|---|---|---|---|
| 1 match | 5.7 ms | **1272.8 ms** (191,845 rows scanned) | **0.78 ms** |
| ~4,000 matches | 43.3 ms | 9.5 ms | 33.5 ms |
| 200,000 matches | 1.2 ms | 0.79 ms | 1.10 ms |

Neither shape is fast on its own: the side table wins when the value is rare, and the
ordered index wins when it is common because `ORDER BY … LIMIT` stops early. Emitting
both turns the unbounded worst case (which grows with table size) into sub-millisecond,
at the cost of ~3x on the middle band, and the JSON predicate still decides correctness —
a spurious index row cannot produce a wrong answer.

Negated predicates emit the JSON half alone: `NOT (json AND side)` is not the negation
wanted, and a NOT cannot use the index anyway. Values longer than the column's
VARCHAR(255) are not indexed and are not asked of the index, on either side.

`TestKeywordListAttrsMatchSchema` fails if the attribute list drifts from the schema —
drift is silent and would just stop indexing a column.

### Connection limits — found by running the full functional suite
The first full-suite run failed with `no usable database connection found`. MariaDB's
default `max_connections` is 151; the suite drove `Max_used_connections` to 152 and
`Connection_errors_max_connections` to 37. Each Temporal cluster holds
`maxConns` + visibility `maxConns` (20 + 2 in the dev config), and the suite runs many at
once. `develop/docker-compose` now sets `--max-connections=1000`. **Worth sizing for
real deployments**: connections are per host process, not per cluster.
