# Acceptance re-run (post-verdict) — 2026-09-13 17:58:06 CST

Containers: MariaDB 3307, MySQL 3306 (control). Server via 'make start-mariadb'.

## MariaDB is the single store
  defaultStore: mariadb-default
  visibilityStore: mariadb-visibility
        pluginName: "mariadb"
        connectAddr: "127.0.0.1:3307"
        pluginName: "mariadb"
        connectAddr: "127.0.0.1:3307"

server DB sockets by port:   33 ->127.0.0.1:3307 

## collation now pinned NO PAD (the verdict-05 fix)
temporal	utf8mb4_uca1400_nopad_ai_ci
temporal_visibility	utf8mb4_uca1400_nopad_ai_ci

## schema versions
temporal	1.19
temporal_visibility	1.0

## health
SERVING

## visibility queries (negative cases must return 0)
  MdbKeyword = 'mdb-keyword'                   => 1
  MdbBool = true                               => 1
  MdbBool = false                              => 0
  MdbKeywordList = 'alpha'                     => 1
  MdbKeywordList IN ('beta','zzz')             => 1
  MdbKeywordList = 'nothere'                   => 0
  MdbText = 'brave'                            => 1
  MdbInt = 42                                  => 1
  MdbDatetime = '2026-09-13T05:06:07Z'         => 1

## web UI API
  list count: 2
  history events (mariadb-smoke-1): 30

## agentic AI sample, both variants, on this MariaDB server
  litellm-gpt-oss-workflow-id    WORKFLOW_EXECUTION_STATUS_COMPLETED 29 events
  mdb-async-1                    WORKFLOW_EXECUTION_STATUS_COMPLETED 17 events

  unmodified sample final output (tool fails -- openai-agents 0.19.4, not the store):
    Result: Clouds veil bright Tokyo  
    Tool fails, silence fills deep air  
    Rain or sun unknown.

  async-tool variant, tool call recorded in MariaDB history:
    tool output: The weather in Tokyo is sunny.
