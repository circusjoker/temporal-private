# MariaDB schema

`schema/mariadb/v10` is the schema for the `mariadb10` SQL plugin
(`common/persistence/sql/sqlplugin/mariadb`). It is kept at version parity with
`schema/mysql/v8` — `Version` and `VisibilityVersion` in `version.go` intentionally match
the MySQL values so that future schema changes stay in lockstep across the two plugins.

## temporal/

Byte-identical to `schema/mysql/v8/temporal`, with one exception:

- `cluster_membership.session_start`, `last_heartbeat` and `record_expiry` are
  `TIMESTAMP(6)` instead of `TIMESTAMP`. MariaDB **truncates** sub-second values when
  storing into a fractionless `TIMESTAMP`/`DATETIME`, whereas MySQL **rounds** them, and
  there is no `sql_mode` toggle for this. Without the `(6)` these columns drift backwards
  by up to a second. Every other time column in the schema is already `(6)`.

## visibility/

A MariaDB-dialect rewrite of `schema/mysql/v8/visibility/schema.sql`. The differences all
come from MariaDB SQL features that MySQL 8 has and MariaDB 11.4 does not:

| MySQL 8 construct | MariaDB replacement | Why |
| --- | --- | --- |
| `search_attributes->'$.X'` / `->>'$.X'` in generated columns | `JSON_EXTRACT(...)` / `JSON_VALUE(...)` | MariaDB does not implement the `->` / `->>` operators at all (any version) — they are a syntax error. |
| Functional index on `COALESCE(close_time, CAST('9999-12-31 23:59:59' AS DATETIME))` | Persistent generated column `close_time_or_max`, indexed as an ordinary column | MariaDB has no expression/functional indexes. Every visibility sort key and page-token predicate uses this column instead, so the queries stay index-backed. Note `CAST(x AS DATETIME)` is rejected inside a `GENERATED ALWAYS AS` clause, so the default is written as a plain string literal. |
| Multi-valued indexes, e.g. `CAST(KeywordList01 AS CHAR(255) ARRAY)` | index omitted | MariaDB has no multi-valued indexes. JSON-array search attributes (KeywordList01-03, BuildIds, BinaryChecksums, TemporalChangeVersion, TemporalPauseInfo, TemporalReportedProblems, TemporalUsedWorkerDeploymentVersions) remain **correct** via `JSON_CONTAINS` / `JSON_OVERLAPS` but are not index-backed. This is a known performance gap versus MySQL, not a correctness gap. |
| Virtual generated `TEXT` columns under a `FULLTEXT` index | the same columns made `PERSISTENT` | MariaDB can only build a `FULLTEXT` index over a stored column. |
| JSON booleans compared against `'true'` | `JSON_VALUE(...) IN ('true','1')` | MariaDB's `JSON_VALUE` renders a JSON boolean as `1`/`0`. The `IN` form matches both renderings and still yields `NULL` when the attribute is absent, which is what the `IS NULL` visibility queries depend on. |

The matching query-converter adaptations live in
`common/persistence/sql/sqlplugin/mariadb/query_converter.go` and
`common/persistence/visibility/store/sql/query_converter_legacy_mariadb.go`
(`json_contains(col, json_quote(v))` for MySQL's `v MEMBER OF (col)`, and
`json_overlaps(col, '[...]')` for `json_overlaps(col, cast('[...]' as json))` — MariaDB has
neither `MEMBER OF` nor `CAST(... AS JSON)`).

## visibility/versioned/

Only `v1.14` exists, as a squashed initial migration. MySQL's v1.0–v1.13 history uses
MySQL-only DDL and no MariaDB database has ever existed at an intermediate version, so
replaying that history would be both impossible and pointless.
`tools/common/schema` imposes no contiguity requirement on version directories.

When adding a new migration here, keep the manifest `Description` under 255 characters: it
is inserted verbatim into `schema_update_history.description VARCHAR(255)`, and an
overlong value makes `update-schema` apply all the DDL and then fail with
`Error 1406 (22001): Data too long`.
