# Acceptance re-run — 2026-09-13 17:42:30 CST

Server started with 'make start-mariadb' (config/development-mariadb.yaml).

## config: MariaDB is the single store
  defaultStore: mariadb-default
  visibilityStore: mariadb-visibility
        pluginName: "mariadb"
        databaseName: "temporal"
        pluginName: "mariadb"
        databaseName: "temporal_visibility"

## other stores configured? (expect none)
0

## cluster health
SERVING

## rows actually in MariaDB
executions	38
history_node	245
executions_visibility	38
custom_search_attributes	38

## fresh workflow through the running server
QUERY before signal: 0
RESULT: Hello, MariaDB! doubled=42 nudges=1
STATUS: COMPLETED
smoke exit: see RESULT/STATUS lines above

## visibility queries (rows matched)
  MdbKeyword = 'mdb-keyword'                     => 3
  MdbInt = 42                                    => 3
  MdbDouble = 1.5                                => 4
  MdbBool = true                                 => 5
  MdbBool = false                                => 1
  MdbDatetime = '2026-09-13T05:06:07Z'           => 3
  MdbKeywordList = 'alpha'                       => 4
  MdbKeywordList IN ('beta','zzz')               => 5
  MdbKeywordList = 'nothere'                     => 0
  MdbText = 'brave'                              => 4

## web UI API against the MariaDB-backed server
  namespaces: ['temporal-system', 'default']
  workflow list count: 38
  history events for mariadb-smoke-1: 30

## agentic AI sample history still in MariaDB
  litellm-gpt-oss-workflow-id  WORKFLOW_EXECUTION_STATUS_COMPLETED 35 events
  mdb-async-1                  WORKFLOW_EXECUTION_STATUS_COMPLETED 17 events
