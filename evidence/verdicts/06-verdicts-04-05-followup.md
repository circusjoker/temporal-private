# Follow-up to verdicts 04 and 05

Both returned **NEEDS_WORK**. Between them every functional criterion passed
(E1–E4, E6, E7; S1–S6), and both evaluators re-ran their checks against live servers
rather than reading the diff. The failures were E5, E8 and S7. This records what was
done about each, at commit `ffe81b4a5` + the changes committed alongside this file.

## Fixed in code — two real defects

### 1. Port conflict in the shared dev compose file (verdict 04, E8-1) — BLOCKING

Confirmed: both services bound host 3306.

```
$ sed -n 6,28p develop/docker-compose/docker-compose.yml
  mysql:   ... ports: - "3306:3306"
  mariadb: ... ports: - "3306:3306"
```

MariaDB now binds **3307**, so both engines start together. Everything downstream
follows: `config/development-mariadb.yaml`, `make install-schema-mariadb` (through a new
`MARIADB_PORT` make variable), and the test harness.

The containers in this session were rebuilt to match — MariaDB on 3307, MySQL on 3306 —
and the whole acceptance pass was redone on them.

### 2. `MYSQL_PORT` selected the engine for *both* suites (verdict 04, E6 caveat)

`NewMariaDBConfig()` used `environment.GetMySQLPort()`. As the evaluator put it,
`MYSQL_PORT=3307 go test -run TestMariaDB` would have run the MariaDB plugin against
MySQL 8 and still reported 44/477 — "the suite name would lie".

Added `MARIADB_SEEDS` / `MARIADB_PORT` (default 3307) in `temporal/environment/env.go`
and switched `GetMariaDBTestClusterOption`, `NewMariaDBConfig`, the
`persistence_test_base.go` driver switches and the MariaDB CLI tests to them. Both
suites now pass with **no environment overrides at all**:

```
go test ./common/persistence/tests/ -run TestMariaDB -count=1 -v   -> 44 PASS / 477 subtests / 0 FAIL / 2 SKIP
go test ./common/persistence/tests/ -run TestMySQL   -count=1 -v   -> 44 PASS / 477 subtests / 0 FAIL / 2 SKIP
```

### 3. Collation PAD semantics (verdict 05, S7-2) — BLOCKING, and a real bug, not just docs

The evaluator framed this as "either document it or pin it". Verified independently:

```
MariaDB 11.4 default: utf8mb4_uca1400_ai_ci   'a' = 'a '  -> 1   (PAD SPACE)
MySQL 8.0.29 default: utf8mb4_0900_ai_ci      'a' = 'a '  -> 0   (NO PAD)
```

That is not cosmetic. It means two workflow ids, namespace names or task queue names
differing only by trailing whitespace are the *same value* on MariaDB, colliding on a
unique key that MySQL accepts. Pinning was the right call rather than documenting:
`CreateDatabase` for the `mariadb` flavor now emits
`COLLATE utf8mb4_uca1400_nopad_ai_ci`, and the MariaDB `database.sql` files match.

Proof on the real `namespaces` table after a clean reinstall — the evaluator's own
reproduction, now passing:

```
INSERT ... name='padtest'  ; INSERT ... name='padtest '
before: ERROR 1062 (23000): Duplicate entry 'padtest ' for key 'name'   -> 1 row
after:  stored
        [padtest]
        [padtest ]                                                      -> 2 rows, as MySQL
```

Accent- and case-insensitivity are unchanged (`'e'='é'`, `'A'='a'` still match, as on
MySQL). Requires MariaDB 10.10+, now stated in the schema comment.

## Fixed in wording — claims that outran their evidence

- `schema/mariadb/v11/version.go:6` said the execution schema is "byte-for-byte the
  MySQL v8 schema". The same commit made that false. Corrected. (Verdict 05, S7-1 — the
  one that shipped in source.)
- `common/persistence/sql/sqlplugin/mysql/mariadb.go:14` said MariaDB "accepts the whole
  execution (non-visibility) schema unchanged". Corrected, and now lists all four places
  MariaDB diverges.
- `PROGRESS.md` said "verbatim MySQL v8" in two places while saying the opposite 30 lines
  lower. Both corrected.
- The visibility schema header claimed MariaDB differs "in four ways that matter here".
  A fifth matters — the collation above. Now five, with the fix named.
- The same header described the boolean workaround as "JSON booleans extract as 1/0 via
  JSON_VALUE", which read as if `JSON_VALUE` were the implementation when it is the
  rejected alternative. Reworded.
- `versioned/v1.0/manifest.json` called itself "equivalent to MySQL v8 visibility 1.14"
  while dropping 11 indexes. Reworded.
- "died at Max turns (10) exceeded" (verdict 04, E5c) — the evaluator's control run
  COMPLETED with 41 events and zero occurrences of "Max turns". The part that matters
  (the tool failure is store-independent) reproduced; that detail did not. Corrected, and
  labelled as a single-run artifact that should not have been stated as fact.
- "23 events, 3 round trips" (E5a) — LLM-nondeterministic; the evaluator got 35 and 5.
  Now stated as varying per run, with both observations given.
- "one word" for the async fix — it is `async` plus a `-> str` annotation. Corrected.
- The mid-session `drop -f` that destroyed the item 06/07/08 evidence is now recorded in
  PROGRESS.md, including that item 07's "list (27)" is not reproducible.

## One evaluator sub-claim that does not hold

Verdict 04, finding 6 says `docker/config_template.yaml` "only wires `defaultStore`, not
the visibility store, so it's incomplete as a docker-image path for MariaDB". Rendering
the template for both `DB` values shows otherwise:

```
DB=mysql8   rendered 4898 bytes | pluginName lines: [pluginName: "mysql8"  pluginName: "mysql8"]
DB=mariadb  rendered 4900 bytes | pluginName lines: [pluginName: "mariadb" pluginName: "mariadb"]
```

Two occurrences, at template lines 52 (`default:`) and 82 (`visibility:`) — the branch
covers both datastores. The same finding calls the file uncommitted; it was committed in
`fe641e323`, which post-dates the commit the evaluator was dispatched against.

## Accepted, not fixed

Verdict 05, S7-3: the Bool generated column diverges from MySQL on *off-contract* JSON —
MariaDB yields 0 for a JSON number `1` where MySQL yields 1, and stores a value for JSON
`"true"`/`null` where MySQL raises ERROR 3156. It is correct for genuine JSON booleans,
which is all the search-attribute encoder emits. The evaluator rated it LOW and found no
path that writes such a value, but explicitly did not prove none exists. Recorded in
PROGRESS.md as unverified rather than ruled out.

Verdict 04's note that `/tmp/mdbcheck/bulk.py`'s paging block crashes is correct; it is a
throwaway script outside the repo, and `page.py` is what proves pagination. No action.

## Re-verification after all of the above

Containers rebuilt (MariaDB 3307, MySQL 3306), schema reinstalled, server restarted with
`make start-mariadb`:

```
cluster health                                    SERVING
DB sockets held by the server                     33 -> 127.0.0.1:3307, 0 -> 3306
temporal / temporal_visibility collation          utf8mb4_uca1400_nopad_ai_ci
schema versions                                   1.19 (40 tables) / 1.0 (5 tables)
smoke workflow                                    "Hello, MariaDB! doubled=42 nudges=1", COMPLETED
persistence suites, MariaDB, no env overrides     44 / 477 / 0 FAIL / 2 SKIP
persistence suites, MySQL,   no env overrides     44 / 477 / 0 FAIL / 2 SKIP
temporal-sql-tool CLI suites (mariadb)            ok
functional visibility suites (mariadb)            3 suites / 65 subtests / 0 FAIL / 2 SKIP
```
