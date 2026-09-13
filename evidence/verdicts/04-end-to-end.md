# Verdict 04 — end-to-end acceptance (items 05–09)

- **Dispatched against commit:** `54d548e51` plus PROGRESS.md items 05–09
- **Evaluator:** `evaluator` subagent (`eval-e2e`)
- **Criteria covered:** E1 MariaDB really the single store · E2 basic functionality ·
  E3 visibility query + pagination coverage · E4 Web UI · E5 the agentic-sample claims ·
  E6 persistence-suite counts · E7 whether the two fixes are honest or target-moving ·
  E8 any overclaim
- **Delivery note:** the agent's report did not reach this session on completion. It was
  requested four times and finally arrived split in two parts, reproduced below in full.

---

## Part 1 of 2 — verdict line and every non-passing finding

NEEDS_WORK — part 1 of 2: every finding that is NOT a pass (E5 partial, E7 caveats, E8). Part 2 has the passing criteria.

The MariaDB-as-single-store work itself holds up under independent re-running — E1, E2, E3, E4, E6 and E7 all pass, and both fixes in the commit are honest. Three things block a clean PASS: a real defect committed in `develop/docker-compose/docker-compose.yml`, one factually wrong statement in the E5 write-up, and stale claims in `PROGRESS.md`. Also, your premise for E5 was wrong — the histories you told me to read are gone, so I re-ran the whole agentic investigation.

---

## E5 — Agentic sample: your premise was wrong; I re-ran all of it

**The three histories do not exist.** `temporal workflow describe` returns `workflow not found` for all of `litellm-gpt-oss-workflow-id`, `mdb-diag-1`, `mdb-async-1`, and the visibility table held only 4 rows when I started. Cause: `make install-schema-mariadb` begins with `temporal-sql-tool ... drop -f` (Makefile:661), and it was re-run at 08:26:44 UTC — presumably to apply the `TIMESTAMP(6)` change. That wiped the evidence for items 06, 07 **and** 08. So I re-ran everything with the LLM.

**(a) VERIFIED.** `/tmp/samples-python` is unmodified (`git status` clean). Unmodified sample against MariaDB:
```
Result: Tokyo waits in hush—
neon glows, sky whispers unknown,
weather still a secret.
```
History in MariaDB: **35 events**, last event `WORKFLOW_EXECUTION_COMPLETED`, **5** model activity round trips. The tool failed, as the haiku says. ⚠️ Your "23 events, 3 round trips" did not reproduce — those numbers are LLM-nondeterministic and `PROGRESS.md` states them as flat proof.

**(b) VERIFIED, and I can back it harder than you did.** The history alone can't support it — the decoded payload contains `"output":"An error occurred while running the tool. Please try again. Error: "` with an **empty** error. Chain confirmed independently:
1. `agents/tool.py:2631` — sync `@function_tool` → `await asyncio.to_thread(...)`
2. `asyncio/threads.py:25` → `loop.run_in_executor(None, func_call)`
3. `_WorkflowInstanceImpl.run_in_executor is asyncio.AbstractEventLoop.run_in_executor` → `True`, whose body is `raise NotImplementedError`
4. Live probe — a plain workflow, no LLM: **`to_thread inside a Temporal workflow -> NotImplementedError: ''`**
5. Your diag run's un-swallowed failure: `UserError: Error running tool get_weather: ` — empty message, exactly matching `NotImplementedError('')`

openai-agents is 0.19.4 as claimed. Nothing in that chain touches the store.

**(c) CORE VERIFIED, DETAIL WRONG.** I ran the unmodified `GptOssWorkflow` (imported straight from the samples repo; only the connect address differs) against a stock `temporal server start-dev` on :7299, in-memory persistence. The tool **does** fail identically — 15 occurrences of `An error occurred while running the tool. Please try again. Error: `. But the run **COMPLETED**: 41 events, `Status COMPLETED`, and `'Max turns'` appears **0 times** in the decoded history.

> `PROGRESS.md` says the control "failed *worse* — the tool failed identically and the run died at 'Max turns (10) exceeded'." The part that matters (tool failure is store-independent) is true; "died at Max turns (10) exceeded" is a single-run artifact stated as fact, and it did not reproduce.

**(d) VERIFIED.** Async tool on MariaDB: **17 events** (matches your claim exactly), `get_weather` → `"output":"The weather in Tokyo is sunny."` recorded in MariaDB-backed history, 0 tool errors, visibility row `mdb-async-1 | status=2 | history_length=17`. Nit: "one word" understates it — the diff is `async` **plus** a `-> str` return annotation.

## E6 caveat — undocumented footgun (the counts themselves PASS, see part 2)

`NewMariaDBConfig()` (`common/persistence/tests/mariadb_test_util.go:60`) uses `environment.GetMySQLPort()` — the *same* env var. `MYSQL_PORT` selects the engine for both suites. Your commands work only because the default is 3306 (`temporal/environment/env.go:28`). `MYSQL_PORT=3307 go test -run TestMariaDB` would silently run the MariaDB plugin and schema against MySQL 8 and still report 44/477 — the suite name would lie. Not recorded anywhere.

## E3 caveat — `bulk.py` is broken (the criterion itself PASSes, see part 2)

`bulk.py` crashes at line 39 (`fetch_next_page()` returns `None` → `AttributeError: 'NoneType' object has no attribute 'workflows'`). Its own paging block does not work, though the 25 workflows do start and complete (`started=25 completed=25 in_order=True`). `page.py` is what actually proves pagination.

## E8 — Problems found

**1. Committed defect — `develop/docker-compose/docker-compose.yml` has a port conflict.** The commit adds a `mariadb` service binding `"3306:3306"`, but `mysql` already binds `"3306:3306"`. `docker compose up` cannot start both:
```
docker: Error response from daemon: ... Bind for 0.0.0.0:3306 failed: port is already allocated
```
Your actual environment runs MySQL on **3307**, which the committed file doesn't reflect — and since both test suites share `MYSQL_PORT`, this matters for anyone reproducing E6. Neither commit message nor `PROGRESS.md` mentions it. This is a regression to a shared dev file.

**2. Stale "verbatim" claims.** `PROGRESS.md:13` ("temporal (verbatim MySQL v8)") and `PROGRESS.md:91` ("is a verbatim copy of `schema/mysql/v8/temporal`") are now false. They're corrected 30 lines further down ("**no longer** a byte-for-byte copy"), but a doc the next session reads cold contradicts itself.

**3. Items 05–08 evidence was destroyed and that isn't recorded.** The DB reinstall wiped every workflow behind items 06 ("25 workflows"), 07 ("list (27)") and 08. I could re-derive 06, 07 and 08 by re-running, but item 07's "list (27)" is now unverifiable. `PROGRESS.md` should say the stores were reinstalled after those items were ticked.

**4. The "Max turns (10) exceeded" statement in (c) is wrong** as a general claim — see E5(c).

**5. Event/round-trip counts in (a) presented as fact** ("23 events, 3 round trips") are run-specific; mine were 35 and 5.

**6. Uncommitted dangling change.** `docker/config_template.yaml` is modified in the working tree (`{{- else if eq $db "mysql8" "mariadb" }}`) — not in either commit, not mentioned anywhere. It also only wires `defaultStore`, not the visibility store, so it's incomplete as a docker-image path for MariaDB.

**7. Minor:** "one word" for the async fix is really `async` + a `-> str` annotation.

---

**What to fix:** give `mariadb` a distinct host port in `docker-compose.yml` (3307 for mysql matches your actual setup, or 3308 for mariadb), correct or delete the "Max turns" sentence, update the two `verbatim` lines, note the DB reinstall, and either commit or drop `docker/config_template.yaml`. Consider a `MARIADB_PORT` so the two suites can't be pointed at the wrong engine.

**What I left running, as asked:** MariaDB, MySQL:3307, the MariaDB-backed server on 7233, and the UI. I killed the temporary dev server on :7299 and the sample workers I started. My scratch files are under `/Users/johnny/.claude/jobs/dccae301/tmp/` (`e3_sa.py`, `probe/main.go`, `tothread_probe.py`, `control_devserver.py`, and the four suite/history logs). New workflows now in MariaDB from my runs: `eval-sa-A/B/C`, `tothread-probe-1`, plus re-runs of `mariadb-smoke-1`, `mariadb-bulk-0..24`, `litellm-gpt-oss-workflow-id`, `mdb-diag-1` (left in a failing-workflow-task retry loop by design — you may want to terminate it), `mdb-async-1`.

---

## Part 2 of 2 — the passing criteria

NEEDS_WORK — part 2 of 2: the passing criteria (E1, E2, E3, E4, E6, E7) with the commands and output backing them.

## E1 — MariaDB is the single store: PASS

`config/development-mariadb.yaml` sets `defaultStore: mariadb-default` and `visibilityStore: mariadb-visibility`, both `pluginName: "mariadb"` → `127.0.0.1:3306`.

Running process is `./temporal-server --env development-mariadb start` (pid 48807), and `/tmp/temporal-server.log` confirms it loaded that file:
```
Loading config files=[config/development-mariadb.yaml]
"msg":"creating new visibility manager","visibility_plugin_name":"mariadb","visibility_index_name":"temporal_visibility"   (x4 — history, matching, frontend, worker)
```

Nothing else backs it — `lsof -p 48807 -a -i -nP` shows 15 established DB connections, **every one to `127.0.0.1:3306`**; no 3307, no 9200 (ES), no 9042 (Cassandra), and zero sqlite/`.db` file descriptors.

Data really lands there (before/after one workflow run):
```
=== BEFORE ===  executions=4  visibility=4  history_node=24
=== AFTER  ===  executions=6  visibility=6  history_node=46
```
Container on 3306 is `mariadb:11.4`, `VERSION()` = `11.4.13-MariaDB-ubu2404`. Schema state matches item 04: `temporal` 40 tables @ curr_version 1.19, `temporal_visibility` 5 tables @ 1.0. `temporal operator cluster health` = `SERVING`.

Caveat worth recording: the running binary reports `git-revision 0f09df5f8, git-modified:true` — it predates commit 54d548e51. The only non-test change in that commit is the schema, and the live DB does carry it (`timestamp(6)`), so this doesn't invalidate anything.

## E2 — Basic functionality: PASS

`cd /tmp/samples-python && uv run python /tmp/mdbcheck/smoke.py`
```
QUERY before signal: 0
RESULT: Hello, MariaDB! doubled=42 nudges=1
STATUS: COMPLETED
smoke exit=0
```
Exact result string as claimed. Covers activity, timer, child workflow, signal, query and the SA upsert.

## E3 — Visibility query coverage: PASS

I didn't use your 14 shapes; I wrote my own (`/Users/johnny/.claude/jobs/dccae301/tmp/e3_sa.py`) seeding three workflows with deliberately *different* SA values so every negative is meaningful and every positive asserts an **exact expected id-set** (a query matching everything fails):

```
PASS Keyword =             total=  2 ours=['eval-sa-A', 'eval-sa-C'] expected=['eval-sa-A', 'eval-sa-C']  |  MdbKeyword = 'alpha-key'
PASS Keyword = NEGATIVE    total=  0 ours=[] expected=[]  |  MdbKeyword = 'nonexistent-key'
PASS Int range >           total=  3 ours=['eval-sa-B', 'eval-sa-C'] expected=['eval-sa-B', 'eval-sa-C']  |  MdbInt > 40
PASS Int BETWEEN           total=  2 ours=['eval-sa-B'] expected=['eval-sa-B']  |  MdbInt BETWEEN 40 AND 60
PASS Int NEGATIVE          total=  0 ours=[] expected=[]  |  MdbInt > 1000
PASS Double <=             total=  3 ours=['eval-sa-A', 'eval-sa-B'] expected=['eval-sa-A', 'eval-sa-B']  |  MdbDouble <= 2.5
PASS Double NEGATIVE       total=  0 ours=[] expected=[]  |  MdbDouble > 99.0
PASS Bool true             total=  3 ours=['eval-sa-A', 'eval-sa-C'] expected=['eval-sa-A', 'eval-sa-C']  |  MdbBool = true
PASS Bool false            total=  1 ours=['eval-sa-B'] expected=['eval-sa-B']  |  MdbBool = false
PASS Datetime >            total=  3 ours=['eval-sa-B', 'eval-sa-C'] expected=['eval-sa-B', 'eval-sa-C']  |  MdbDatetime > '2026-05-01T00:00:00Z'
PASS Datetime NEGATIVE     total=  0 ours=[] expected=[]  |  MdbDatetime > '2030-01-01T00:00:00Z'
PASS KeywordList =         total=  3 ours=['eval-sa-A', 'eval-sa-C'] expected=['eval-sa-A', 'eval-sa-C']  |  MdbKeywordList = 'beta'
PASS KeywordList IN        total=  2 ours=['eval-sa-B', 'eval-sa-C'] expected=['eval-sa-B', 'eval-sa-C']  |  MdbKeywordList IN ('gamma','delta')
PASS KeywordList NEGATIVE  total=  0 ours=[] expected=[]  |  MdbKeywordList = 'zeta'
PASS Text match            total=  2 ours=['eval-sa-A'] expected=['eval-sa-A']  |  MdbText = 'brave'
PASS Text match 2          total=  3 ours=['eval-sa-A', 'eval-sa-C'] expected=['eval-sa-A', 'eval-sa-C']  |  MdbText = 'hello'
PASS Text NEGATIVE         total=  0 ours=[] expected=[]  |  MdbText = 'zzzzunmatchable'
PASS AND combo             total=  1 ours=['eval-sa-C'] expected=['eval-sa-C']  |  MdbKeyword = 'alpha-key' AND MdbInt > 40
PASS AND combo NEGATIVE    total=  0 ours=[] expected=[]  |  MdbKeyword = 'beta-key' AND MdbBool = true

19/19 query shapes correct
E3 exit=0
```
All 7 SA types, KeywordList `=` and `IN`, Bool both true and false, Text, Datetime, Int range — and every negative returns zero rows **overall**, not just zero of mine.

Pagination — `uv run python /tmp/mdbcheck/page.py`:
```
pages=4 rows=25 unique=25
missing=[]
page exit=0
```
Multiple pages, no duplicates, no missing rows. (`bulk.py`'s own paging block is broken — see part 1.)

## E4 — Web UI: PASS

`docker inspect temporal-dev-ui` → `TEMPORAL_ADDRESS=host.docker.internal:7233`, i.e. the MariaDB-backed server. UI version 2.54.1, root page HTTP 200.

**List:** `GET /api/v1/namespaces/default/workflows?query=` → `executions listed: 32`

**History:** `GET /api/v1/namespaces/default/workflows/mariadb-smoke-1/history?execution.runId=…&maximumPageSize=100`
```
UI history events: 30
first: EVENT_TYPE_WORKFLOW_EXECUTION_STARTED
last : EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED
   6  EVENT_TYPE_WORKFLOW_TASK_SCHEDULED / STARTED / COMPLETED
   1  EVENT_TYPE_ACTIVITY_TASK_SCHEDULED / STARTED / COMPLETED
   1  EVENT_TYPE_TIMER_STARTED / TIMER_FIRED
   1  EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_INITIATED / CHILD_WORKFLOW_EXECUTION_STARTED / COMPLETED
   1  EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED
   1  EVENT_TYPE_UPSERT_WORKFLOW_SEARCH_ATTRIBUTES
```
30 events, matching your item 07 claim.

**Search-attribute filtered queries through the UI**, including your exact one:
```
MdbKeywordList = 'alpha' AND MdbInt > 40 -> 2 rows: ['mariadb-smoke-1', 'mariadb-smoke-1']
MdbKeyword = 'alpha-key'                 -> 2 rows: ['eval-sa-C', 'eval-sa-A']
MdbBool = false                          -> 1 rows: ['eval-sa-B']
MdbKeywordList = 'zeta'                  -> 0 rows: []
```
2 rows rather than your 1 because I re-ran the smoke workflow, producing a second run.

## E6 — Persistence suites: PASS, counts exact

`go test ./common/persistence/tests/ -run TestMariaDB -count=1 -v` → `ok … 25.273s`, `exit=0`
`MYSQL_PORT=3307 go test ./common/persistence/tests/ -run TestMySQL -count=1 -v` → `ok … 66.897s`, `exit=0`

| | top-level PASS | subtest PASS | FAIL | SKIP |
|---|---|---|---|---|
| `TestMariaDB` | 44 | **477** | 0 | 2 |
| `MYSQL_PORT=3307 TestMySQL` | 44 | **477** | 0 | 2 |

Counting method: 521 total `--- PASS` lines at all nesting depths, minus 44 top-level, = 477. Identical on both engines, so your numbers are exactly right.

The 2 skips are the same on both:
```
--- SKIP: TestMariaDBMetadataPersistenceSuiteV2/TestRenameNamespaceCassandra (0.00s)
--- SKIP: TestMariaDBExecutionMutableStateStoreSuite/TestListConcreteExecutions (0.00s)
--- SKIP: TestMySQLMetadataPersistenceSuiteV2/TestRenameNamespaceCassandra (0.00s)
--- SKIP: TestMySQLExecutionMutableStateStoreSuite/TestListConcreteExecutions (0.00s)
```

I also proved the MariaDB suite isn't secretly hitting MySQL: polling `SHOW DATABASES` on both containers during `TestMariaDBClusterMetadataPersistence` showed test databases `test_0b67cfcc1bbd4c849fc953f4` and `test_1d6633a499c94bf3b18e4ed9` created **on the MariaDB container**. (A test DB also appeared on 3307 during the poll — that was another agent running concurrently, not this run.) See part 1 for the `MYSQL_PORT` footgun this exposed.

## E7 — Both fixes are honest, neither moves the target: PASS

I wrote and ran my own probe (`/Users/johnny/.claude/jobs/dccae301/tmp/probe/main.go`), not yours, using the same `go-sql-driver/mysql` with `clientFoundRows=true`:

```
=== MariaDB :3306 (VERSION()=11.4.13-MariaDB-ubu2404) ===
  probe1  ON DUPLICATE KEY UPDATE, 5 rows updated, clientFoundRows=true -> RowsAffected=10
  probe1b same values (no-op update)                                    -> RowsAffected=5
  probe2  '2026-09-13 08:21:20.7' -> TIMESTAMP(0) reads back as "2026-09-13 08:21:20"
  probe3  same value              -> TIMESTAMP(6) reads back as "2026-09-13 08:21:20.700000"

=== MySQL   :3307 (VERSION()=8.0.29) ===
  probe1  ON DUPLICATE KEY UPDATE, 5 rows updated, clientFoundRows=true -> RowsAffected=10
  probe1b same values (no-op update)                                    -> RowsAffected=5
  probe2  '2026-09-13 08:21:20.7' -> TIMESTAMP(0) reads back as "2026-09-13 08:21:21"
  probe3  same value              -> TIMESTAMP(6) reads back as "2026-09-13 08:21:20.700000"
```

**(i) `WithDoubleCountedUpdatedRows()` — honest, and an improvement over what it replaced.** Both engines report 10 for 5 updated rows, and both report 5 for 5 no-op updates. Identical behaviour. There is no MariaDB defect being hidden; the old `strings.Contains(strings.ToLower(s.T().Name()), "mysql")` check was matching on the wrong thing (a test name) instead of the actual store property, and "TestMariaDB…" simply didn't match. Naming the property is strictly better.

Scope is correct — the option is passed at exactly two call sites:
```
common/persistence/tests/mysql_test.go:640:   NewHistoryExecutionChasmSuite(t, store, sqltests.WithDoubleCountedUpdatedRows())
common/persistence/tests/mariadb_test.go:645: NewHistoryExecutionChasmSuite(t, store, sqltests.WithDoubleCountedUpdatedRows())
common/persistence/tests/postgresql_test.go:612: NewHistoryExecutionChasmSuite(p.T(), store)
common/persistence/tests/sqlite_test.go:1047,1459: NewHistoryExecutionChasmSuite(t, store)
```
And the assertion is still genuinely live for the others — `go test -run 'TestSQLite.*Chasm' -count=1 -v` passes with it running, including `TestSQLiteFileHistoryExecutionChasmSuite/TestInsertReplaceDelete`.

**(ii) `TIMESTAMP(6)` — legitimate, not a dodge.** The engine difference is real (probe2), and neither behaviour is wrong: truncate and round are both permitted. The shared assertion is
```go
// common/persistence/persistence-tests/cluster_metadata_manager.go:143 and :241
s.Equal(req.SessionStart.Round(time.Second), resp.ActiveMembers[0].SessionStart.Round(time.Second))
```
Both sides are rounded, which silently encodes MySQL's storage rounding: on a truncating engine the stored value is `trunc(t)` while the left side is `round(t)`, so it fails whenever the fractional part is ≥ .5 — roughly half of runs. `TIMESTAMP(6)` makes it pass **because the data is preserved** (probe3 shows `.700000` survives on both engines), not because the bar was lowered — the assertion is untouched, and it still runs for MariaDB.

Divergence is exactly what the commit claims. `diff -ru schema/mysql/v8/temporal schema/mariadb/v11/temporal` shows only those 3 columns, in 2 files (`schema.sql` and `versioned/v1.0/schema.sql`), and nothing else. Live DB confirms:
```
`session_start`  timestamp(6) NULL DEFAULT '1970-01-02 00:00:01.000000',
`last_heartbeat` timestamp(6) NULL DEFAULT '1970-01-02 00:00:01.000000',
`record_expiry`  timestamp(6) NULL DEFAULT '1970-01-02 00:00:01.000000',
```
Documented in both the schema comment and the commit message. My only reservation is the one you already flagged yourself: `PROGRESS.md` still calls the directory a "verbatim copy" in two places (part 1, finding 2).

### Supporting claims I also re-verified

Every MariaDB probe claim in `PROGRESS.md` checks out directly against the container:
- `JSON_OVERLAPS('["a"]','["a","b"]')` → `1` (supported)
- `'{"a":1}'->'$.a'` → `ERROR 1064 (42000)` (rejected)
- `CAST('["a"]' AS JSON)` → `ERROR 1064` (rejected)
- `'a' MEMBER OF ('["a"]')` → `ERROR 1064` (rejected)
- `CREATE INDEX i ON t ((COALESCE(b, CAST('9999-12-31 23:59:59' AS DATETIME))))` → `ERROR 1064` (rejected)
- `INDEX i2 ((CAST(j AS CHAR(255) ARRAY)))` → `ERROR 1064` (rejected)
- `JSON_VALUE('{"b":true}','$.b')` → `1` while `JSON_UNQUOTE(JSON_EXTRACT(...))` → `true` — exactly the trap you documented

Dropped-index count verified independently by diffing index names between the two visibility schemas rather than trusting the header: exactly **11**, matching the header list item-for-item, with **none added**:
```
by_binary_checksums, by_build_ids, by_keyword_list_01, by_keyword_list_02, by_keyword_list_03,
by_temporal_change_version, by_temporal_keyword_list_01, by_temporal_keyword_list_02,
by_temporal_pause_info, by_temporal_reported_problems, by_used_deployment_versions
```
`schema/mysql/v8/temporal/versioned` contains 20 migrations, as claimed. The `ORDER BY` rejection is in the shared SQL visibility store (`// ORDER BY is not support in SQL visibility store`, ~line 965 of `common/persistence/visibility/store/sql/visibility_store.go`), so "not MariaDB-specific" is correct. `make install-schema-mariadb` exists at Makefile:659 and uses `--pl mariadb` with the `schema/mariadb/v11/...` directories throughout.

---

## Disposition

See `evidence/verdicts/06-verdicts-04-05-followup.md`.
