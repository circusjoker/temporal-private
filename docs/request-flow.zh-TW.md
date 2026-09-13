# 請求流程導覽：一個 Workflow 在程式碼裡走過哪些地方（繁體中文）

> 這是 [專案總覽](./project-overview.zh-TW.md) 的續篇。總覽回答「這專案在幹嘛」，
> 本文回答「**它實際上怎麼跑**」—— 用可點擊的 `檔案:行號` 把一個 Workflow 從 `StartWorkflowExecution`
> 到第一個 Activity 完成的完整路徑串起來。
>
> 所有行號皆對照 commit `3d5972f4b` 時的程式碼實際確認過；重構後行號會漂移，但函式名稱可用來重新定位。

## 先記住三件事

1. **Server 不執行使用者的程式碼。** 它只做三件事：把事件寫進 History、把任務丟給 Matching、在時間到時觸發 Timer。
   使用者的 Workflow / Activity 邏輯跑在使用者自己的 Worker 行程裡。
2. **所有寫入都是「改 Mutable State + append History Events + 產生內部任務」的一次原子提交。**
   內部任務（transfer / timer / visibility…）和 Mutable State 寫在同一次 DB 交易裡，所以不會有「狀態變了但任務漏了」的情況。
   （細節：SQL 後端是先 append history node、再開交易寫 Mutable State 與 tasks，孤兒 node 事後 trim；
   Cassandra 則靠 shard 同 partition 的 LOGGED BATCH + LWT 一次做完。見 [資料模型與持久層導覽](./data-model.zh-TW.md)。）
3. **Frontend 幾乎不做決策。** 它驗證、授權、然後把請求轉給 History（依 workflow ID 分片）或 Matching（依 task queue 分片）。

## 全景圖

```
SDK Client ──1──> Frontend ──2──> History(shard N)          [寫 WorkflowExecutionStarted + 產生 transfer task]
                                     │
                                     3 (背景佇列處理)
                                     v
                                  Matching ──4──> (task queue 裡等 Worker 來領)
                                     ^
User Worker ──5 long poll──> Frontend ──> Matching ──6 RecordWorkflowTaskStarted──> History
User Worker ──7 RespondWorkflowTaskCompleted──> Frontend ──> History  [寫 commands 產生的事件 + 新 transfer task]
                                                               │
                                                               8 (同上循環，這次是 Activity Task)
```

## 第一段：Start Workflow（步驟 1–2）

| # | 位置 | 做什麼 |
| --- | --- | --- |
| 1 | `service/frontend/workflow_handler.go:550` `StartWorkflowExecution` | 對外 gRPC 入口 |
| 1a | `service/frontend/workflow_handler.go:615` `prepareStartWorkflowRequest` | 補預設值 → 驗證 workflow ID / type / task queue / timeout / retry policy / cron |
| 1b | `service/frontend/workflow_handler.go:566` | 用 `namespaceRegistry` 把 namespace 名稱換成 namespace ID |
| 1c | `client/history/client.go:298` `shardIDFromWorkflowID` → `common/util.go:418` `WorkflowIDToHistoryShard` | **關鍵**：`hash(namespaceID + workflowID) % numShards` 決定由哪個 history shard 負責。同一個 workflow ID 永遠落在同一個 shard，這就是 Temporal 序列化並行操作的方式 |
| 2 | `service/history/handler.go:630` `Handler.StartWorkflowExecution` | History 服務端入口 |
| 2a | `service/history/api/startworkflow/api.go:186` `Starter.Invoke` | 主流程 |
| 2b | 同檔 `:258` `prepareNewWorkflow` | 建立 Mutable State，寫入 `WorkflowExecutionStarted` 事件與第一個 Workflow Task |
| 2c | `service/history/workflow/mutable_state_impl.go:3007` `AddWorkflowExecutionStartedEvent` | 真正 append 事件 |
| 2d | `service/history/workflow/task_generator.go:129` `GenerateWorkflowStartTasks` / `:424` `GenerateScheduleWorkflowTaskTasks` | 產生內部任務（execution timeout timer、transfer task…） |
| 2e | 同檔 `:239` `lockCurrentWorkflowExecution` → `:311` `createBrandNew` | 鎖住「current run」再寫入；失敗時 `:328` `handleConflict` 依 `WorkflowIdReusePolicy` / `WorkflowIdConflictPolicy` 決定 dedup、拒絕還是接上既有 run |
| 2f | `common/persistence/execution_manager.go:105` `CreateWorkflowExecution` | 一次交易寫進 DB |

**這裡的巧妙之處**：`Starter.Invoke` 先樂觀地建立 workflow，撞到 `CurrentWorkflowConditionFailedError`
才走 `handleConflict`；先寫的那份狀態由背景程序清掉（見 `:311` 上方註解）。這讓最常見的「全新 workflow」路徑只需一次寫入。

## 第二段：Transfer Task → Matching（步驟 3–4）

Start 的 DB 交易只是「把待辦事項記下來」。實際推給 Matching 是**非同步**的：

| # | 位置 | 做什麼 |
| --- | --- | --- |
| 3 | `service/history/queues/queue_immediate.go:132` `processEventLoop` → `service/history/queues/queue_base.go:262` `processNewRange` | 每個 shard 跑佇列處理迴圈，掃出新任務 |
| 3a | `service/history/transfer_queue_active_task_executor.go:289` `processWorkflowTask` | 載入 Mutable State、驗證 task 沒過期（`Stamp` 比對、`CheckTaskVersion`） |
| 4 | `service/history/transfer_queue_task_executor_base.go:147` `pushWorkflowTask` | 呼叫 `matchingRawClient.AddWorkflowTask` |
| 4a | `service/matching/matching_engine.go:586` `AddWorkflowTask` | Matching 收下，放進 task queue partition |

`processWorkflowTask` 裡有一段值得注意的註解（`transfer_queue_active_task_executor.go:334-336`）：
**呼叫 matching 前必須先 `release(nil)` 放掉 workflow 鎖**，因為 matching 可能立刻回頭呼叫 history 的
`RecordWorkflowTaskStarted`，不放鎖就會自我死鎖。

## 第三段：Worker 領任務（步驟 5–6）

| # | 位置 | 做什麼 |
| --- | --- | --- |
| 5 | `service/frontend/workflow_handler.go:1073` `PollWorkflowTaskQueue` | Worker 的 long poll 進來（有 `ValidateLongPollContextTimeout` 檢查） |
| 5a | 同檔 `:1129` | 轉給 `matchingClient.PollWorkflowTaskQueue` |
| 5b | `service/matching/matching_engine.go:697` `PollWorkflowTaskQueue` | 把 poller 和 task 配對（`matcher.go` / `pri_matcher.go`、`task_queue_partition_manager.go`、`physical_task_queue_manager.go`） |
| 6 | `service/matching/matching_engine.go:3482` `recordWorkflowTaskStarted` → `:3527` `historyClient.RecordWorkflowTaskStarted` | **配對成功後才回頭找 history**，寫入 `WorkflowTaskStarted` 事件並取回要回放給 Worker 的歷史 |

為什麼是 Matching 回呼 History 而不是 History 先準備好內容？因為 task 可能在佇列裡待很久、
workflow 可能已經被 terminate。等真的有 Worker 要領時才去 history 拿最新狀態，才不會派送過期資料
（所以 `matching_engine.go:809-816` 有一整段在處理 `NotFound` / `DataLoss` 時直接 drop task）。

## 第四段：Worker 回報，循環繼續（步驟 7–8）

| # | 位置 | 做什麼 |
| --- | --- | --- |
| 7 | `service/frontend/workflow_handler.go:1217` `RespondWorkflowTaskCompleted` → `:1248` `historyClient.RespondWorkflowTaskCompleted` | Worker 回報「這輪 workflow code 跑完了，產出這些 commands」 |
| 7a | `service/history/api/respondworkflowtaskcompleted/workflow_task_completed_handler.go:279` `handleCommand` | **整個系統的核心 switch**：把 SDK 的 command 翻譯成 history event |
| 8 | `service/history/workflow/mutable_state_impl.go:4306` `AddActivityTaskScheduledEvent` | 例如 `SCHEDULE_ACTIVITY_TASK` → 寫 `ActivityTaskScheduled` 事件 |
| 8a | `service/history/workflow/task_generator.go:552` `GenerateActivityTasks` | 產生對應的 transfer task |
| 8b | `service/history/workflow/mutable_state_impl.go:7607` `CloseTransactionAsMutation` → `common/persistence/execution_manager.go:168` `UpdateWorkflowExecution` | 事件 + 任務 + 狀態一次交易寫入 |
| 8c | `service/history/transfer_queue_active_task_executor.go:234` `processActivityTask` | 同第二段，只是推的是 Activity Task |
| 8d | `service/frontend/workflow_handler.go:1340` `PollActivityTaskQueue` / `:1640` `RespondActivityTaskCompleted` | Worker 領 Activity、做完回報，再次觸發新的 Workflow Task |

`handleCommand` 的 switch 就是「SDK 能做什麼」的權威清單：schedule activity、start/cancel timer、
complete/fail/cancel workflow、start child workflow、continue-as-new、signal/cancel external workflow、
record marker、upsert search attributes、protocol message（Update 用）。

## 時間怎麼來的：Timer Queue

Workflow 裡的 `sleep`、各種 timeout 都不是靠輪詢，而是寫進 timer queue：

- `service/history/timer_queue_active_task_executor.go:144` `executeUserTimerTimeoutTask` — 使用者的 `StartTimer`
- 同檔 `:204` `executeActivityTimeoutTask` — activity 的 start-to-close / heartbeat 等逾時
- 同檔 `:382` `executeWorkflowTaskTimeoutTask` — workflow task 逾時後重派

## 怎麼自己驗證這條路徑

```bash
# 1. 看一個 command 會產生哪些事件（handleCommand 的所有分支）
grep -n "case enumspb.COMMAND_TYPE" service/history/api/respondworkflowtaskcompleted/workflow_task_completed_handler.go

# 2. 看 history 服務對每個 RPC 的實作（一個 RPC 一個 package）
ls service/history/api/

# 3. 跑起 functional test 看真實流程
make functional-test
```

## 延伸閱讀

- `docs/architecture/workflow-lifecycle.md` — 官方版序列圖，可與本文對照
- `docs/architecture/history-service.md`、`docs/architecture/matching-service.md` — 兩個服務的內部設計
- `docs/architecture/speculative-workflow-task.md` — 為了 Query / Update 而生的「不落地的 workflow task」
- `docs/architecture/message-protocol.md` — `COMMAND_TYPE_PROTOCOL_MESSAGE` 背後的機制
- [`docs/runtime-topology.zh-TW.md`](./runtime-topology.zh-TW.md) — 本文裡的跨服務呼叫實際上怎麼找到對方的主機
- [`docs/feature-map.zh-TW.md`](./feature-map.zh-TW.md) — 本文追的是 Start 這一條路徑，那裡列出另外一百多個 RPC 各自通往哪裡
- [`docs/reliability.zh-TW.md`](./reliability.zh-TW.md) — 本文追的是順利的路徑；那裡說明同樣這些 transfer / timer 任務失敗時會怎麼被分類、退避、去重與丟進 DLQ
- [`docs/dev-workflow.zh-TW.md`](./dev-workflow.zh-TW.md) — 想在本文這條路徑上動手改，先看這裡的測試與 lint 流程
