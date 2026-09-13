# Follow-up to verdict 01

Verdict 01 (`evidence/verdicts/01-schema-and-plugin.md`) returned **NEEDS_WORK** against
commit `0f09df5f8`. This records what was re-run afterwards, at commit `ef974ea86`.

## C5 (blocking) — was real, already fixed in the next commit

The evaluator was dispatched against `0f09df5f8` only. The defect it found was hit
independently while building the next commit and fixed there:

```
$ git log --oneline -1 -- common/persistence/visibility/store/sql/query_converter_legacy_mysql_test.go
54d548e51 Cover MariaDB with the full persistence suite, and fix what it caught

$ grep -rn "mysqlQueryConverter{" --include="*.go" .
common/persistence/visibility/store/sql/query_converter_legacy_mysql.go:91:		&mysqlQueryConverter{mysqlDialect{}},
common/persistence/visibility/store/sql/query_converter_legacy_mysql_test.go:21:			pqc: &mysqlQueryConverter{mysqlDialect{}},
common/persistence/visibility/store/sql/query_converter_legacy_mariadb.go:34:		&mysqlQueryConverter{mariaDBDialect{}},
common/persistence/visibility/store/sql/query_converter_legacy_mariadb_test.go:25:			pqc: &mysqlQueryConverter{mariaDBDialect{}},
```

All four construction sites pass a dialect; none leaves the embedded interface nil.

Re-ran the command the verdict asked for:

```
$ go test ./common/persistence/sql/... ./common/persistence/visibility/... -count=1
EXIT=0
ok  go.temporal.io/server/common/persistence/sql/sqlplugin
ok  go.temporal.io/server/common/persistence/sql/sqlplugin/mysql
ok  go.temporal.io/server/common/persistence/sql/sqlplugin/mysql/session
ok  go.temporal.io/server/common/persistence/sql/sqlplugin/postgresql
ok  go.temporal.io/server/common/persistence/sql/sqlplugin/postgresql/session
ok  go.temporal.io/server/common/persistence/sql/sqlplugin/sqlite
ok  go.temporal.io/server/common/persistence/visibility
ok  go.temporal.io/server/common/persistence/visibility/store/elasticsearch
ok  go.temporal.io/server/common/persistence/visibility/store/elasticsearch/client
ok  go.temporal.io/server/common/persistence/visibility/store/query
ok  go.temporal.io/server/common/persistence/visibility/store/sql
ok  go.temporal.io/server/common/persistence/visibility/store/tests
```

And the specific suite it named, plus the MariaDB mirror of it, verbosely:
`TestMySQLQueryConverterSuite` and `TestMariaDBQueryConverterSuite` both PASS, including
`TestConvertColName`, `TestConvertKeywordListComparisonExpr` and
`TestGetCoalesceCloseTimeExpr` — the three the verdict listed as failing.

The evaluator's framing of the mistake is worth keeping: "unchanged" was cited as
evidence of no regression when an unchanged file was the *cause* of one.

## C6 (wording) — accepted, re-probed, corrected

Checked independently before editing rather than taking the verdict's word:

```
CREATE TABLE g1 (id INT PRIMARY KEY, ct DATETIME(6) NULL,
  m DATETIME(6) GENERATED ALWAYS AS (COALESCE(ct, CAST('9999-12-31 23:59:59' AS DATETIME))));
[CREATE ACCEPTED]
 id | ct                         | m
  1 | NULL                       | 9999-12-31 23:59:59.000000
  2 | 2026-02-02 03:04:05.000000 | 2026-02-02 03:04:05.000000

CREATE INDEX i_g1 ON g1(m DESC);
ERROR 1901 (HY000): Function or expression 'coalesce(`ct`,cast('9999-12-31 23:59:59' as datetime))'
cannot be used in the GENERATED ALWAYS AS clause of `m`
```

The verdict is right: the `CREATE TABLE` is accepted and the column computes correct
values; only the index is rejected. PROGRESS.md now says so.

## Minor (stale plan note) — corrected

PROGRESS.md's recon note listed five plugin registration sites. Only two needed
changing; the other three already import `sqlplugin/mysql`, and `mariadb.go` registers
from that package's `init()`. The note now says which were actually touched and why the
rest needed nothing, so it does not read as an unfinished item.
