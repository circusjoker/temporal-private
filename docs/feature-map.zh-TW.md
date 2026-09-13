# Temporal Server 能力地圖（繁體中文導覽）

> 前四份導覽都是「往內看」——[總覽](./project-overview.zh-TW.md)談定位、[請求流程](./request-flow.zh-TW.md)談程式碼路徑、[資料模型](./data-model.zh-TW.md)談怎麼存、[執行期拓樸](./runtime-topology.zh-TW.md)談怎麼部署。
> 這一份反過來「往外看」：**這台 server 到底對外提供哪些能力？每個能力由誰實作？**
> 全文 file:line 皆以本 repo 當前 commit 逐一比對過，文末附自驗指令。

---

## 1. 對外的五個介面

Frontend 在同一個 gRPC server 上註冊三個 service（`service/frontend/service.go:516-518`）：

| 介面 | 註冊處 | 使用者 | 方法數 |
|---|---|---|---|
| `WorkflowService` | `service.go:516` → `WorkflowHandler` | SDK / 應用程式 | 103 |
| `OperatorService` | `service.go:518` → `OperatorHandlerImpl` | 叢集管理者 | 12 |
| `AdminService` | `service.go:517` → `AdminHandler` | 維運 / `tctl admin` | 46 |

（方法數＝handler 上的匯出方法扣掉 `Start`/`Stop`/`GetConfig`；文末自驗指令可重算。）

另外還有兩個 HTTP 面，跑在同一個行程但不是 gRPC：

- **REST 轉譯層**：`service/frontend/http_api_server.go:148` 用 grpc-gateway 把 `temporal.api.workflowservice.v1.WorkflowService` 整個服務直接掛成 HTTP/JSON，`:157` 建出 `runtime.ServeMux`。不是手寫的 REST API，而是由 proto 反射生成，所以 WorkflowService 加一個 RPC，HTTP 面自動就有。
- **Nexus 入口**：`service/frontend/nexus_operation_http_handler.go:115` 的 `RegisterRoutes` 掛上兩條路由——依 namespace+task queue 派送（`:123`）與依 endpoint 派送（`:128`）。這是給**其他 Temporal 叢集或外部系統**呼叫的跨服務入口，不是給 SDK 用的。

`AdminService` 值得特別提醒：它和 `WorkflowService` 開在**同一個 port、同一個 gRPC server** 上，沒有獨立的 listener。要限制誰能呼叫 admin API，只能靠 authorizer，不能靠網路分段。

---

## 2. 一個請求進來會經過什麼

所有 gRPC 請求先穿過一條固定順序的 unary interceptor 管線（`service/frontend/fx.go:290-319` 的字面陣列 21 層，再加 `:329` 附加的 retry 共 22 層），由外而內大致是：

```
錯誤遮罩 → 錯誤轉譯 → routing key 擷取 → namespace 存在性驗證 → namespace log
  → metrics context → 授權(authInterceptor) → namespace handover → 跨叢集轉導(redirection)
  → telemetry → 健康檢查 → namespace 狀態驗證 → 併發數限制 → namespace 速率限制
  → 全域速率限制 → SDK 版本紀錄 → caller info → 慢請求記錄 → CHASM visibility
  → context metadata → [自訂 interceptor] → [故障注入] → retry(最內層)
```

程式碼裡的註解點出三個順序上的硬性約束，改動時不能亂調：

- 遮罩錯誤細節的 interceptor 必須在**最外層**，否則內層產生的錯誤格式化後才被攔到（`fx.go:292` 註解）。
- `businessIDInterceptor` 必須在任何碰 namespace 的 interceptor **之前**（`fx.go:297` 註解）。
- handover 必須在 redirection **之前**，因為交接完成後請求要轉去正確叢集（`fx.go:303` 註解）；telemetry 則必須在 redirection **之後**，才不會把 metrics 記到錯的叢集（`fx.go:307`）。
- retry 必須在**最內層**（`fx.go:328-329` 註解）。

換句話說：這條管線就是 Temporal 的多租戶邊界（namespace 驗證、限流、授權）與多叢集邊界（handover、redirection）的實際位置，不在各 RPC 的實作裡。

---

## 3. 能力分類：使用者能做什麼

以下把 `WorkflowService` 的 RPC 依功能族分組，行號指向 `service/frontend/workflow_handler.go`。

### 3.1 Namespace 管理（租戶）

`RegisterNamespace`(`:464`)、`DescribeNamespace`(`:484`)、`ListNamespaces`(`:499`)、`UpdateNamespace`(`:514`)、`DeprecateNamespace`(`:532`)。
注意**刪除** namespace 不在這裡，而在 `OperatorService.DeleteNamespace`（`service/frontend/operator_handler.go:550`）——因為刪除是長時間的非同步作業，實作是一支 Workflow（見第 4 節）。

### 3.2 啟動與結束一個 Workflow

| 能力 | RPC | 行號 |
|---|---|---|
| 啟動 | `StartWorkflowExecution` | `:550` |
| 送訊號並在不存在時啟動 | `SignalWithStartWorkflowExecution` | `:2364` |
| 一次原子送出多個操作 | `ExecuteMultiOperation` | `:764` |
| 請求取消（可被 workflow 攔截） | `RequestCancelWorkflowExecution` | `:2265` |
| 強制終止（不可攔截） | `TerminateWorkflowExecution` | `:2469` |
| 回捲到歷史中某一點重跑 | `ResetWorkflowExecution` | `:2418` |
| 刪除紀錄 | `DeleteWorkflowExecution` | `:2510` |
| 暫停 / 恢復整支 workflow | `PauseWorkflowExecution` / `UnpauseWorkflowExecution` | `:7776` / `:7803` |

`ExecuteMultiOperation` 是比較新的合成 API（例如「Start + Update」一次送出），實作在 `service/history/api/multioperation/`。

### 3.3 Worker 端：領任務與回報

這組是 SDK 的長連線主迴圈，也是整個系統流量最大的部分：

- 三種 long-poll：`PollWorkflowTaskQueue`(`:1073`)、`PollActivityTaskQueue`(`:1340`)、`PollNexusTaskQueue`(`:6378`)。
- 三組回報：`RespondWorkflowTaskCompleted`(`:1217`)/`Failed`(`:1269`)、`RespondActivityTaskCompleted`(`:1640`)/`Failed`(`:1845`)/`Canceled`(`:2076`)、`RespondNexusTaskCompleted`(`:6482`)/`Failed`(`:6549`)。
- Activity 心跳：`RecordActivityTaskHeartbeat`(`:1446`)。
- 每個 Activity 回報 API 都有一個 `...ById` 變體（`:1519`/`:1714`/`:1940`/`:2147`），讓**不持有 task token** 的第三方也能代為回報結果——這是「Activity 交給外部系統執行、完成後回呼」這類非同步模式的基礎。
- `ResetStickyTaskQueue`(`:3039`)、`ShutdownWorker`(`:3065`)：worker 重啟與優雅下線用。

這段的完整程式碼路徑（frontend → history → matching → worker）已在[請求流程](./request-flow.zh-TW.md)逐行追過，此處不重複。

### 3.4 與執行中的 Workflow 互動

| 互動方式 | 語意 | RPC | 行號 |
|---|---|---|---|
| Signal | 單向、非同步、寫入歷史 | `SignalWorkflowExecution` | `:2298` |
| Query | 唯讀、同步、**不**寫入歷史 | `QueryWorkflow` | `:3290` |
| Update | 雙向、同步、寫入歷史、可被 workflow 驗證後拒絕 | `UpdateWorkflowExecution` | `:5476` |
| Update（取結果） | 非阻塞取得 update 結果 | `PollWorkflowExecutionUpdate` | `:5583` |

Query 的回覆走一條特殊路徑：worker 收到 query task 後用 `RespondQueryTaskCompleted`(`:2972`) 回，這條 RPC 不屬於前述三組任務回報。

### 3.5 從外部操控 Activity（不改 workflow 程式碼）

`UpdateActivityOptions`(`:7345`)、`PauseActivity`(`:7383`)、`UnpauseActivity`(`:7416`)、`ResetActivity`(`:7448`)。
這組讓維運者能在不重新部署 workflow 的前提下，調整正在重試的 Activity 的 retry policy、timeout，或直接暫停它。對應的 history 實作在 `service/history/api/updateactivityoptions/`、`pauseactivity/`、`unpauseactivity/`、`resetactivity/`。

### 3.6 查詢與可觀測

- 歷史：`GetWorkflowExecutionHistory`(`:961`)、`GetWorkflowExecutionHistoryReverse`(`:1028`)。
- 單筆狀態：`DescribeWorkflowExecution`(`:3354`)、`DescribeTaskQueue`(`:3410`)。
- Visibility 搜尋：`ListWorkflowExecutions`(`:2788`)、`CountWorkflowExecutions`(`:2925`)、`ListArchivedWorkflowExecutions`(`:2827`)，以及舊式的 `ListOpen/ListClosed`(`:2572`/`:2673`) 與已淘汰的 `ScanWorkflowExecutions`(`:2905`)。
- 搜尋欄位定義：讀取用 `GetSearchAttributes`(`:2956`)，**新增/刪除**則屬於運維操作，在 `OperatorService.AddSearchAttributes`(`operator_handler.go:142`)/`RemoveSearchAttributes`(`:333`)/`ListSearchAttributes`(`:474`)。
- 叢集資訊：`GetClusterInfo`(`:3510`)、`GetSystemInfo`(`:3533`)——`GetSystemInfo` 是 SDK 啟動時探測 server 支援哪些能力的地方。

Visibility 是最終一致的獨立儲存，細節見[資料模型第 6 節](./data-model.zh-TW.md)。

### 3.7 Schedule（排程）

`CreateSchedule`(`:3866`)、`DescribeSchedule`(`:4474`)、`UpdateSchedule`(`:4698`)、`PatchSchedule`(`:4851`)、`DeleteSchedule`(`:5074`)、`ListSchedules`(`:5168`)、`CountSchedules`(`:5374`)、`ListScheduleMatchingTimes`(`:4980`)。
這是 cron 的取代品：支援日曆式規則、時區、跳過/補跑策略、暫停與手動觸發。實作見第 4、5 節。

### 3.8 Batch 操作（對一批 workflow 做同一件事）

`StartBatchOperation`(`:5832`)、`StopBatchOperation`(`:6137`)、`DescribeBatchOperation`(`:6181`)、`ListBatchOperations`(`:6311`)。
典型用途：對一個 visibility query 命中的數萬筆 workflow 一次送 signal、terminate、reset 或改 options。

### 3.9 Worker 版本管理與部署

這是目前 API 面積最大、也最新的一塊，同時存在兩代設計：

- **舊代（Build ID 相容集合）**：`UpdateWorkerBuildIdCompatibility`(`:5624`)、`GetWorkerBuildIdCompatibility`(`:5666`)、`GetWorkerTaskReachability`(`:5778`)。
- **中代（Versioning Rules）**：`UpdateWorkerVersioningRules`(`:5699`)、`GetWorkerVersioningRules`(`:5739`)。
- **新代（Worker Deployment）**：`CreateWorkerDeployment`(`:4330`)、`DescribeWorkerDeployment`(`:4291`)、`ListWorkerDeployments`(`:4239`)、`SetWorkerDeploymentCurrentVersion`(`:4128`)、`SetWorkerDeploymentRampingVersion`(`:4177`)、`DescribeWorkerDeploymentVersion`(`:4082`)、`DeleteWorkerDeployment`(`:4395`)、`DeleteWorkerDeploymentVersion`(`:4415`)、`UpdateWorkerDeploymentVersionMetadata`(`:4441`)，以及更早被標為過時的 `DescribeDeployment`(`:4051`)、`ListDeployments`(`:4061`) 等。

要解決的問題是同一個：**長時間執行的 workflow 跨越了程式碼版本更新**。新代多了「ramping version」——可以把一部分新啟動的 workflow 導到新版本做灰度。

### 3.10 Worker 機隊觀測與遠端設定

`RecordWorkerHeartbeat`(`:7595`)、`ListWorkers`(`:7620`)、`CountWorkers`(`:7645`)、`DescribeWorker`(`:7749`)、`FetchWorkerConfig`(`:7720`)、`UpdateWorkerConfig`(`:7728`)、`UpdateTaskQueueConfig`(`:7667`)。
方向很明確：讓 server 不只調度任務，還要能**看見並遠端調整** worker 機隊（例如從 server 端改 worker 的併發上限），不必重新部署 worker。

### 3.11 Workflow Rules

`CreateWorkflowRule`(`:7480`)、`DescribeWorkflowRule`(`:7523`)、`DeleteWorkflowRule`(`:7548`)、`ListWorkflowRules`(`:7572`)、`TriggerWorkflowRule`(`:7771`，目前回傳 `Unimplemented`)。
在 namespace 層級定義「符合條件就自動採取動作」的規則（例如某類 Activity 卡住就自動暫停），屬於平台側的自動化治理。

### 3.12 Nexus（跨 namespace / 跨叢集呼叫）

- 端點註冊屬於運維：`OperatorService` 的 `CreateNexusEndpoint`(`operator_handler.go:840`)、`UpdateNexusEndpoint`(`:848`)、`DeleteNexusEndpoint`(`:856`)、`GetNexusEndpoint`(`:864`)、`ListNexusEndpoints`(`:872`)。
- 任務面在 `WorkflowService`：`PollNexusTaskQueue`(`:6378`) 與兩個 Respond。
- 入站則走第 1 節的 HTTP 路由。

Nexus 讓一個 namespace 的 workflow 能呼叫另一個 namespace（甚至另一個叢集）暴露的操作，而不必共用 task queue 或直接互相信任。設計文件見 [`docs/architecture/nexus.md`](./architecture/nexus.md)。

### 3.13 測試支援

`PollWorkflowExecutionTimeSkipping`(`:7837`) 搭配 `proto/internal/temporal/server/api/testservice/` 提供**時間跳躍**：測試時可以讓 server 的時鐘快轉，讓一支 sleep 30 天的 workflow 在毫秒內跑完。

---

## 4. 關鍵設計：系統功能本身就是 Workflow

Temporal 把自己的平台功能拿 Temporal 自己實作（dogfooding）。`service/worker` 角色註冊了兩類元件（介面定義在 `service/worker/common/interface.go:11` 與 `:36`）：

- `WorkerComponent`：跑在系統 namespace `temporal-system`（`common/primitives/namespaces.go:11`）的單一 worker，由 `service/worker/fx.go:119` 收集。
- `PerNSWorkerComponent`：**每個 namespace 各跑一份**的 worker，由 `service/worker/fx.go:218` 以 fx group `perNamespaceWorkerComponent` 收集。

於是這些「功能」其實都是 workflow type：

| 對外能力 | Workflow Type | 定義位置 | 類別 |
|---|---|---|---|
| Schedule | `temporal-sys-scheduler-workflow` | `service/worker/scheduler/fx.go:26` | per-NS |
| Batch 操作 | `temporal-sys-batch-workflow` | `service/worker/batcher/fx.go:24` | per-NS |
| Worker Deployment | `temporal-sys-worker-deployment-workflow` | `service/worker/workerdeployment/util.go:40` | per-NS |
| Worker Deployment Version | `temporal-sys-worker-deployment-version-workflow` | `service/worker/workerdeployment/util.go:39` | per-NS |
| 刪除 namespace | `temporal-sys-delete-namespace-workflow` | `service/worker/deletenamespace/workflow.go:21` | 系統 |
| 刪除 namespace 內的執行 | `temporal-sys-delete-executions-workflow` | `service/worker/deletenamespace/deleteexecutions/workflow.go:16` | 系統 |
| 回收 namespace 資源 | `temporal-sys-reclaim-namespace-resources-workflow` | `service/worker/deletenamespace/reclaimresources/workflow.go:19` | 系統 |
| 新增 search attribute | `temporal-sys-add-search-attributes-workflow` | `service/worker/addsearchattributes/workflow.go:26` | 系統 |
| DLQ 處理 | `temporal-sys-dlq-workflow` | `service/worker/dlq/workflow.go:139` | 系統 |
| Parent close policy 傳播 | `temporal-sys-parent-close-policy-workflow` | `service/worker/parentclosepolicy/workflow.go:31` | 系統 |
| Task queue 清掃 | `temporal-sys-tq-scanner-workflow` | `service/worker/scanner/workflow.go:22` | 系統 |
| 歷史清掃 | `temporal-sys-history-scanner-workflow` | `service/worker/scanner/workflow.go:27` | 系統 |
| Execution 清掃 | `temporal-sys-executions-scanner-workflow` | `service/worker/scanner/workflow.go:32` | 系統 |
| 跨叢集強制複寫 | `force-replication` | `service/worker/migration/force_replication_workflow.go:105` | 系統 |
| Namespace 交接 | `namespace-handover` | `service/worker/migration/handover_workflow.go:13` | 系統 |

這帶來幾個非顯而易見的後果：

1. **`CreateSchedule` 只是啟動一支 workflow**。`workflow_handler.go:3880` 把 schedule ID 加上前綴組成 workflow ID，所以 schedule 的持久性、重試、歷史紀錄全部直接繼承 workflow 引擎，沒有第二套狀態機。
2. **worker 角色掛掉，這些功能就停擺**，但已排程的狀態不會遺失——它們就存在 Mutable State 裡。
3. **除錯方式一致**：schedule 出問題，可以直接對 `temporal-sys-scheduler-workflow` 用 `DescribeWorkflowExecution` 看歷史，跟除錯使用者的 workflow 完全一樣。
4. Schedule 與 Batch 是 per-namespace worker，所以它們的吞吐量受限於每個 namespace 分配到幾台 worker（見[執行期拓樸第 6 節](./runtime-topology.zh-TW.md)的 `LookupN`）。

---

## 5. 正在進行中的方向：CHASM

前四份文件都提到 `chasm/`，這裡從能力角度說明它為什麼存在。

上一節的模式很優雅，但代價明顯：schedule 這種「server 內建功能」被迫走完整的 workflow 路徑（task queue、workflow task、SDK worker 往返），而它其實不需要使用者程式碼。CHASM（`chasm/lib/` 下有 `workflow`、`scheduler`、`activity`、`callback`、`nexusoperation`）把 workflow 一般化成**可註冊的狀態機框架**，讓 schedule 這類元件直接以狀態機形式跑在 history 服務裡，不必繞 worker。

遷移正在灰度中，而且是**同一個 RPC 兩套實作並存**：`workflow_handler.go:3937` 的 `chasmSchedulerCreationEnabled` 決定新建的 schedule 走哪一邊——先看請求標頭有沒有帶 experiment 旗標（`:3938`），再看動態設定 `EnableCHASMSchedulerCreation`（`:3942`），最後用 `namespace\x00scheduleID` 當 key 做百分比取樣（`:3945-3946`）。

`:3949-3953` 的註解說明了一個容易踩雷的區別：**建立**用帶百分比的旗標，**讀取/路由**（`chasmSchedulerEnabled`, `:3954`）刻意不帶百分比，因為「已經用任何百分比建在 CHASM 上的 schedule，之後都必須到 CHASM 去找」。調降百分比只會停止產生新的 CHASM schedule，不會讓既有的迴流。

`workflow_handler.go:3881-3883` 還留了一段註解：即使 CHASM schedule 不需要 workflow ID 前綴，長度驗證仍然對 V1/V2 一律套用，為的是**保留回滾空間**——這是遷移期程式碼的典型痕跡。

另外，`AdminService.MigrateSchedule`（`service/frontend/admin_handler.go:2229`）是把既有 V1 schedule 搬到 CHASM 的運維入口。

設計文件見 [`docs/architecture/chasm.md`](./architecture/chasm.md)。

---

## 6. 運維面：AdminService 在做什麼

`AdminService` 不是給應用程式用的，它是把[資料模型](./data-model.zh-TW.md)與[執行期拓樸](./runtime-topology.zh-TW.md)裡的內部概念直接開出來：

| 類別 | 代表 RPC | 行號（`service/frontend/admin_handler.go`） |
|---|---|---|
| 檢查/修復單筆狀態 | `DescribeMutableState` / `RebuildMutableState` | `:434` / `:320` |
| Shard 操作 | `GetShard` / `CloseShard` / `DescribeHistoryHost` | `:503` / `:516` / `:556` |
| 內部佇列任務 | `ListHistoryTasks` / `RemoveTask` / `AddTasks` / `ListQueues` | `:526` / `:487` / `:2016` / `:2038` |
| DLQ（新版） | `GetDLQTasks` / `PurgeDLQTasks` / `MergeDLQTasks` / `DescribeDLQJob` / `CancelDLQJob` | `:1849` / `:1867` / `:1905` / `:1941` / `:1994` |
| DLQ（舊版，複寫用） | `GetDLQMessages` / `PurgeDLQMessages` / `MergeDLQMessages` | `:1134` / `:1192` / `:1232` |
| 跨叢集複寫 | `StreamWorkflowReplicationMessages` / `GetReplicationMessages` / `SyncWorkflowState` | `:1701` / `:1015` / `:2065` |
| 叢集拓樸 | `DescribeCluster` / `ListClusterMembers` / `AddOrUpdateRemoteCluster` | `:676` / `:790` / `:862` |
| 原始歷史 | `GetWorkflowExecutionRawHistoryV2` / `ImportWorkflowExecution` | `:626` / `:348` |
| 健康檢查 | `DeepHealthCheck` | `:229` |

`CloseShard`(`:516`) 值得一提：它強制卸載一個 shard，下次有請求進來時重新取得租約（`range_id` +1）。這是「某個 history 節點狀態卡住」時最常用的手術刀，而它之所以安全，正是因為 `range_id` fencing 保證舊擁有者的寫入一定會失敗（見[資料模型第 3 節](./data-model.zh-TW.md)）。

`ImportWorkflowExecution`(`:348`) 與 `StreamWorkflowReplicationMessages`(`:1701`) 則是跨叢集遷移的骨幹——配合第 4 節的 `force-replication` 與 `namespace-handover` 兩支 workflow 使用。

---

## 7. 一句話總結

Temporal Server 對外是三個 gRPC service（約 160 個 RPC）加上一個自動生成的 REST 面與一個 Nexus HTTP 面；核心能力是「啟動 / 互動 / 觀測持久化的執行」，其餘的排程、批次、版本管理、namespace 刪除等平台功能，**幾乎全部是用 Temporal 自己的 workflow 實作的**；而 CHASM 正在把這層 dogfooding 從「繞 worker 的 workflow」改成「直接跑在 history 裡的狀態機」。

---

## 8. 自驗指令

```bash
# 三個 service 的註冊點
grep -n "RegisterWorkflowServiceServer\|RegisterAdminServiceServer\|RegisterOperatorServiceServer" service/frontend/service.go

# 重算三個 handler 的方法數（含 Start/Stop 等非 RPC 方法）
grep -c "^func (wh \*WorkflowHandler) [A-Z]" service/frontend/workflow_handler.go
grep -c "^func (h \*OperatorHandlerImpl) [A-Z]" service/frontend/operator_handler.go
grep -c "^func (adh \*AdminHandler) [A-Z]" service/frontend/admin_handler.go

# 列出所有 WorkflowService RPC 與行號
grep -n "^func (wh \*WorkflowHandler) [A-Z]" service/frontend/workflow_handler.go | sed 's/(ctx context.*//'

# interceptor 管線
sed -n '290,330p' service/frontend/fx.go

# 所有系統 workflow type 名稱
grep -rn "WFTypeName\s*=\s*\"\|WorkflowName\s*=\s*\"\|WorkflowType\s*=\s*\"" --include="*.go" service/worker/ | grep -v _test

# CHASM scheduler 的灰度開關
sed -n '3937,3970p' service/frontend/workflow_handler.go
```

## 延伸閱讀

- [專案總覽](./project-overview.zh-TW.md)
- [請求流程導覽](./request-flow.zh-TW.md)——本文第 3.3 節的完整程式碼路徑
- [資料模型導覽](./data-model.zh-TW.md)——本文第 6 節提到的 shard 與 `range_id` fencing
- [執行期拓樸導覽](./runtime-topology.zh-TW.md)——本文第 4 節 per-namespace worker 的分片方式
- [開發流程導覽](./dev-workflow.zh-TW.md)——新增一個 RPC 要改哪些地方、產生碼怎麼重跑
- [`docs/architecture/chasm.md`](./architecture/chasm.md)、[`docs/architecture/nexus.md`](./architecture/nexus.md)、[`docs/architecture/schedules.md`](./architecture/schedules.md)
