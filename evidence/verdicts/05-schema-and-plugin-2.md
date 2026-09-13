# Verdict 05 — MariaDB schema + plugin, second review

- **Dispatched against:** `9ab3a9f77..54d548e51`, restricted to `schema/` and
  `common/persistence/sql/` (plus the visibility-store dialect files named in S5)
- **Evaluator:** `evaluator` subagent (`eval-schema2`)
- **Criteria covered:** S1 temporal-tree diff · S2 install into a scratch db ·
  S3 column-by-column visibility equivalence against MySQL · S4 the 11 dropped indexes ·
  S5 MySQL behaviour preserved across the dialect refactor · S6 a sweep for MySQL-8-only
  SQL still reachable from the mariadb plugin · S7 anything else wrong or overclaimed
- **Delivery note:** as with verdict 04, the report did not arrive on completion and had
  to be requested repeatedly; it finally came in two parts, reproduced below in full.

---

## Part 1 of 2 — verdict line and every non-passing finding

NEEDS_WORK — MariaDB schema/plugin review, part 1 of 2: the verdict line and every non-passing finding (S7). Part 2 carries the passing criteria S1-S6.

Scope: diff range 9ab3a9f77..54d548e51, restricted to schema/ and common/persistence/sql/ (plus the visibility-store dialect files named in S5).

Summary: every functional criterion S1-S6 passes, several of them exactly. The blockers are all in S7 — one statement in shipped source that this same commit range made false, and one enumerated-completeness claim that is not complete. No code or schema change is required to clear them.

Scratch databases used: evalsch_tmp, evalsch_vis, evalsch_emb, evalsch_emb2 (MariaDB) and evalsch_my_tmp, evalsch_my_vis (MySQL). All six dropped at the end; `temporal`, `temporal_visibility` and every container untouched; working tree left clean (`git status --short` empty).

================================================================
S7 — FINDINGS (the reason for NEEDS_WORK)
================================================================

--- 1. BLOCKER. schema/mariadb/v11/version.go:6 states something this commit range made false. ---

    // Version is the MariaDB database release version.
    // The execution/persistence schema is byte-for-byte the MySQL v8 schema, so the
    // version lineage is kept in sync with it.
    const Version = "1.19"

    git log --oneline -S 'TIMESTAMP(6) DEFAULT' -- schema/mariadb/v11/temporal/schema.sql
    54d548e51 Cover MariaDB with the full persistence suite, and fix what it caught

The very same commit's own PROGRESS.md:150 says it plainly:

    This means `schema/mariadb/v11/temporal` is **no longer** a byte-for-byte copy of
    `schema/mysql/v8/temporal`: those three `TIMESTAMP` columns are the only difference.

Two further copies of the stale claim were also not updated:

    common/persistence/sql/sqlplugin/mysql/mariadb.go:14
      "MariaDB speaks the MySQL wire protocol and accepts the whole execution
       (non-visibility) schema unchanged"

    PROGRESS.md:13
      "- [x] 02 `schema/mariadb/v11/` — temporal (verbatim MySQL v8) + rewritten visibility schema"

The functional decision is right and I verified it (S1/S2: those three columns and only those). The three comments are wrong, and one of them ships in source. This is exactly the "wording matches evidence" bar in CLAUDE.md, and it is a one-line fix in each place.

--- 2. BLOCKER. The visibility schema header claims an exhaustive list that is missing a fifth difference that matters. ---

schema/mariadb/v11/visibility/schema.sql:3-4 says:

    -- Derived from schema/mysql/v8/visibility/schema.sql. MariaDB 11.4 differs from
    -- MySQL 8 in four ways that matter here (each verified against mariadb:11.4):

All four listed are real and I confirmed all four. But the default utf8mb4 collation differs in PAD semantics, and that changes uniqueness and equality on every VARCHAR in BOTH schemas:

    SELECT schema_name, default_character_set_name, default_collation_name
      FROM information_schema.schemata ...
    evalsch_tmp     utf8mb4   utf8mb4_uca1400_ai_ci      (MariaDB 11.4)
    evalsch_my_tmp  utf8mb4   utf8mb4_0900_ai_ci         (MySQL 8.0.29)

    SELECT _utf8mb4'a' COLLATE <that collation> = _utf8mb4'a ';
    MariaDB utf8mb4_uca1400_ai_ci -> 1      (PAD SPACE: trailing spaces ignored)
    MySQL   utf8mb4_0900_ai_ci    -> 0      (NO PAD: trailing spaces significant)

    (accent/case/sharp-s all agree, so this is the only axis that diverges:
     'e'='é' -> 1 on both, 'A'='a' -> 1 on both, 'ss'='ß' -> 1 on both)

tools/.../mysql/admin.go:37 creates the database as `CREATE DATABASE IF NOT EXISTS %v CHARACTER SET utf8mb4` with no COLLATE, so each engine takes its own default. Concrete consequence on the real `namespaces` table — same two INSERTs, same schema, different outcome:

    INSERT ... name='padtest'  ; INSERT ... name='padtest '
    MariaDB: ERROR 1062 (23000): Duplicate entry 'padtest ' for key 'name'
             -> 1 row stored:  [padtest]
    MySQL 8: both succeed
             -> 2 rows stored: [padtest], [padtest ]

The same divergence applies to task_queue_user_data.task_queue_name (PK), executions.workflow_id, nexus_endpoints, and every VARCHAR(255) Keyword search-attribute generated column.

This is a property of running Temporal on MariaDB rather than a defect introduced by the diff. But the schema asserts a COMPLETE enumeration of what differs and this is not in it. MariaDB does offer utf8mb4_uca1400_nopad_ai_ci (confirmed present: SHOW COLLATION LIKE 'utf8mb4_uca1400%nopad%') if pinning is preferred; otherwise the honest fix is to add it to the list as a known, accepted difference.

--- 3. LOW, not blocking. The Bool idiom diverges on off-contract JSON, silently. ---

`JSON_UNQUOTE(JSON_EXTRACT(...)) = 'true'` is exactly right for JSON booleans (verified in S3, all three of true/false/absent agree). For other JSON types it parts company with MySQL's `->`. Each row inserted on its own so one rejection does not mask the others:

    Bool01 =   MySQL 8                                                   MariaDB 11.4
    true       1                                                         1
    false      0                                                         0
    "true"     ERROR 3156 (22018) Invalid JSON value for CAST to INTEGER  1     <<< DIFF
    "false"    ERROR 3156 (22018) Invalid JSON value for CAST to INTEGER  0     <<< DIFF
    1          1                                                         0     <<< DIFF
    0          0                                                         0
    null       ERROR 3156 (22018) Invalid JSON value for CAST to INTEGER  0     <<< DIFF
    absent     <NULL>                                                    <NULL>

I did not find a server path that writes a JSON number or string into a Bool search attribute (searchattribute decoding produces a Go bool, so the column receives true/false), so I rate this LOW. I did NOT exhaustively prove no such path exists — treat that as unverified rather than ruled out. The asymmetry worth recording is the direction: MySQL fails loudly, MariaDB stores a wrong-but-plausible value.

--- 4. MINOR, no action strictly needed. ---

schema/mariadb/v11/visibility/schema.sql:6 says "JSON booleans extract as 1/0 via JSON_VALUE -> compare unquoted text", but JSON_VALUE appears nowhere in the schema — it is the rejected alternative, and the wording reads as if it were the implementation.

schema/mariadb/v11/visibility/versioned/v1.0/manifest.json describes itself as "Initial MariaDB visibility schema (equivalent to MySQL v8 visibility 1.14)" while 11 indexes are dropped. The schema header does disclose the drops, so only the manifest string overstates.

================================================================
WHAT WOULD FLIP THIS TO PASS
================================================================

Fix the three stale claims in finding 1 — schema/mariadb/v11/version.go:6, common/persistence/sql/sqlplugin/mysql/mariadb.go:14, PROGRESS.md:13 — and either add the collation/PAD difference from finding 2 to the header list in schema/mariadb/v11/visibility/schema.sql or pin the collation in admin.go.

No code or schema change is required. S1-S6 are sound, and I re-ran them against live servers rather than reading the diff and reasoning about it. Part 2 follows with the commands and output for each.

---

## Part 2 of 2 — the passing criteria

MariaDB schema/plugin review, part 2 of 2: the passing criteria S1-S6 with the commands and output behind them. Verdict line and non-passing findings were in part 1 (NEEDS_WORK).

================================================================
S1 — temporal tree identity: PASS (exact)
================================================================

    diff -r schema/mysql/v8/temporal schema/mariadb/v11/temporal

reports differences in exactly two files, and in both the only hunk is the three cluster_membership columns:

    diff -r schema/mysql/v8/temporal/schema.sql schema/mariadb/v11/temporal/schema.sql
    367,369c367,374
    <     session_start        TIMESTAMP DEFAULT '1970-01-02 00:00:01',
    <     last_heartbeat       TIMESTAMP DEFAULT '1970-01-02 00:00:01',
    <     record_expiry        TIMESTAMP DEFAULT '1970-01-02 00:00:01',
    ---
    >     -- TIMESTAMP(6), not the plain TIMESTAMP that MySQL uses here: [5-line comment]
    >     session_start        TIMESTAMP(6) DEFAULT '1970-01-02 00:00:01',
    >     last_heartbeat       TIMESTAMP(6) DEFAULT '1970-01-02 00:00:01',
    >     record_expiry        TIMESTAMP(6) DEFAULT '1970-01-02 00:00:01',

    diff -r schema/mysql/v8/temporal/versioned/v1.0/schema.sql schema/mariadb/v11/temporal/versioned/v1.0/schema.sql
    251,253c251,258
    [same three columns]

    EXIT=1

Nothing else in the whole tree — no manifest, no versioned migration, no database.sql. (The same table is defined twice in the tree, hence two files for one three-column change.)

Additional file-identity checks:

    diff schema/mariadb/v11/visibility/schema.sql schema/mariadb/v11/visibility/versioned/v1.0/schema.sql
      -> IDENTICAL vis schema.sql == v1.0/schema.sql
    diff schema/mariadb/v11/temporal/database.sql schema/mysql/v8/temporal/database.sql
      -> IDENTICAL temporal database.sql
    diff schema/mariadb/v11/visibility/database.sql schema/mysql/v8/visibility/database.sql
      -> IDENTICAL vis database.sql

================================================================
S2 — install: PASS
================================================================

    ./temporal-sql-tool -u root --pw root --pl mariadb --db evalsch_tmp create
    ./temporal-sql-tool -u root --pw root --pl mariadb --db evalsch_tmp setup-schema -v 0.0
    ./temporal-sql-tool -u root --pw root --pl mariadb --db evalsch_tmp update-schema -d ./schema/mariadb/v11/temporal/versioned
    (same for evalsch_vis with ./schema/mariadb/v11/visibility/versioned)

Positive signal, not exit codes:

    === MariaDB evalsch_tmp version ===   (version_partition, curr_version, min_compatible_version)
    0	1.19	1.0
    === MariaDB evalsch_vis version ===
    0	1.0	1.0
    === table counts ===
    evalsch_tmp	40
    evalsch_vis	5
    === schema_update_history row counts ===
    evalsch_tmp: 21 rows
    evalsch_vis:  2 rows

    evalsch_tmp tables (40 = 38 + schema_version + schema_update_history):
    activity_info_maps buffered_events build_id_to_task_queue chasm_node_maps
    child_execution_info_maps cluster_membership cluster_metadata cluster_metadata_info
    current_chasm_executions current_executions executions history_immediate_tasks
    history_node history_scheduled_tasks history_tree namespace_metadata namespaces
    nexus_endpoints nexus_endpoints_partition_status queue queue_messages queue_metadata
    queues replication_tasks replication_tasks_dlq request_cancel_info_maps
    schema_update_history schema_version shards signal_info_maps signals_requested_sets
    task_queue_user_data task_queues task_queues_v2 tasks tasks_v2 timer_info_maps
    timer_tasks transfer_tasks visibility_tasks

    evalsch_vis tables (5 = 3 + 2):
    chasm_search_attributes custom_search_attributes executions_visibility
    schema_update_history schema_version

MySQL control (--pl mysql8 -p 3307, schema/mysql/v8) gives the same counts, 40 and 5, at curr_version 1.19 and 1.14.

Rather than trust the tool's exit code, I diffed the INSTALLED structures out of information_schema:

Columns (221 rows each; information_schema.columns excluding schema_version/schema_update_history). Raw diff shows only engine cosmetics — `int` vs `int(11)`, `bigint` vs `bigint(20)`, `int unsigned` vs `int(10) unsigned`, default-literal quoting (`Proto3` vs `'Proto3'`), and table-name sort order. After normalizing exactly those, the diff is THREE LINES:

    35c35
    < cluster_membership	last_heartbeat	7	timestamp	YES	1970-01-02 00:00:01
    ---
    > cluster_membership	last_heartbeat	7	timestamp(6)	YES	1970-01-02 00:00:01.000000
    37c37
    < cluster_membership	record_expiry	8	timestamp	YES	1970-01-02 00:00:01
    ---
    > cluster_membership	record_expiry	8	timestamp(6)	YES	1970-01-02 00:00:01.000000
    41c41
    < cluster_membership	session_start	6	timestamp	YES	1970-01-02 00:00:01
    ---
    > cluster_membership	session_start	6	timestamp(6)	YES	1970-01-02 00:00:01.000000

Nothing else. This independently corroborates S1 at the installed level.

Indexes (124 rows each; information_schema.statistics: table, index, seq_in_index, column, non_unique, sub_part, index_type):

    diff my_idx.txt ma_idx.txt
    -> NO INDEX DIFFERENCES IN TEMPORAL TREE

Embedded-schema path also works (newly wired in schema/embed.go):

    update-schema --schema-name mariadb/v11/visibility  -> curr_version 1.0,  5 tables
    update-schema --schema-name mariadb/v11/temporal    -> curr_version 1.19, 40 tables

================================================================
S3 — visibility semantic equivalence: PASS
================================================================

STATIC, column by column. I parsed both schema files and compared, for all three tables, every column's name / declared type / the set of $.paths its generating expression extracts / STORED-vs-VIRTUAL:

    === columns only in MySQL ===
    (none)
    === columns only in MariaDB ===
      ('executions_visibility', 'close_time_or_max') DATETIME(6) (COALESCE(close_time, '9999-12-31 23:59:59'))
    === type mismatches ===
    (none)
    === json path mismatches ===
    (none)
    === STORED/VIRTUAL mismatches ===
    (none)
    === counts === mysql 88 mariadb 89
    generated cols: mysql 59 mariadb 60

So: ZERO search-attribute columns missing, ZERO renamed, ZERO extracting a different path, ZERO type mismatches. All 59 MySQL generated columns have a counterpart. The single extra column is close_time_or_max — intentional, the expression-index replacement.

I checked that extra column cannot leak into results: the visibility store never does SELECT * on a real table, and never introspects information_schema (the only such query in the repo is PostgreSQL's listTablesQuery, sqlplugin/postgresql/admin.go:48). Column lists come from sqlplugin.DbFields, which getDbFields() derives by reflection from the VisibilityRow struct (common/persistence/sql/sqlplugin/visibility.go:227), so close_time_or_max is never selected and never inserted. Safe.

DYNAMIC, same JSON into both engines. 24 values in custom_search_attributes plus 48 in executions_visibility (3 rows x 16 predefined attributes) = 72 comparisons, 72 identical, 0 discrepancies.

custom_search_attributes (label | MySQL 8.0.29 | MariaDB 11.4.13):

    Bool01           1                                    1
    Bool02           0                                    0
    Bool03           <NULL>                               <NULL>        (absent)
    Int01            42                                   42
    Int02            -7                                   -7
    Int03            9007199254740993                     9007199254740993
    Double01         3.14159                              3.14159
    Double02         -0.50000                             -0.50000
    Double03         12345.67890                          12345.67890
    Keyword01        [simple]                             [simple]
    Keyword02        [with "quote" and \\ backslash]      [with "quote" and \\ backslash]
    Keyword03        [unicode é中]                         [unicode é中]
    Keyword04        []                                   []            (empty string)
    Keyword05        [123]                                [123]
    Keyword06        [<NULL>]                             [<NULL>]      (absent)
    Text01           [hello world foo]                    [hello world foo]
    Text02           []                                   []
    Text03           [<NULL>]                             [<NULL>]
    KeywordList01    ["a", "b", "c"]                      ["a", "b", "c"]
    KeywordList02    []                                   []
    KeywordList03    <NULL>                               <NULL>
    Datetime01       2025-01-15 00:21:20.700000           2025-01-15 00:21:20.700000   (input +08:00)
    Datetime02       2025-01-15 00:00:00.000000           2025-01-15 00:00:00.000000   (input Z)
    Datetime03       2025-07-01 05:29:59.999999           2025-07-01 05:29:59.999999   (input -05:30)

The KeywordList JSON columns render with identical spacing on both engines, so even byte-level comparison agrees.

executions_visibility, all 16 predefined attributes x 3 rows — every row identical. The one that matters most is the boolean, the only place the two schemas use genuinely different SQL (`search_attributes->"$.X"` vs `JSON_UNQUOTE(JSON_EXTRACT(...)) = 'true'`):

    absent   TemporalSchedulePaused   <NULL>   <NULL>
    false    TemporalSchedulePaused   0        0
    true     TemporalSchedulePaused   1        1

The rest all matched: TemporalChangeVersion ["v1", "v2"], BinaryChecksums ["abc"], BatcherUser batch@user, TemporalScheduledStartTime 2025-01-15 00:21:20.700000 (from a +08:00 input), TemporalScheduledById sched-1, TemporalNamespaceDivision div, BuildIds ["bid1"], TemporalPauseInfo ["p1"], TemporalReportedProblems ["prob"], TemporalWorkerDeploymentVersion wdv, TemporalWorkflowVersioningBehavior PINNED, TemporalWorkerDeployment dep, TemporalUsedWorkerDeploymentVersions ["u1", "u2"], TemporalExternalPayloadSizeBytes 123456789, TemporalExternalPayloadCount 7.

close_time_or_max itself is correct — closed rows get the real close time, the open row gets the sentinel:

    absent	close_time_or_max	2025-02-02 03:04:05.123456
    false	close_time_or_max	2025-02-02 03:04:05.123456
    true	close_time_or_max	9999-12-31 23:59:59.000000      (close_time was NULL)

That .000000 equals the '9999-12-31 23:59:59' literal the Go page token binds (maxDatetime has 0 nanos), so the pagination predicate still matches.

================================================================
S4 — dropped indexes: PASS (exact)
================================================================

MySQL 73 indexes, MariaDB 62. Dropped = 11, added = 0.

    === indexes ONLY in MySQL (dropped in MariaDB): 11 ===
      executions_visibility     by_temporal_change_version
      executions_visibility     by_binary_checksums
      executions_visibility     by_build_ids
      executions_visibility     by_temporal_pause_info
      executions_visibility     by_temporal_reported_problems
      executions_visibility     by_used_deployment_versions
      custom_search_attributes  by_keyword_list_01
      custom_search_attributes  by_keyword_list_02
      custom_search_attributes  by_keyword_list_03
      chasm_search_attributes   by_temporal_keyword_list_01
      chasm_search_attributes   by_temporal_keyword_list_02

    === indexes ONLY in MariaDB: 0 ===

    All dropped indexes contain ARRAY?:  (all 11) True

For the 62 shared indexes, after rewriting MySQL's inline
`(COALESCE(close_time, CAST('9999-12-31 23:59:59' AS DATETIME)))` to `close_time_or_max`, the name / column-list / FULLTEXT-flag comparison produces:

    === common indexes whose column list differs beyond COALESCE->close_time_or_max ===
    (none)

The header comment in schema/mariadb/v11/visibility/schema.sql lists the dropped index names; that list matches these 11 exactly.

EXPLAIN confirms the generated column actually restores index usability on MariaDB (the point of the whole close_time_or_max design, so worth a positive signal rather than an assumption):

    EXPLAIN SELECT run_id FROM executions_visibility WHERE namespace_id='ns1'
      ORDER BY close_time_or_max DESC, start_time DESC, run_id LIMIT 20;
    -> key: default_idx, key_len 256, Extra: Using where; Using index

    EXPLAIN ... WHERE namespace_id='ns1' AND workflow_id='wf' ORDER BY ...
    -> key: by_workflow_id, key_len 1278, Extra: Using where; Using index

================================================================
S5 — MySQL behaviour preserved by the dialect refactor: PASS
================================================================

I extracted each moved method from 9ab3a9f77 and from 54d548e51 via `git show`, and compared the bodies with only the receiver clause normalized:

    ===== common/persistence/sql/sqlplugin/mysql/query_converter.go
      GetCoalesceCloseTimeExpr:          DIFFERS (constant extraction only, see below)
      ConvertKeywordListComparisonExpr:  IDENTICAL body (receiver normalised)
      buildJSONOverlapsExpr:             IDENTICAL body (receiver normalised)
      GetDatetimeFormat:                 DIFFERS (constant extraction only)
    ===== common/persistence/visibility/store/sql/query_converter_legacy_mysql.go
      getCoalesceCloseTimeExpr:          DIFFERS (constant extraction only)
      convertKeywordListComparisonExpr:  IDENTICAL body (receiver normalised)
      convertToJsonOverlapsExpr:         IDENTICAL body (receiver normalised)
      getDatetimeFormat:                 DIFFERS (constant extraction only)

The four "DIFFERS" are all the same single substitution:

      -  return "2006-01-02 15:04:05.999999"
      +  return visibilityDatetimeFormat            (resp. mysqlDatetimeFormat)

      -  Value: query.NewUnsafeSQLString(maxDatetime.Format(c.GetDatetimeFormat())),
      +  Value: query.NewUnsafeSQLString(maxDatetime.Format(visibilityDatetimeFormat)),

Both new constants are defined as exactly that literal
(`const visibilityDatetimeFormat = "2006-01-02 15:04:05.999999"`,
 `const mysqlDatetimeFormat = "2006-01-02 15:04:05.999999"`),
and the old `c.GetDatetimeFormat()` returned exactly that literal. So the emitted SQL strings are byte-identical.

Non-circular confirmation: the test diffs change ONLY construction (`&queryConverter{}` -> `&queryConverter{mysqlDialect{}}`, `&mysqlQueryConverter{}` -> `&mysqlQueryConverter{mysqlDialect{}}`). No expected SQL string was edited — e.g. `"coalesce(close_time, cast('9999-12-31 23:59:59' as datetime))"`, `"'foo' member of (KeywordList01)"` and `"json_overlaps(KeywordList01, cast('[\"foo\",\"bar\"]' as json))"` are untouched, so they predate the refactor and are a real check:

    go test ./common/persistence/sql/sqlplugin/mysql/... -run 'TestQueryConverter' -count=1
    ok  go.temporal.io/server/common/persistence/sql/sqlplugin/mysql	0.459s

    go test ./common/persistence/visibility/store/sql/... -run 'TestMySQLQueryConverterSuite|TestMariaDBQueryConverterSuite|TestPostgreSQL|TestSQLite' -count=1
    ok  go.temporal.io/server/common/persistence/visibility/store/sql	0.440s

No non-MySQL plugin changed:

    git diff --name-only 9ab3a9f77..54d548e51 | grep -E "postgresql|sqlite"
    -> NONE

One shared file WAS touched and deserves flagging as checked rather than missed: common/persistence/sql/sqlplugin/tests/history_execution_chasm.go replaced a `strings.Contains(strings.ToLower(s.T().Name()), "mysql")` skip with an explicit `WithDoubleCountedUpdatedRows()` option. Call sites:

    common/persistence/tests/mysql_test.go:640      ... WithDoubleCountedUpdatedRows()
    common/persistence/tests/mariadb_test.go:645    ... WithDoubleCountedUpdatedRows()
    common/persistence/tests/postgresql_test.go:612 (no option)
    common/persistence/tests/sqlite_test.go:1047    (no option)
    common/persistence/tests/sqlite_test.go:1459    (no option)

No Postgres/SQLite test name contains "mysql", so their behaviour is unchanged, and the skip is now explicit rather than name-based. This is a strengthening, not a regression or a weakened assertion.

================================================================
S6 — MySQL-8-only SQL reachable by MariaDB: PASS, none found
================================================================

Swept common/persistence/sql/sqlplugin/mysql/ (21 non-test .go files plus session/) for: `->` / `->>`, MEMBER OF, CAST(... AS JSON), JSON_TABLE, CHAR(255) ARRAY, window functions / OVER ( / ROW_NUMBER / RANK / PARTITION BY, SKIP LOCKED, NOWAIT, FOR SHARE, utf8mb4_0900 collations, CTE / WITH ... AS (, RECURSIVE, LATERAL, RETURNING, JSON_VALUE, INSERT IGNORE, the MySQL-8 `... AS new` row-alias form of ON DUPLICATE KEY UPDATE, INVISIBLE indexes, ANY_VALUE, ALGORITHM=, generated-column DDL.

Results:

  - `->` and `->>`: ZERO hits anywhere in the package (non-test and test).
  - `member of` and convertTypeJSON: present, but only inside
      mysqlDialect.ConvertKeywordListComparisonExpr  (query_converter.go:114)
      mysqlDialect.buildJSONOverlapsExpr             (query_converter.go:289)
    MariaDB never reaches these — it is wired to mariaDBDialect at both dispatch points:
      mysql/mariadb.go init(): sql.RegisterPlugin(PluginNameMariaDB, &plugin{flavor: mariaDBFlavor, queryConverter: &queryConverter{mariaDBDialect{}}})
      query_converter_legacy_factory.go:25: case mysql.PluginNameMariaDB -> newMariaDBQueryConverter
  - Everything else on the list: ZERO hits.
  - Present but supported by both engines: `ON DUPLICATE KEY UPDATE x = VALUES(x)` (10 sites — the deprecated-in-8.0.20 form, which MariaDB supports), `LOCK IN SHARE MODE` (2 sites: execution.go:32, shard.go:23).
  - The only utf8mb4 hit is admin.go:37 `CREATE DATABASE IF NOT EXISTS %v CHARACTER SET utf8mb4` — no 0900 collation named. See part 1 finding 2 for why that line still matters.

I then EXECUTED the forms each dialect actually emits — MariaDB's against MariaDB, MySQL's against MySQL — over the schemas loaded in S3. Identical on all comparable cases:

    case                       MySQL 8 (member of / cast as json)   MariaDB 11.4 (json_contains / string literal)
    hit                        1                                    1
    miss                       0                                    0
    on empty array []          0                                    0
    on NULL document           <NULL>                               <NULL>
    json_overlaps hit          1                                    1
    json_overlaps miss         0                                    0
    json_overlaps on NULL      <NULL>                               <NULL>
    negated (not ...)          1                                    1
    MATCH..AGAINST hit         1                                    1
    MATCH..AGAINST miss        0                                    0

The MATCH ... AGAINST ... IN NATURAL LANGUAGE MODE case matters because ConvertTextComparisonExpr is SHARED, not dialect-split — MariaDB runs the identical SQL, and it works against the FULLTEXT index on the STORED generated TEXT column.

The full BuildSelectStmt shape asserted in mariadb_test.go — both `LEFT JOIN ... USING (namespace_id, run_id)` joins, the 3-clause page-token predicate, `ORDER BY close_time_or_max DESC, start_time DESC, run_id LIMIT` — executes on MariaDB with no error.

Audited every plugin-name dispatch site for a missing mariadb case:

    grep -rn "mysql\.PluginName\b|mysql\.PluginNameMariaDB" --include='*.go' .
    tests/testcore/flag.go:27                          handles PluginNameMariaDB
    common/persistence/visibility/defs.go:28           handles PluginNameMariaDB
    .../query_converter_legacy_factory.go:23,25        handles PluginNameMariaDB
    .../persistence-tests/persistence_test_base.go:156,168  handles PluginNameMariaDB
    .../persistence-tests/setup.go:42,44,74,88         handles PluginNameMariaDB
    tools/sql/main.go:74                               default flag value only, fine

None missed.

    go build ./...  -> BUILD OK
    go vet ./schema/... ./common/persistence/sql/sqlplugin/mysql/... ./common/persistence/visibility/...  -> clean

End of part 2.

---

## Disposition

See `evidence/verdicts/06-verdicts-04-05-followup.md`.
