# 資料模型與持久層導覽（繁體中文）

> 系列第三篇。[專案總覽](./project-overview.zh-TW.md) 回答「這專案在幹嘛」，
> [請求流程導覽](./request-flow.zh-TW.md) 回答「一個請求在程式碼裡怎麼跑」，
> 本文回答 **「狀態到底存在哪裡、長什麼樣、怎麼保證一致」**。
>
> 本文所有 `檔案:行號` 皆對應 commit 當下的程式碼，已逐一驗證。

---

## 0. 一分鐘版本

Temporal Server 本身**沒有狀態**，所有狀態都在資料庫裡。一個 Workflow Execution 的狀態被拆成三份存放：

| 存什麼 | 存在哪 | 性質 |
| --- | --- | --- |
| **History Events** | `history_node` / `history_tree` | append-only，事實來源（source of truth） |
| **Mutable State** | `executions` | 由事件推導出的「目前狀態快照」，每次交易條件式覆寫 |
| **內部佇列任務** | `history_immediate_tasks` / `history_scheduled_tasks`（及舊版分表） | server 給自己的待辦事項，處理完就刪 |

外加一份 `current_executions`，回答「這個 Workflow ID 現在跑的是哪個 Run ID」。

而所有這些列的第一個 key 都是 **`shard_id`** —— 這是整個系統擴展性與一致性的軸心。

---

## 1. 三層抽象：Manager → Store → Plugin

`common/persistence/` 的分層：

```
service/history, service/matching, ...
        │  呼叫的是 XxxManager 介面（吃 proto 物件）
        ▼
common/persistence/*.go            ← Manager 層：序列化/反序列化、驗證、統計、拆合
   execution_manager.go, history_manager.go, shard_manager.go, metadata_manager.go...
        │  轉成 Internal*Request（欄位是 DataBlob）
        ▼
common/persistence/cassandra/  |  common/persistence/sql/   ← Store 層：實際 CQL / SQL
        │                                    │
        ▼                                    ▼
   gocql                          common/persistence/sql/sqlplugin/
                                     mysql / postgresql / sqlite
```

Manager 層之外還套了幾層裝飾器，都在 `common/persistence/` 根目錄：
`persistence_retryable_clients.go`（重試）、`persistence_rate_limited_clients.go`（限流）、
`persistence_metric_clients.go`（metrics）、以及測試用的 `faultinjection/`。

**閱讀技巧**：想知道某個操作真正寫了什麼 SQL，路徑固定是
`common/persistence/<X>_manager.go` → `common/persistence/sql/<x>.go` → `sqlplugin/<db>/<x>.go`。

---

## 2. Shard：一致性與擴展性的軸心

### 2.1 怎麼決定一個 Workflow 屬於哪個 shard

```go
// common/util.go:418
func WorkflowIDToHistoryShard(namespaceID string, workflowID string, numberOfShards int32) int32 {
	idBytes := []byte(namespaceID + "_" + workflowID)
	hash := farm.Fingerprint32(idBytes)
	return int32(hash%uint32(numberOfShards)) + 1 // ShardID starts with 1
}
```

- `numberOfShards` 來自靜態設定 `numHistoryShards`（`common/config/config.go:266`）。
  它決定了所有資料的 partition key，**叢集建立後改不動**——而且這是由程式碼強制的：
  啟動時會拿設定值和 `cluster_metadata_info` 裡存下來的 `HistoryShardCount` 比對，
  不一致就**印一行 warning 然後直接用 DB 裡的值覆蓋你的設定**（`temporal/fx.go:876`）。
  換句話說改 yaml 不會有任何效果。
- shard 編號**從 1 開始**，不是 0。
- 一個 shard 在同一時間只被**一台 history host** 擁有；誰擁有誰，由 `common/membership/` 的 ring 決定。

### 2.2 `range_id`：用單調遞增租約做 fencing

`shards` 表只有三個有意義的欄位（`schema/mysql/v8/temporal/schema.sql:21`）：

```sql
CREATE TABLE shards (
  shard_id INT NOT NULL,
  range_id BIGINT NOT NULL,          -- 租約號碼
  data MEDIUMBLOB NOT NULL,          -- ShardInfo：各佇列的 ack level 等
  data_encoding VARCHAR(16) NOT NULL,
  PRIMARY KEY (shard_id)
);
```

一台 history host 接手 shard 時，會做 `range_id + 1` 的**條件式更新**
（`service/history/shard/context_impl.go:1164` `renewRangeLocked`，帶 `PreviousRangeID`）。
此後這台 host 對此 shard 的**每一次寫入都帶著自己的 `range_id`**：

- SQL：`readLockShard(ctx, tx, shardID, rangeID)`（`common/persistence/sql/execution.go:49`，
  實作在 `common/persistence/sql/shard.go:152`）在同一個交易裡先鎖 shard 列並比對 range_id。
- Cassandra：所有更新語句都帶 `IF range_id = ?`（`common/persistence/cassandra/mutable_state_store.go:31`）。

比對失敗 → `ShardOwnershipLostError`（`common/persistence/data_interfaces.go:148`），
舊 owner 立刻放棄 shard。**這就是為什麼 Temporal 不需要分散式鎖**：
腦裂時舊 owner 的寫入會被資料庫本身擋掉。

### 2.3 Task ID 也是從 range 切出來的

```go
// service/history/shard/task_key_generator.go:152
a.nextTaskID = rangeID << a.rangeSizeBits
a.exclusiveMaxTaskID = (rangeID + 1) << a.rangeSizeBits
```

`rangeSizeBits` 預設 20（`service/history/configs/config.go:558`），也就是每個 range 可發出 2^20 ≈ 一百萬個 task ID。
用完就 renew range（`range_id + 1`）。好處：

1. Task ID 在 shard 內**全域單調遞增**，不需要每次寫入都去 DB 拿號碼（一次拿一百萬個放在記憶體）。
2. 換 owner 後新 host 的 task ID 一定大於舊 host 發過的所有 ID，佇列游標不會倒退。

---

## 3. 一個 Workflow Execution 的四份資料

### 3.1 `current_executions` — workflowID 的「現在哪一個 run」

`schema/mysql/v8/temporal/schema.sql:46`，主鍵是 `(shard_id, namespace_id, workflow_id)`（**沒有 run_id**）。
這是 workflow ID 唯一性的執行點：`WorkflowIDReusePolicy`、`de-dup`、conflict policy 全靠對這一列做條件式更新來判定
（對應 `CurrentWorkflowConditionFailedError`，也就是 [請求流程導覽](./request-flow.zh-TW.md) 提到的 `handleConflict` 路徑）。

### 3.2 `executions` — Mutable State

`schema/mysql/v8/temporal/schema.sql:30`，主鍵 `(shard_id, namespace_id, workflow_id, run_id)`：

```sql
next_event_id     BIGINT,     -- 下一個事件編號
data / state      MEDIUMBLOB, -- WorkflowExecutionInfo / WorkflowExecutionState（proto）
db_record_version BIGINT,     -- 樂觀鎖版本號
```

內容的權威定義在 proto：

- `proto/internal/temporal/server/api/persistence/v1/workflow_mutable_state.proto:13` — `WorkflowMutableState`，
  聚合了 `activity_infos`、`timer_infos`、`child_execution_infos`、`request_cancel_infos`、
  `signal_infos`、`chasm_nodes`、`signal_requested_ids`、`buffered_events`。
- `proto/internal/temporal/server/api/persistence/v1/executions.proto:56` — `WorkflowExecutionInfo`（近千行的大結構，
  放 parent 資訊、retry/cron 設定、各佇列的 transition 資訊、version histories…）。
- 同檔 `:385` — `WorkflowExecutionState`（run id、state、status、create_request_id）。

在 MySQL/Postgres 中，那些 map 欄位**各自拆成一張表**：`activity_info_maps`、`timer_info_maps`、
`child_execution_info_maps`、`request_cancel_info_maps`、`signal_info_maps`、`signals_requested_sets`、
`chasm_node_maps`、`buffered_events`（`schema.sql:226` 起）；讀取 Mutable State 時要把它們組回一個 proto。

**心智模型**：Mutable State ＝ 重播歷史後的結果快取 ＋ 尚未寫入歷史的暫存（buffered events）。
它可以從歷史重建，但重建很貴，所以它是被持久化的。

### 3.3 `history_node` / `history_tree` — 事件本體

```sql
-- schema/mysql/v8/temporal/schema.sql:312
CREATE TABLE history_node (
  shard_id, tree_id, branch_id, node_id, txn_id,   -- 主鍵
  prev_txn_id, data, data_encoding
);
-- schema/mysql/v8/temporal/schema.sql:326
CREATE TABLE history_tree (shard_id, tree_id, branch_id, data, data_encoding);
```

- **一個 node ＝ 一個事件批次（batch）**，不是一個事件。`node_id` 是該批次第一個事件的 event ID。
- **tree / branch**：歷史是一棵樹而不是一條線，因為有 **Reset** 與 XDC 衝突解決。
  Reset 會從某個點 fork 出新 branch（`common/persistence/history_manager.go:49` `ForkHistoryBranch`），
  共用 fork 點之前的 node，不複製資料。
- **branch token** 是編碼過的 `(tree_id, branch_id, ancestors[])`，存在 Mutable State 的 version histories 裡；
  讀歷史時一律先 `ParseHistoryBranchInfo`（`history_manager.go:136`）。
- `txn_id` + `prev_txn_id` 讓同一個 `node_id` 可以有多筆（寫入重試留下的垃圾），讀取時靠鏈結挑出有效的那條。

### 3.4 內部佇列任務

History 服務給自己排的待辦事項，共七個 category（`service/history/tasks/category.go:21`）：

| ID | Category | 型別 | 做什麼 |
| --- | --- | --- | --- |
| 1 | `transfer` | Immediate | 需要呼叫其他服務的動作：推任務給 matching、啟動 child workflow、送 cancel/signal… |
| 2 | `timer` | Scheduled | 所有時間相關：使用者 timer、activity/workflow task timeout、retry backoff、workflow 保留期到期 |
| 3 | `replication` | Immediate | XDC：把事件複寫到其他叢集 |
| 4 | `visibility` | Immediate | 同步 open/close/upsert 到 visibility store（含 Elasticsearch） |
| 5 | `archival` | Scheduled | 歷史/visibility 歸檔 |
| 6 | `memory-timer` | Scheduled | **只存在記憶體**、不落地的短期 timer（見 `docs/architecture/in-memory-queue.md`） |
| 7 | `outbound` | Immediate | Nexus 等對外呼叫 |

- **Immediate**：key 只有 `task_id`，馬上處理 → 存 `history_immediate_tasks`（`schema.sql:158`）。
- **Scheduled**：key 是 `(visibility_timestamp, task_id)`，到點才處理 → 存 `history_scheduled_tasks`（`schema.sql:168`）。

兩張表都帶 `category_id` 欄位，**新增 category 不需要改 schema**。
`transfer_tasks` / `timer_tasks` / `replication_tasks` / `visibility_tasks`（`schema.sql:179` 起）是舊版的專用表，
仍為相容保留。任務處理完就刪除；卡住的任務會被送進 DLQ（見 §6）。

---

## 4. 一次寫入到底發生什麼事（最重要的一節）

以「Workflow Task 完成，產生新事件 + 新 activity task」為例：

```
service/history/workflow/context.go:1044
    NewTransaction(shardContext).UpdateWorkflowExecution(...)
        │
        ▼ service/history/workflow/transaction_impl.go:167
    組出 persistence.UpdateWorkflowExecutionRequest
    （ShardID / Mode / UpdateWorkflowMutation / UpdateWorkflowEvents / NewWorkflowSnapshot）
        │  RangeID 由 shard context 填入
        ▼ common/persistence/execution_manager.go:168
    serializeWorkflowEventBatches：事件批次 → InternalAppendHistoryNodesRequest
    SerializeWorkflowMutation：Mutable State → DataBlob
        │
        ▼ common/persistence/sql/execution.go:334
    // first append history      ← :338 先寫 history_node（不在交易內！）
    // then update mutable state ← :350 再開交易
        txExecuteShardLocked(shardID, rangeID)     :40
            readLockShard(range_id 比對)            :49
            更新 executions / current_executions / 各 *_maps / 寫入 tasks
```

### 兩個必須理解的設計決定

**(1) 事件先寫、狀態後寫，而且事件不在同一個交易裡。**
程式碼註解就寫得很直白（`common/persistence/sql/execution.go:338` 與 `:350`）。
因此可能出現「事件寫進去了，但 Mutable State 更新失敗」的孤兒 node。
處理方式是**事後清掉**：條件式更新失敗時（`CurrentWorkflowConditionFailedError` /
`WorkflowConditionFailedError` / `ConditionFailedError`）呼叫 `trimHistoryNode`
（`common/persistence/execution_manager.go:266`，實作在 `:1081`）——
它重讀 Mutable State，拿到目前有效的 branch token，把超出 `next_event_id` 的 node 剪掉。
註解標明這是 **best effort**：就算 trim 失敗也沒關係，因為讀歷史時是以 Mutable State 的
`next_event_id` 為界，孤兒 node 永遠不會被讀到。

**(2) 條件式更新是雙保險。**
`db_record_version`（Mutable State 樂觀鎖，`common/persistence/data_interfaces.go:377`）擋的是
「同一台 host 上的併發修改」；`range_id` 擋的是「另一台 host 以為自己還擁有這個 shard」。
兩者缺一不可。

### Cassandra 的做法不同，但目的相同

Cassandra 把 **shard 列、execution 列、以及所有 task 列放在同一張 `executions` 表**
（`schema/cassandra/temporal/schema.cql:7`），靠 `type` 欄位當 row type 區分
（註解就寫著 `enum RowType { Shard, Execution, TransferTask, TimerTask, ReplicationTask, VisibilityTask }`）。

這不是偷懶，而是刻意的：**同一個 `shard_id` ＝ 同一個 partition**，
所以「更新 Mutable State + 插入多筆 task + 驗證 range_id」可以塞進**一個 LOGGED BATCH 加上 LWT**
（`common/persistence/cassandra/mutable_state_store.go:387`，語句帶 `IF range_id = ?`），
取得等同交易的原子性。SQL 那邊用真交易，自然就能拆成多張正規化的表。

---

## 5. Matching 的資料

Matching 服務有自己獨立的表（`schema/mysql/v8/temporal/schema.sql:95` 起）：

| 表 | 內容 |
| --- | --- |
| `tasks` | 實際排隊中的任務，主鍵 `(range_hash, task_queue_id, task_id)` |
| `task_queues` | 每個 task queue partition 的 metadata 與 `range_id`（**同樣的租約 fencing 手法**，見 `common/persistence/cassandra/matching_task_store_queue.go:77`） |
| `tasks_v2` / `task_queues_v2` | fairness 版本，主鍵多了 `pass` 欄位（stride scheduling），見 `service/matching/fairness.md` |
| `task_queue_user_data` | 使用者資料（Worker Versioning 的 build id 規則等），有 `version` 欄位做樂觀鎖 |
| `build_id_to_task_queue` | build id → task queue 的反查索引 |

注意 partition key 是 `range_hash`（task queue 名稱的 hash）而不是 `shard_id`——
**matching 的切分方式和 history 完全獨立**。

同時也要記得：DB 裡的 `tasks` 只是 backlog。任務優先走記憶體直接配對給正在 long-poll 的 worker，
只有沒人領時才落地（`docs/architecture/matching-service.md`）。

---

## 6. 其餘的表

| 表 | 用途 |
| --- | --- |
| `namespaces` / `namespace_metadata`（`schema.sql:1`、`:13`） | Namespace 設定。`namespace_metadata` 是**單列**的全域 `notification_version` 計數器，每次改 namespace 就 +1，用來給變更排序（failover 判序也靠它）。各服務的 namespace cache 由 `common/namespace/nsregistry/registry.go` 維護：DB 支援 watch 就掛 `WatchNamespaces`（`:506`），不支援就退回定時全量 `ListNamespaces` 輪詢（`runPollingLoop`，`:533`） |
| `cluster_metadata_info`（`:352`） | 各叢集的 metadata（XDC 用），帶 `version` 樂觀鎖 |
| `cluster_membership`（`:361`） | 服務節點心跳表，membership ring 的底層（有 `record_expiry` 做自動過期） |
| `queues` / `queue_messages`（`:378`、`:386`） | **Queue V2**：通用的持久化佇列，目前主要用途是 History Task DLQ（`common/persistence/queue_v2.go:9` 定義 `QueueTypeHistoryNormal = 1`、`QueueTypeHistoryDLQ = 2`），用 `tdbg dlq` 檢視與重放 |
| `nexus_endpoints` / `nexus_endpoints_partition_status`（`:402`、`:411`） | Nexus endpoint 註冊表；後者是單列的全表版本號，供 long-poll 增量同步 |
| `history_node` 的鄰居 `queue` / `queue_metadata`（`:336`、`:344`） | 舊版 namespace replication queue |

### Visibility 是獨立的一套

Visibility 不走上面的 store，有自己的 `common/persistence/visibility/`：

- `factory.go:33` `NewManager` 依設定挑 store；有次要 store 設定時回傳
  `NewVisibilityManagerDual`（`factory.go:118`）做雙寫——這是從 SQL visibility 遷移到 Elasticsearch 的機制。
- 兩種實作：`store/sql/`（標準 visibility）與 `store/elasticsearch/`（進階 visibility，支援 Search Attributes 與複雜查詢）。
- 資料從哪來？來自 §3.4 的 **visibility task**：Mutable State 變更 → 產生 visibility task → 佇列處理器寫入 visibility store。
  所以 **visibility 永遠是最終一致的**，`DescribeWorkflowExecution`（讀 Mutable State）和 `ListWorkflowExecutions`（讀 visibility）
  短時間內可能不一致，這是設計而非 bug。

---

## 7. 快取：為什麼不是每次都讀 DB

History 服務在記憶體裡快取 Mutable State（`service/history/workflow/cache/`）：

- 預設上限 128,000 筆（`common/dynamicconfig/constants.go:1888` `history.hostLevelCacheMaxSize`），
  或改用 byte 為單位（`history.hostLevelCacheMaxSizeBytes`，預設約 1GB），TTL 1 小時（`history.cacheTTL`）。
- 快取 entry **同時是鎖**：取得 workflow context 就是取得該 workflow 的互斥鎖。
  這也解釋了 [請求流程導覽](./request-flow.zh-TW.md) 記錄的那個坑——呼叫 matching 前必須先 `release(nil)`，
  否則 matching 回呼 history 時會撞到自己持有的鎖而死鎖。
- 因為 shard 有唯一 owner，這份快取**不需要跨 host 失效機制**：別人根本不會寫這個 shard。

---

## 8. 自己動手驗證

```bash
# 看某個 DB 的完整 schema（MySQL 為例）
sed -n '1,60p' schema/mysql/v8/temporal/schema.sql

# 目前 schema 版本與 migration 歷史
ls schema/mysql/v8/temporal/versioned | sort -V | tail -5

# Mutable State 的完整欄位定義
grep -n "^  " proto/internal/temporal/server/api/persistence/v1/executions.proto | head -80

# 所有 task 型別（對應 §3.4 的七個 category）
ls service/history/tasks/*.go | grep -v test

# 實際跑起來看資料（需要本機 DB）
make start-dependencies         # 起 cassandra/mysql/es 等相依服務（Makefile:715）
make install-schema-mysql8      # 安裝 schema（另有 -cass-es / -postgresql12 / -xdc 等變體，Makefile:639 起）
```

工具面：`tools/tdbg`（`make bins` 會建出）可以直接讀 shard 資訊、Mutable State、歷史事件與 DLQ，
是理解這些資料結構最直接的方式。

---

## 接下來讀什麼

1. `docs/architecture/history-service.md` — 佇列處理器（queue processor）的框架與 ack level 機制。
2. `docs/architecture/in-memory-queue.md` — 為什麼要有不落地的 memory-timer category。
3. `docs/architecture/chasm.md` — `chasm_nodes` / `chasm_node_maps` 欄位背後的新架構。
4. `common/persistence/data_interfaces.go` — 所有持久層請求/回應結構的權威定義，值得從頭翻一次。
5. [`docs/runtime-topology.zh-TW.md`](./runtime-topology.zh-TW.md) — shard 到底是誰認領的、`range_id` 租約在整個擁有權機制裡的位置。
6. [`docs/feature-map.zh-TW.md`](./feature-map.zh-TW.md) — 這些內部結構被 `AdminService` 開成哪些運維 API（`GetShard`、`ListHistoryTasks`、DLQ 系列）。
7. [`docs/reliability.zh-TW.md`](./reliability.zh-TW.md) — 內部佇列任務被讀出來之後的完整生命週期：誰在什麼時候才真的把那些列刪掉。
8. [`docs/dev-workflow.zh-TW.md`](./dev-workflow.zh-TW.md) — 為什麼一動到 `common/persistence/` 或 `schema/`，CI 就會對八種資料庫跑全量測試。
