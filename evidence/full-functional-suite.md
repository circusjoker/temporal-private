# Full functional suite, MariaDB vs MySQL

The claim being tested is the user's bar: *all* functionality works, not just the three
visibility suites checked earlier. `tests/` is 115 files; this runs all of it.

```
CGO_ENABLED=0 go test ./tests/ -tags disable_grpc_modules,test_dep \
  -persistenceType=sql -persistenceDriver=<driver> -count=1 -v -timeout 180m
```

Both engines had `--max-connections=1000`; see the connection note below for why.

## Result

| | suites PASS | suites FAIL | subtests PASS | subtests FAIL | subtests SKIP | wall |
|---|---|---|---|---|---|---|
| MariaDB 11.4.13 (3307) | 133 | 9 | 3294 | 51 | 38 | 999s |
| MySQL 8.0.29 (3306) | 130 | 12 | 3270 | 75 | 38 | 1117s |

**MySQL failed more than MariaDB.** Failure sets: 29 fail on both, 34 only on MySQL,
13 only on MariaDB.

## Are the 13 MariaDB-only failures real?

No. Re-running the five suites containing them against MariaDB:

- of the 13, **1** reproduced
- **15 new** failures appeared that had passed the first time
  (the whole `TestActivityParityTestSuite/TestMetrics` group, among others)

The failure set is not stable between runs of the same code against the same database, so
it does not identify anything about MariaDB. The one that reproduced,
`TestVersioningFunctionalSuite/TestWorkflowTaskRedirectInRetryNonFirstTask/ForceTaskForwardForcePollForwardForceAsync`,
is a task-forwarding/timing test and also had siblings failing on MySQL.

Several of the failures are structurally suspicious in the same way: a parent test is
marked FAIL while **all of its children PASS** (e.g.
`TestActivityParityTestSuite/TestCancel`), which points at the suite's own teardown rather
than at anything a storage layer did.

## What this does and does not establish

It establishes that MariaDB is not worse than MySQL across the whole functional surface on
this machine, which is the comparison that matters — a bare pass count would not have
been meaningful, since MySQL does not get a clean run here either.

It does **not** establish that the suite is green, on either engine. This is a laptop
running two databases, a server and the suite at once; the timing-sensitive tests suffer.
A CI machine would be the place to get a clean baseline.

## Connection limits — a real finding

The first attempt failed entirely with `no usable database connection found`. MariaDB's
default `max_connections` is 151; the suite drove `Max_used_connections` to 152 and
`Connection_errors_max_connections` to 37.

Each Temporal host holds `maxConns` + the visibility store's `maxConns` — 20 + 2 in
`config/development-mariadb.yaml` — and connections are per host process, not per cluster.
`develop/docker-compose` now passes `--max-connections=1000`. **Size this deliberately for
a real deployment**: it is the first thing that breaks when host count grows.
