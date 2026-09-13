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

## In progress
- [ ] 05 Server boots with MariaDB as default+visibility store; workflow start→complete
- [ ] 06 Search-attribute / visibility query coverage (list, filter, order, count)
- [ ] 07 Web UI operates (list, detail, history, search)
- [ ] 08 Official agentic AI sample runs to completion
- [ ] 09 Evaluator verdicts saved under `evidence/verdicts/`

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
