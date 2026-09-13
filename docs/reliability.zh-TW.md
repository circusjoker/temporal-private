# 可靠性與失敗處理導覽（繁體中文）

> 前四份文件講的是「順利的時候怎麼跑」：
> [請求流程](./request-flow.zh-TW.md) 講程式碼路徑、[資料模型](./data-model.zh-TW.md) 講狀態存在哪、
> [執行期拓樸](./runtime-topology.zh-TW.md) 講誰在跑、[能力地圖](./feature-map.zh-TW.md) 講提供什麼。
> **本文講「出事的時候怎麼辦」** —— Temporal 賣點是 durable execution，而 durable 不是一句口號，
> 它是由一整套錯誤分類、退避重試、去重防線、過載保護與毒丸隔離機制堆出來的。
> 想知道怎麼在本機重現這些行為請看 [開發流程導覽](./dev-workflow.zh-TW.md)。

本文所有敘述皆對照程式碼逐行驗證，行號對應 commit `92cc45b4b` 當下的檔案。

---

## 0. 一句話總結：三層可靠性

Temporal 的持久性不是靠單一機制，而是三層疊加，**每一層都假設上一層會失敗**：

| 層 | 保證 | 失敗時發生什麼 | 主要程式碼 |
| --- | --- | --- | --- |
| 1. 內部任務（transfer / timer / …） | **至少一次（at-least-once）** | 任務重跑；靠第 2 層去重 | `service/history/queues/` |
| 2. Mutable State 條件式寫入 | **同一 shard 內序列化、以 `range_id` 圍籬（fencing）** | 條件失敗 → 整個操作重來 | `service/history/shard/context_impl.go` |
| 3. History Events（append-only） | **可重播還原任何時刻的狀態** | Worker 掛掉 → 換一台重播歷史繼續 | `service/history/workflow/` |

換句話說：**任務可以重複派送，但狀態只會前進一次**。第 3 節說明第 2 層怎麼把第 1 層的重複吃掉。

---

## 1. 一個內部任務的完整生命週期

History service 的每個 shard、每個 task category 各有一條處理管線。以下是一個任務從被讀出來到被刪除的全程：

```
 persistence (history_immediate_tasks / history_scheduled_tasks)
        │
        │ ① Reader 依 slice 分批讀（有 rate limiter + pending 上限）
        ▼
    Executable  ──② scheduler.Submit──▶  worker goroutine ──③ Execute()
        │                                                      │
        │                                     ┌────────────────┴────────────────┐
        │                                     ▼                                 ▼
        │                                  成功 → Ack()                   失敗 → HandleErr()
        │                                     │                                 │
        │                                     │              ┌──────────────────┼──────────────────┐
        │                                     │              ▼                  ▼                  ▼
        │                                     │        「可丟棄」return nil  「可重試」Nack()   「毒丸」→ DLQ
        │                                     │                                 │
        │                                     │              ④ TrySubmit（快路徑）或 rescheduler.Add（退避）
        │                                     ▼
        │                              ⑤ checkpoint()：slice 收縮 → 算出新的刪除水位
        ▼
  RangeCompleteHistoryTasks 真正把列從 DB 刪掉
```

| 步驟 | 位置 |
| --- | --- |
| ① 讀取批次、限流、暫停 | `service/history/queues/reader.go:427`（`loadAndSubmitTasks`，開頭就 `ratelimiter.Wait`） |
| ① 待處理任務過多時暫停 | `service/history/queues/reader.go:532`（`verifyPendingTaskSize`）→ `reader.go:384`（`Pause`） |
| ③ 執行本體（含 panic 攔截、OTEL span） | `service/history/queues/executable.go:273`（`Execute`） |
| ③ 單次執行的硬逾時 3 秒 | `service/history/transfer_queue_task_executor_base.go:36`（`taskTimeout`） |
| 錯誤分類與決策 | `service/history/queues/executable.go:584`（`HandleErr`） |
| ④ 重新排程 | `service/history/queues/executable.go:771`（`Nack`）、`rescheduler.go:120`（`Add`） |
| ⑤ checkpoint 與刪除 | `service/history/queues/queue_base.go:295`（`checkpoint`）、`:373`（`rangeCompleteTasks`） |

### 兩個容易誤解的點

**(a) 任務不是「執行完就刪」，是「整段區間一起刪」。**
`checkpoint()` 先讓每個 reader 收縮自己的 slice，取所有 reader 最小的 `InclusiveMin` 當新的刪除水位，
再呼叫 `RangeCompleteHistoryTasks` 刪掉整個區間（`queue_base.go:317-352`）。
所以**一個卡住的任務會擋住它後面所有已完成任務的刪除**，這就是 backlog 指標會爆的原因。
程式碼裡還有一行順序上的關鍵註解（`queue_base.go:340-342` 的 NOTE）：必須**先刪 DB 再更新 queue state**，
反過來的話一旦刪除失敗、shard 重載，那些任務就永遠不會被刪掉。

**(b) 執行緒永遠不會被重試佔住。**
`IsRetryableError` 固定回 `false`、`RetryPolicy` 固定回 `DisabledRetryPolicy`
（`executable.go:712-726`，註解寫得很白：*never retry task while holding the goroutine*）。
所有重試一律走「放掉 goroutine → 進 rescheduler → 之後再排程」，避免一個壞任務吃掉整個 worker pool。

---

## 2. 錯誤分類：`HandleErr` 的五條岔路

`executable.go:584` 的 `HandleErr` 是整個 history 服務最重要的決策點。它把錯誤分成五類：

| 類別 | 判斷函式 | 行為 | 典型錯誤 |
| --- | --- | --- | --- |
| **無效任務** | `isInvalidTaskError`（`:451`） | **直接丟棄**（`return nil`），任務算完成 | `ErrStaleReference`、`NotFound`、`NamespaceNotFound`、`ErrTaskVersionMismatch` |
| **可安全丟棄** | `isSafeToDropError`（`:478`） | 丟棄 | `ErrTaskDiscarded`（standby 任務等太久） |
| **預期內可重試** | `isExpectedRetryableError`（`:511`） | 退避重試，**不計入失敗指標** | `ResourceExhausted`、`NamespaceNotActive`、`ErrTaskRetry`、`ErrDependencyTaskNotCompleted`、`ErrNamespaceHandover` |
| **非預期不可重試** | `isUnexpectedNonRetryableError`（`:561`） | 進 DLQ；DLQ 關閉則丟棄 | `DataLoss`、terminal task error、（可選）`Internal` |
| **非預期但可重試** | 以上皆非 | 重試；累積到上限後進 DLQ | 其餘一切 |

三個值得記住的細節：

1. **`invalidTask` 只在第一次嘗試時才算數**（`:601-604` 的註解）：
   第二次之後拿到 `NotFound` 有可能是「前一次其實寫成功了」，不能當成無效任務來統計。
2. **`ErrStaleReference` 雖然本質是 `NotFound`，卻被特別挑出來**（`:452-460`）單獨記 `TaskSkipped` 指標 ——
   因為它是「任務參照的東西已經被新版本取代」的正常現象，不是錯誤。
3. **錯誤是否值得告警，和是否重試是兩個獨立判斷**：`classifyAlertableError`（`:489`）
   把 namespace 級的 `ResourceExhausted`（＝使用者自己超量）、failover、handover 都排除在告警之外。
   超過 30 次嘗試（`taskCriticalLogMetricAttempts`，`:119`）才升級成 `Critical error processing task`。

### 四套退避策略

不同錯誤用不同的退避曲線（`executable.go:887` 的 `backoffDuration` 挑選，常數在 `common/util.go:69-86`）：

| 策略 | 初始 / 係數 / 上限 | 用於 |
| --- | --- | --- |
| `CreateTaskReschedulePolicy` | 1s / 1.1 / 3min | 一般錯誤（`common/util.go:251`） |
| `CreateTaskNotReadyReschedulePolicy` | 3s / 1.5 / 3min | `ErrTaskRetry`、handover、`Internal`（`:268`） |
| `CreateDependencyTaskNotCompletedReschedulePolicy` | 3s / 1.5 / 3min | 依賴任務未完成（`:260`） |
| `CreateTaskResourceExhaustedReschedulePolicy` | 3s / 1.5 / **5min** | 系統級資源耗盡，取兩者較大值（`:276`） |

另有一條**跳過退避的快路徑**：`shouldResubmitOnNack`（`:860`）在前 10 次嘗試內、
且錯誤不是 shard 失主／internal／資源耗盡時，直接 `scheduler.TrySubmit` 重投。
這條路徑是為了 workflow busy（同一個 workflow 被鎖住）這種「馬上重試就會好」的情況，
不然每次都要等 1 秒以上，延遲會非常難看。更進一步，busy workflow 還可以被路由到序列化排程器
（`Nack` 開頭的 `BusyWorkflowHandler`，`:779-787`）。

---

## 3. 至少一次為什麼不會造成重複副作用

任務會重跑（shard 搬家、checkpoint 前當機、退避重試），但業務語意必須只發生一次。
防線全部在 **executor 執行前的驗證**，以 `processActivityTask` 為例
（`service/history/transfer_queue_active_task_executor.go:234`）：

| 檢查 | 行號 | 擋掉什麼 |
| --- | --- | --- |
| 載入 Mutable State，取不到就丟棄 | `:247-254` | workflow 已被刪除 |
| `GetActivityInfo(task.ScheduledEventID)` 找不到 | `:256-260` | Activity 已完成，`ActivityInfo` 已移除 |
| `ai.Stamp != task.Stamp \|\| ai.Paused` | `:262-265` | **上一輪重試留下的過期任務**（每次重試 `Stamp` 遞增） |
| `CheckTaskVersion(..., ai.Version, task.Version, ...)` | `:267-270` | 跨叢集 failover 後版本不符的任務 |
| `!mutableState.IsWorkflowExecutionRunning()` | `:272-275` | workflow 已結束 |

換句話說：**任務本身只是一個「去看一下 Mutable State」的提示**，真正的判斷永遠來自 Mutable State。
任務裡帶的 `Version` / `Stamp` / `EventID` 都只是用來確認「我看到的那個狀態還在不在」。
這也是為什麼第 2 節裡 `ErrStaleReference` 被歸為正常現象 —— 它正是這些檢查被觸發的結果。

---

## 4. 過載保護：五道閥門

Temporal 對「系統快撐不住」的處理不是單點限流，而是分散在五個層次：

1. **入口限流（frontend）**：
   - 主機層 `RateLimitInterceptor`（`service/frontend/fx.go:496`）——
     注意它用的是 `ClusterAwareQuotaCalculator`（`:502-508`）：設定 `GlobalRPS` 後，
     **實際每台的配額是全域值除以 membership 看到的 frontend 台數**，所以擴縮容會自動改變單機限額。
   - Namespace 層 `NamespaceRateLimitInterceptor`（`:635`），且依 API 類別路由到三組獨立的限流器
     （一般執行 / visibility / 會引發 namespace 複寫的 API）。
   - 長輪詢併發數另有 `ConcurrentRequestLimitInterceptor`（`:661`）。
2. **任務優先權**：`priority_assigner.go:32` 把 standby（非 active cluster）任務、刪除／封存類任務
   一律降為 `PriorityPreemptable`；逾時類任務為 `PriorityLow`；其餘為 `PriorityHigh`。
   優先權還會在 failover 後重算（`executable.go:405-413`）。
3. **讀取端自我節流**：reader 讀取前先過 rate limiter，pending 任務超標就 `Pause`（第 1 節）。
4. **佇列告警 → 自動緩解**：`monitor` 會產生三種告警
   （`alerts.go:35-38`：`QueuePendingTaskCount` / `ReaderStuck` / `SliceCount`），
   `mitigator.go:58` 收到後各自套用對應 action：把大戶 namespace 的 slice 搬到獨立 reader、
   強制推進卡住的 reader、合併過多 slice。**這是一套執行期自我調整的機制，不需要人介入。**
5. **熔斷器（僅 outbound queue）**：`NewCircuitBreakerExecutable`（`executable.go:1000`）
   只被 `service/history/outbound_queue_factory.go:143` 使用，每個「目的地」（如 Nexus callback 的對端）
   一個熔斷器。熔斷打開時回傳的是 `ResourceExhausted`（`:1020-1028`），
   用意寫在註解裡：**讓任務走比較慢的退避曲線，而且不會被送進 DLQ**。

---

## 5. 毒丸任務：DLQ

當一個任務怎麼重試都失敗（通常是資料損毀或程式 bug），它必須被移出主佇列，否則會永遠擋住刪除水位。

**進 DLQ 的四個條件**（皆在 `executable.go`）：

| 條件 | 行號 | 預設值 |
| --- | --- | --- |
| 錯誤符合設定的正規表示式 | `:687`（`matchDLQErrorPattern`） | `history.TaskDLQErrorPattern` 預設空字串（停用） |
| terminal error（`DataLoss`、terminal task error） | `:561` + `:649-663` | 一律 |
| `Internal` 錯誤 | `:570-575` | `history.TaskDLQInternalErrors` 預設 **false** |
| 非預期錯誤累積次數達上限 | `:671-682` | `history.TaskDLQUnexpectedErrorAttempts` 預設 **70**（註解：約一小時） |

實作上有個巧妙的兩段式：`HandleErr` 只是把錯誤記在 `e.terminalFailureCause` 並回傳 `ErrTerminalTaskFailure`，
**真正寫 DLQ 發生在下一次 `Execute()` 的開頭**（`:385-400`）。
這讓「寫入 DLQ」這個動作本身也享有任務框架的重試與限流，而不是在錯誤處理路徑裡直接做 I/O。
若當下 DLQ 被關掉，同一段程式會把 `terminalFailureCause` 清掉並讓任務回到正常重試（`:398-399`）。

**運維面**：寫入時記 `Task enqueued to DLQ` 警告（`queues/dlq_writer.go:134`）與 `dlq_writes` 指標；
處理則透過 AdminService 的五支 RPC ——
`GetDLQTasks` / `PurgeDLQTasks` / `MergeDLQTasks` / `DescribeDLQJob` / `ListQueues`
（`proto/internal/temporal/server/api/adminservice/v1/service.proto:177-202`），
CLI 是 `tdbg dlq`，詳細步驟見 [docs/admin/dlq.md](./admin/dlq.md)。

> ⚠️ **文件與程式碼不一致**：`history.TaskDLQEnabled` 的說明字串
> （`common/dynamicconfig/constants.go:2950-2955`）寫著「非 Cassandra 不要開，因為沒實作」，
> 但預設值本身就是 `true`，且 SQL 後端的 `common/persistence/sql/queue_v2.go`（466 行）
> 與 MySQL schema 的 `queue_messages` 表（`schema/mysql/v8/temporal/schema.sql:386`）都存在。
> 這段警語應已過時 —— 要依賴它之前請先實測。

---

## 6. 使用者層級的失敗處理

前五節都是 server 內部的自我保護。使用者看得到的失敗處理是另一套機制：

### 6.1 Activity 重試不寫歷史事件

這是 Temporal 最容易被誤解的設計。`RetryActivity`（`service/history/workflow/mutable_state_impl.go:6880`）
在決定要重試時，**只更新 Mutable State 裡的 `ActivityInfo`（`Attempt++`、`Stamp++`、記下最後一次失敗）
並產生一個 `ActivityRetryTimerTask`**（`service/history/workflow/task_generator.go:572`），
完全不寫 History Event。只有當重試終結（次數用盡／逾時／不可重試錯誤）時，
`RespondActivityTaskFailed` 才會寫下 `ActivityTaskFailed` 事件
（`service/history/api/respondactivitytaskfailed/api.go:112-119`）。

**這代表**：一個重試一萬次的 Activity，歷史裡只會有 `ActivityTaskScheduled` 與最後的 `ActivityTaskFailed`
兩個事件 —— 歷史長度與重試次數無關。想看重試進度只能靠 `DescribeWorkflowExecution` 的 pending activity 資訊。

退避計算在 `service/history/workflow/retry.go:69`（`nextBackoffInterval`），回傳的 `RetryState` 有四種終結原因：
`MAXIMUM_ATTEMPTS_REACHED`、`TIMEOUT`、`NON_RETRYABLE_FAILURE`、`RETRY_POLICY_NOT_SET`。
另外兩個細節：SDK 可以在失敗裡夾帶 `nextRetryDelay` 覆寫本次的最大間隔（`retry.go:55` + `mutable_state_impl.go:6943-6947`）；
而 `ScheduleToStart` / `ScheduleToClose` 逾時會回傳 `RETRY_STATE_TIMEOUT` 而非「不可重試」
（`mutable_state_impl.go:6895-6911` 有長註解說明真正的失敗原因被放在 `failure.Cause`）。

### 6.2 Workflow 重試與 Cron 是「開一個新 run」

Workflow 層級沒有「原地重試」這回事。以 run timeout 為例
（`service/history/timer_queue_active_task_executor.go:658`）：

1. 無論如何先寫 `WorkflowExecutionTimedOut` 事件（`:720-726`）。
2. 判斷是否還能重試 → 是則 `CONTINUE_AS_NEW_INITIATOR_RETRY`；
   否則看有沒有 cron → `CONTINUE_AS_NEW_INITIATOR_CRON_SCHEDULE`（`:702-714`）。
3. 兩者皆無 → 就此結束（`:729-739`）。
4. 有的話**產生新的 run ID、建立一份全新的 Mutable State**
   （`NewMutableStateInChain`，`:747-758`），由 `SetupNewWorkflowForRetryOrCron`（`:763`）承接
   start attributes、links、`LastCompletionResult` 與失敗資訊。

也就是說：**Workflow retry、Cron、Continue-As-New 在 server 端是同一條程式碼路徑**
（`workflow.SetupNewWorkflowForRetryOrCron` 的另外兩個呼叫點在
`service/history/api/respondworkflowtaskcompleted/workflow_task_completed_handler.go:1469` 與 `:1529`）。
這也解釋了為什麼三者的歷史都是「舊 run 關閉 + 新 run 開始」而非單一 run 變長。

四種 workflow 層 timer 任務的執行器都在同一個檔案：
`executeWorkflowTaskTimeoutTask`（`:382`）、`executeWorkflowBackoffTimerTask`（`:480`，三種等待：retry / cron / delay-start，見 `:507-517`）、
`executeWorkflowRunTimeoutTask`（`:658`，單次 run）、`executeWorkflowExecutionTimeoutTask`（`:821`，含所有 retry/cron 的總時限）。

---

## 7. Shard 級故障：`range_id` 是最後的裁判

前面所有機制都建立在「我還是這個 shard 的擁有者」之上。這個前提由
`service/history/shard/context_impl.go` 守護：

- **讀到 `ShardOwnershipLostError`** → 直接把 shard context 轉成停止狀態（`:1475-1489`），
  history engine 關閉，所有進行中的任務被丟棄。新擁有者會從持久化的 queue state 重新讀 —— 這就是「至少一次」的來源。
- **寫入錯誤分三類處理**（`:1501-1549`）：
  - 明確沒寫進去的（各種 `ConditionFailed`、`ResourceExhausted`、`NotFound`…）→ 原樣回傳，呼叫端重試即可。
  - `ShardOwnershipLostError` → 同上，停掉 shard。
  - **其他一律視為「不知道寫進去了沒」** → 觸發 `contextRequestLost{}`，在背景重新取得 shard。
    註解（`:1540-1546`）解釋了為什麼：重新取得會拿到**新的 `range_id`**，
    而 `range_id` 遞增保證了後續讀取要嘛看得到那筆寫入、要嘛確定它失敗了。
    **用「換一個新租約」把不確定性變成確定性 —— 這是整個 shard 設計最漂亮的一招。**

搭配 `queue_base.go:364`（`updateShardRangeID`）：checkpoint 偵測到 `range_id` 變了會**重跑一次區間刪除**，
因為某些 persistence 實作是以持久化的 shardInfo 為準來服務請求的。

shard 擁有權的三層檢查（hash ring → ownership 元件 → `range_id` 條件式更新）
在 [執行期拓樸導覽](./runtime-topology.zh-TW.md) 第 5 節有完整說明。

---

## 8. 自己驗證

```bash
cd /Users/johnny/Projects/temporal

# 1) 錯誤分類的五條岔路，一次看完
sed -n '442,600p' service/history/queues/executable.go

# 2) 四套退避策略的常數
sed -n '69,90p;250,282p' common/util.go

# 3) 任務去重的五道檢查（以 Activity transfer task 為例）
sed -n '234,276p' service/history/transfer_queue_active_task_executor.go

# 4) 確認 Activity 重試真的不寫歷史事件（只有終結時才寫）
grep -n "AddActivityTaskFailedEvent" service/history/api/respondactivitytaskfailed/api.go

# 5) 列出所有進 DLQ 的觸發點
grep -n "terminalFailureCause = err" service/history/queues/executable.go

# 6) 佇列告警與對應的緩解動作
sed -n '35,39p' service/history/queues/alerts.go && sed -n '58,92p' service/history/queues/mitigator.go

# 7) 確認「寫入結果不明 → 換 range_id」那段註解
sed -n '1540,1547p' service/history/shard/context_impl.go

# 8) 相關的 DLQ 動態設定與預設值
grep -n "history.TaskDLQ" -A 3 common/dynamicconfig/constants.go
```

---

## 延伸閱讀

- [請求流程導覽](./request-flow.zh-TW.md) —— 本文的任務框架處理的就是該文第 2 段產生的那些 transfer / timer 任務。
- [資料模型與持久層導覽](./data-model.zh-TW.md) —— 第 1 節提到的「刪除水位」對應該文的內部佇列任務表結構。
- [執行期拓樸導覽](./runtime-topology.zh-TW.md) —— 第 7 節的 shard 擁有權在該文第 5 節有三層防線的完整說明。
- [能力地圖](./feature-map.zh-TW.md) —— 第 5 節的 DLQ 管理 RPC 屬於該文的 AdminService 運維 API。
- `docs/architecture/retry.md` —— `common/backoff` 套件與 gRPC 層重試（本文聚焦的是任務層）。
- `docs/architecture/circuit-breaker.md` —— outbound queue 熔斷器的完整設計。
- `docs/admin/dlq.md` —— DLQ 的實際操作步驟（`tdbg dlq`）。
