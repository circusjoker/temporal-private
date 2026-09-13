# 專案總覽（繁體中文）

> 本文是給第一次接觸這個 repo 的人看的導覽，目的是快速回答「這專案在幹嘛、程式碼怎麼擺、要怎麼動手」。
> 更深入的內部設計請看 [docs/architecture/](./architecture/README.md)；
> 想直接看「一個請求在程式碼裡怎麼跑」請看 [請求流程導覽](./request-flow.zh-TW.md)。

## 一句話總結

這是 **Temporal Server**（`go.temporal.io/server`）的原始碼 —— 一個開源的 **Durable Execution（持久化執行）平台後端**。
使用者用 SDK（Go / Java / TypeScript / Python…）把商業邏輯寫成 Workflow 與 Activity，Temporal Server 負責把這些邏輯
以「即使機器掛掉、網路斷線也能從斷點繼續」的方式跑完，自動處理重試、逾時、狀態持久化與排程。

- 這個 repo **只有伺服器端**：SDK、CLI（`temporal`）、Web UI 都在各自獨立的 repo。
- 使用者的 Workflow / Activity 程式碼跑在**使用者自己的 Worker 行程**裡，Server 不執行使用者程式碼；
  Server 只保存狀態（事件歷史）、派送任務、管理計時器。
- 起源於 Uber 的 Cadence fork，由 Temporal Technologies 維護。目前 server 版本為 `1.33.0`（`common/headers/version_checker.go`）。
- 規模：約 2,950 個 `.go` 檔、~104 萬行 Go，Go 1.27。

## 核心運作模型

1. **Event Sourcing**：每個 Workflow Execution 有一份 append-only 的 History Events；任何時刻都能靠重播歷史還原狀態。
2. **Workflow 必須是決定性的**，副作用被隔離到 Activity（at-least-once 或 at-most-once）。
3. **Task Queue 長輪詢**：Worker 向 Server long-poll 取 Workflow Task / Activity Task，做完回報結果與下一步指令。

## 四個內部服務（`service/`）

| 服務 | 職責 |
| --- | --- |
| `service/frontend` | 對外 gRPC / HTTP API 入口。做 rate limit、驗證、授權、namespace 路由，再轉給 history / matching。也承載 Nexus HTTP handler。 |
| `service/history` | 核心。以 **shard** 為單位管理個別 Workflow Execution：寫入 History Events、維護 Mutable State、產生並處理各種內部佇列任務（transfer / timer / visibility / outbound / archival / replication）。 |
| `service/matching` | 管理 Task Queue 與其 partition，把任務配對給 polling 的 Worker（含 partition forwarding、fairness、backlog 管理）。 |
| `service/worker` | 叢集自己的背景 worker：跨叢集 replicator、scanner、batcher、schedule、namespace 刪除、DLQ、migration 等系統級 workflow。 |

History service 內部的重點子目錄：`shard/`（shard 生命週期）、`workflow/`（Mutable State）、`queues/`（佇列處理框架）、
`api/`（每個 RPC 一個 package）、`ndc/`（跨叢集 history 複寫）、`replication/`、`hsm/`（階層式狀態機）。

## 其他重要目錄

- `chasm/` — **CHASM（Coordinated Heterogeneous Application State Machines）**：把「Workflow」一般化成可註冊的狀態機框架，
  讓 scheduler、nexusoperation、activity、callback 等元件共用 Temporal 的 sharding／儲存／失敗復原基礎設施。
  已有的 library 在 `chasm/lib/`（`workflow`、`scheduler`、`nexusoperation`、`activity`、`callback`）。這是目前演進中的核心架構方向。
- `common/` — 全服務共用模組（約 1,200 個檔）：`persistence/`（儲存抽象層）、`dynamicconfig/`（動態設定）、
  `membership/`（叢集成員與 ring）、`metrics/`、`namespace/`、`nexus/`、`rpc/`、`searchattribute/`、`archiver/`、`quotas/`、`tasks/`。
- `api/` + `proto/` — 內部服務間的 proto 定義與產生碼（`historyservice`、`matchingservice`、`adminservice`…）。
  對外的公開 API 來自 `go.temporal.io/api`（用 `make update-go-api` 更新）。
- `client/` — frontend / history / matching 之間互相呼叫的 client（含 routing、retry、metrics wrapper）。
- `schema/` — 資料庫 schema 與版本化 migration：`cassandra`、`mysql`、`postgresql`、`sqlite`、`elasticsearch`。
- `temporal/`、`cmd/server/` — server 組裝與啟動（以 **uber-go/fx** 做依賴注入，各層都有 `fx.go`）。
- `tools/`、`cmd/tools/` — 維運與開發工具：`tdbg`（debug CLI）、`temporal-sql-tool`／`temporal-cassandra-tool`／
  `temporal-elasticsearch-tool`（schema 安裝）、以及各種 codegen（`protogen`、`gendynamicconfig`、`genrpcwrappers`…）。
- `config/` — 本機開發用的多種組合設定（cassandra/mysql/postgres/sqlite × elasticsearch × 多叢集 XDC × JWT × archival）。
- `tests/` — functional / integration 測試（需要跑起測試叢集），單元測試則與程式碼同目錄。

## 儲存與可觀測性

- **主儲存（persistence）**：Cassandra、MySQL、PostgreSQL、SQLite；抽象在 `common/persistence`，
  下面分 `nosql/` 與 `sql/` 實作，另有 `faultinjection/` 用於故障注入測試。
- **Visibility（查詢/搜尋）**：標準 SQL visibility 或 Elasticsearch 進階 visibility（支援 Search Attributes）。
- **Archival**：歷史與 visibility 資料可歸檔到 S3／GCS／檔案系統。
- **多叢集複寫（XDC / Global Namespace）**：由 history 的 replication 任務 + worker 的 replicator 完成，支援 namespace failover。
- 全面的 metrics（`common/metrics`）、結構化 log、動態設定（`common/dynamicconfig`，不重啟即可調參）。

## 常用開發指令（`Makefile`）

```bash
make bins            # 建出 temporal-server、tdbg、schema 工具
make unit-test       # 單元測試（最快的回饋）
make functional-test # 需要 DB 的功能測試
make lint-code-fast  # 只 lint 有變動的 package
make fmt-imports     # 整理 import
make proto           # 重新產生 proto 相關程式碼
```

測試相關約定（見 `AGENTS.md`）：跑測試一律加 `-tags test_dep`；整合測試才加 `integration` tag；
單元測試偏好 `require` 而非 `assert`，用 `require.Eventually` 取代 `time.Sleep`（linter 會擋）。

## 建議的閱讀順序

1. [`docs/request-flow.zh-TW.md`](./request-flow.zh-TW.md) — 一個 Workflow 從 Start 到 Activity 完成，逐個檔案的程式碼路徑。
2. `docs/architecture/README.md` — 系統全貌與 Workflow/Activity Task 流程。
3. `docs/architecture/workflow-lifecycle.md` — 一個 Workflow 從 start 到 complete 的序列圖。
4. `docs/architecture/history-service.md`、`matching-service.md` — 兩個最核心服務的內部機制。
5. `docs/architecture/chasm.md` — 新一代狀態機框架（理解 repo 未來走向的關鍵）。
6. `service/history/README.md`、`service/matching/fairness.md` — 更貼近程式碼的說明。
