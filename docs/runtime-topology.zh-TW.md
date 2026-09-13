# 執行期拓樸導覽：一個 binary 怎麼變成一個叢集（繁體中文）

> 前三篇回答了「這專案是什麼」「一個請求怎麼跑」「狀態存在哪」。
> 這篇回答剩下的那塊：**程式跑起來之後長什麼樣子** —— 一個 binary 怎麼同時／分別扮演四種角色、
> 多台機器怎麼互相找到對方、哪個 shard 歸誰管、設定從哪裡來、上下線時怎麼不掉請求。
>
> 建議先讀 [專案總覽](./project-overview.zh-TW.md)、[請求流程](./request-flow.zh-TW.md)、[資料模型](./data-model.zh-TW.md)。
> 本文所有 `file:line` 皆對照 commit 當下的原始碼逐行驗證過。

---

## 0. 一張圖

```
                    ┌─────────────────────────────────────────────┐
                    │  DB: cluster_membership 表（成員名冊+心跳）  │
                    └───────▲──────────────────────▲──────────────┘
              upsert 心跳   │                      │  讀出 seed 清單
                            │                      │
   ┌────────────────────────┴──────────────────────┴─────────────────────┐
   │                   Ringpop / SWIM gossip 叢集                        │
   │   每個 service 一個 consistent hash ring（frontend/history/…）      │
   └──────┬─────────────────┬────────────────┬──────────────┬────────────┘
          │                 │                │              │
     ┌────▼────┐      ┌─────▼─────┐    ┌─────▼─────┐  ┌─────▼─────┐
     │frontend │      │  history  │    │ matching  │  │  worker   │
     │ (無狀態) │      │ 擁有 shard │    │擁有 TQ分區│  │擁有系統 wf│
     └─────────┘      └───────────┘    └───────────┘  └───────────┘
       hash key:        hash key:         hash key:      hash key:
        （不分片）       shardID          ns:tq:type     namespaceID
```

一句話：**所有分片決策都是同一個機制** —— 把一個字串丟進該 service 的 hash ring，
`Lookup()` 出來是誰就是誰負責。沒有 master、沒有協調者、沒有 etcd/ZooKeeper。

---

## 1. 一個 binary、五種角色

進入點是單一執行檔 `temporal-server`（`cmd/server/main.go:34` 的 `buildCLI`），
`start` 子命令（`cmd/server/main.go:126`）用 `--service` 決定這個行程要扮演哪些角色。

| 角色（`primitives.ServiceName`） | 預設啟動 | 說明 |
| --- | --- | --- |
| `frontend` | ✅ | 對外 gRPC／HTTP API |
| `history` | ✅ | 擁有 shard，跑 workflow 狀態機 |
| `matching` | ✅ | 擁有 task queue 分區 |
| `worker` | ✅ | 叢集自己的系統 workflow |
| `internal-frontend` | ❌ | 給系統元件用的內部 frontend，預設不啟動 |

- 全部五個在 `temporal/server.go:25` 的 `Services`，預設四個在 `temporal/server.go:35` 的 `DefaultServices`。
- `internal-frontend` 與 `frontend` 共用同一份程式碼（`temporal/fx.go:576` 的 `genericFrontendServiceProvider`），
  差別只有 claim mapper 被換成 `authorization.NewInternalClaimMapper()`（`temporal/fx.go:592`），
  而這個 mapper **無條件把呼叫者當成系統管理員**：回傳 `System: RoleAdmin`
  且 `AuthInfoRequired()` 為 false（`common/authorization/claim_mapper.go:72-78`）。
  所以它預設不開，開了也**絕對不能對外曝露**。

### 組裝方式：fx 的巢狀 app

整個 server 用 **uber-go/fx** 做依賴注入，而且是「**app 裡面再開 app**」的兩層結構：

1. 外層 `fx.New`（`temporal/fx.go:163`）套用 `TopLevelModule`（`temporal/fx.go:138`），
   裡面 provide 了五個 service provider（`temporal/fx.go:144-148`）。
2. 每個 service provider 各自再開一個**獨立的 `fx.App`**：
   - `HistoryServiceProvider` → `temporal/fx.go:536`（`history.QueueModule` + `history.Module` + `replication.Module`）
   - `MatchingServiceProvider` → `temporal/fx.go:556`（`matching.Module`）
   - frontend → `temporal/fx.go:585`（`frontend.Module`）
   - `WorkerServiceProvider` → `temporal/fx.go:623`（`worker.Module`）
3. 沒被 `--service` 點名的角色會直接回傳空的 `ServicesGroupOut` 並記一行 log
   （例如 `temporal/fx.go:532`），連依賴都不會被建出來。

四個 app 共用的基礎設施（logger、persistence factory、client bean、membership、metrics…）
來自 `GetCommonServiceOptions`（`temporal/fx.go:414`），它會把 `common/resource` 的 `Module`
（`common/resource/fx.go:80`）疊進去。**這是「加一個跨服務共用元件要改哪裡」的答案。**

### 同行程多角色時的啟動順序

開發用的 all-in-one 模式（四個角色跑在同一個行程）有硬編的順序表
`initOrder`（`temporal/server_impl.go:44-52`）：`matching(1) → history(2) → frontend(3) → worker(4)`，
停止時反向（`temporal/server_impl.go:114` 用負號排序）。
理由寫在註解裡：worker 依賴 frontend，frontend 依賴 matching 與 history。

啟動前還會先確保系統 namespace 存在（`temporal/server_impl.go:92` 呼叫的 `initSystemNamespaces`，本體在 `temporal/server_impl.go:148`），
這就是 `temporal-system` namespace 的來源。

---

## 2. 兩套設定系統（別搞混）

| | 靜態設定 (static config) | 動態設定 (dynamic config) |
| --- | --- | --- |
| 檔案 | `config/*.yaml` | `config/dynamicconfig/*.yaml` |
| 載入 | `config.Load()`（`common/config/loader.go:129`） | `FileBasedClient` 定期輪詢 |
| 生效 | **重啟才生效** | **執行中熱更新** |
| 內容 | DB 連線、port、TLS、cluster metadata、archival | 各種 rate limit、開關、timeout、feature flag |
| 結構 | `config.Config`（`common/config/config.go:30`） | `Setting` + 精度階層 |

### 靜態設定的三種載入方式

`cmd/server/main.go:169-177` 決定走哪一條：

1. `--config-file <path>`：直接指定單一檔案。
2. `--config/--env/--zone`（已標記 deprecated）：走 `loadLegacy`（`common/config/loader.go:178`），
   依序疊 `base.yaml` → `<env>.yaml` → `<env>_<zone>.yaml`，後者覆蓋前者（註解在 `common/config/loader.go:174-176`）。
3. 什麼都不給：用**編譯進 binary 的內建樣板**（`common/config/loader.go:20` 的 `//go:embed`，
   在 `common/config/loader.go:145` 被選用），全部從環境變數取值。這就是容器化部署最常走的路徑。

`config/` 底下那一堆 `development-*.yaml` 是本機開發用的組合（cassandra／mysql／postgres／sqlite ×
elasticsearch × 多叢集 XDC × JWT × archival），不是生產設定。

> ⚠️ 有些欄位寫了也沒用：`persistence.numHistoryShards` 在啟動時會被 DB 裡的值覆蓋
> （`temporal/fx.go:876-883`），詳見[資料模型](./data-model.zh-TW.md)。

### 動態設定的精度階層

動態設定不是單純的 key→value，每個 key 屬於某個**精度類別（Precedence）**。
目前有八種（`common/dynamicconfig/setting_gen.go:13-22`）：

```
Global、Namespace、NamespaceID、TaskQueue、ShardID、TaskType、Destination、ChasmTaskType
```

精度類別決定的不是「大小」，而是**這個 key 的查找順序（constraint 列表）**，由產生的 `Get` 方法給出。
以 `TaskQueue` 為例（`common/dynamicconfig/setting_gen.go:1319-1324`）：

```
{ns + taskQueue + taskType} → {ns + taskQueue} → {taskQueue} → {ns} → {}（全域預設）
```

由上往下第一個命中的就是答案。所以你可以只把某個吵鬧 namespace 的 rate limit 調低、
或只針對某一條 task queue 開某個開關，不影響其他人。
每份 constraint 列表都必須以「無條件」結尾，否則視為 server bug（`common/dynamicconfig/collection.go:358`）。

檔案輪詢的最小間隔是 5 秒（`common/dynamicconfig/file_based_client.go:20` 的 `minPollInterval`，
設定欄位在 `common/dynamicconfig/file_based_client.go:34`），設更小會在驗證時被拒絕。
`temporal-server validate-dynamic-config <file>` 可以先檢查 key 名稱與型別（`cmd/server/main.go` 的同名子命令）。

---

## 3. 成員探索：DB 當名冊，gossip 當真相

Temporal **不需要額外的協調服務**。成員探索分兩段（`common/membership/ringpop/monitor.go:124` 的 `Start`）：

1. **寫進 DB 讓別人找得到自己**：`startHeartbeat` → `upsertMyMembership`
   （請求組在 `common/membership/ringpop/monitor.go:290`），寫入 `cluster_membership` 表，
   記錄 TTL 48 小時（`common/membership/ringpop/monitor.go:32`，註解說留這麼久是為了讓人事後查表 debug）。
   之後進入心跳迴圈（`common/membership/ringpop/monitor.go:358`）。
2. **從 DB 撈 seed 清單，然後加入 gossip**：`fetchCurrentBootstrapHostports`
   （`common/membership/ringpop/monitor.go:321`）呼叫 `GetClusterMembers` 時帶
   `LastHeartbeatWithin: healthyHostLastHeartbeatCutoff`（20 秒，`common/membership/ringpop/monitor.go:35`），
   只撈活著的節點；接著 `bootstrapRingPop`（`common/membership/ringpop/monitor.go:190`）
   用這些 seed 加入 **Ringpop（SWIM gossip）** 叢集，失敗最多重試 5 次
   （`common/membership/ringpop/monitor.go:38`）。

**DB 只是 seed 名冊，即時成員狀態靠 gossip。** 節點掛掉時，是 gossip 在秒級傳播，不是等 DB 記錄過期。

程式碼裡有個誠實的註解（`common/membership/ringpop/monitor.go:138-140`）：先寫 DB 再 bootstrap
會有一個小的競態視窗，這是 ringpop 函式庫結構造成的限制。

### Hash ring

每個 service 一個 ring（`common/membership/ringpop/monitor.go:116`），用 `farm.Fingerprint32`
做 consistent hashing，每台主機在環上放多個虛擬節點（replica points）以求分佈均勻
（`common/membership/ringpop/service_resolver.go:120-121`）。
虛擬節點數與 gossip 傳播時間估計值都是動態設定（`common/membership/ringpop/factory.go:112-113`）。

### 不想用 gossip？

`temporal/fx.go:415-418`：如果啟動時給了靜態主機清單，membership 模組會換成
`static.MembershipModule`（`common/membership/static/fx.go:11`），直接用固定清單當成員。
測試與 Kubernetes 上用 headless service 的部署會走這條。

---

## 4. Shard 歸誰：三層一致性檢查

Temporal 對 shard 擁有權用了**三道防線**，一層比一層可靠：

### 第一層：hash ring（快，但可能短暫不一致）

- Workflow → shard：`common.WorkflowIDToHistoryShard`（`common/util.go:418`），
  `farm.Fingerprint32(namespaceID + "_" + workflowID) % numShards + 1`（`common/util.go:422-424`，
  注意 shardID 從 1 開始）。history 服務的入口在 `service/history/configs/config.go:903`。
- shard → 主機：把 shardID 當字串丟進 history ring 做 `Lookup`
  （`service/history/shard/ownership.go:136`）。

### 第二層：ownership 元件（本機認知）

`ownership`（`service/history/shard/ownership.go:20-27` 的型別註解講得很清楚）夾在 membership 與
shard controller 之間：

- 向 ring 註冊監聽器（`service/history/shard/ownership.go:71`），成員變動時觸發
  controller 的 `acquireShards`（`service/history/shard/controller_impl.go:375`）。
- `acquireShards` 會**掃過全部 shard**（`service/history/shard/controller_impl.go:440` 用隨機起點避免所有主機同時搶同一個），
  對每個 shard 呼叫 `verifyOwnership`（`service/history/shard/controller_impl.go:403`）：
  該我的就開起來，不該我的就關掉。
- 關掉時不一定立刻關：若啟用 shard linger（`service/history/shard/controller_impl.go:300` 的 `shardLingerThenClose`），
  會先讓 shard 多活一陣子並定期 `AssertOwnership`（`service/history/shard/controller_impl.go:369`），
  讓進行中的請求做完，減少 rolling restart 時的抖動。

### 第三層：`range_id` 租約（唯一真正權威）

hash ring 在成員變動期間**可能兩台主機同時認為自己擁有同一個 shard**。
真正阻止雙寫的是 DB 上的 `range_id` 條件式更新（見[資料模型](./data-model.zh-TW.md)）：
舊主機的寫入會條件失敗並得到 `ShardOwnershipLostError`（`service/history/shard/controller_impl.go:577`
的判定），然後放掉 shard。**ring 只是最佳化，正確性由 DB 租約保證。**

### 呼叫端怎麼找到 shard 的擁有者

`client/history` 的 `CachingRedirector` 快取「shardID → 主機位址」：
`Execute`（`client/history/caching_redirector.go:94`）→ `redirectLoop`
（`client/history/caching_redirector.go:105`），一旦收到 `ShardOwnershipLost`
（`client/history/caching_redirector.go:123`）就清掉快取、重新 `shardLookup`
（`client/history/redirector.go:32`）並重試。
背景還有 `staleCheck`（`client/history/caching_redirector.go:254`）定期比對快取與 ring 是否還一致。

---

## 5. Matching 與 Worker 的分片：同樣的 ring，不同的 key

**Matching 完全不知道 shard 的存在。** 它的 hash key 是 task queue 分區：

- `client/matching/client.go:465` 的 `Route` → `c.clients.Lookup(p.RoutingKey(spread))`（`:468`）。
- key 的組成在 `common/tqid/task_queue_id.go:456`：`namespaceId:taskQueueName:taskType`
  （或帶 partition batch 編號）。
- `spread` 是動態設定控制的：batchSize 為 0 時單一 key，非 0 時把多個 partition 打散到多台主機上
  （`common/tqid/task_queue_id.go:458-469` 的註解說明這是為了限制 `LookupN` 的 O(n) 成本）。

**Worker 服務的 per-namespace worker 也是同一招**：`service/worker/pernamespaceworker.go:275`
用 `LookupN(key, count)` 決定哪幾台 worker 主機負責跑某個 namespace 的系統 workflow（如 schedules）。

所以 history／matching／worker 三種分片彼此完全獨立，可以各自獨立擴縮。

---

## 6. 優雅上下線（這段是營運最該懂的）

### 啟動

| 服務 | 行為 | 位置 |
| --- | --- | --- |
| history | 先開 gRPC server，**之後**才加入 membership | `service/history/service.go:94-112` |
| history | 健康狀態先設 `NOT_SERVING`，等 `InitialShardsAcquired` 再轉 `SERVING`（再多等 5 秒穩定） | `service/history/service.go:80-89` |
| history | 可設定 `StartupMembershipJoinDelay` 延後加入 ring | `service/history/service.go:104-111` |
| matching | 開 server 後立刻 `SERVING` 並加入 membership | `service/matching/service.go:72-83` |
| frontend | 註冊 workflow／admin／operator 三個 gRPC service，另起 HTTP server（Nexus） | `service/frontend/service.go:512-546` |
| worker | 加入 membership → 確保系統 namespace → 起 scanner／replicator／per-namespace worker | `service/worker/service.go:258-283` |

history 的順序有明確註解（`service/history/service.go:101-102`）：
**一旦加入 membership，別人馬上就會把該 shard 的請求送過來，所以 gRPC server 必須先就緒。**
而 `StartupMembershipJoinDelay` 的註解（`service/history/service.go:105-107`）說明用途是
rolling upgrade 時錯開「舊節點下線」與「新節點上線」造成的兩次 shard 搬移。

### 關閉

history 與 matching 的 `Stop` 幾乎一樣（`service/history/service.go:117`、`service/matching/service.go:87`）：

1. **先把自己從 ring 上移除**，再等流量排空 —— 不是先關 server。
2. 兩種移除模式：`EvictSelf()` 立刻移除；或設了 `AlignMembershipChange` 時改用
   `EvictSelfAt(asOf)`，把移除時間對齊到未來的整數時間點
   （`service/history/service.go:123-131`）。對齊的用意是讓**所有節點在同一瞬間看到同一份 ring**，
   避免各節點對「誰擁有哪個 shard」的認知在過渡期分歧。
3. 等待時間依模式而定：對齊模式等 `傳播時間 + ShardLingerTimeLimit + ShardFinalizerTimeout`
   （`service/history/service.go:137-146`）；否則等 `ShutdownDrainDuration`。
4. 最後才停 handler／shard controller 與 gRPC server。

因為時間戳是跨節點比較的，`monitor` 對排程的加入／離開時間設了 15 秒硬上限來限制時鐘偏移的影響
（`common/membership/ringpop/monitor.go:40-47` 的註解）。

---

## 7. 想自己確認的話

```bash
# 這個 binary 支援哪些角色、預設啟動哪些
sed -n '24,41p' temporal/server.go

# 五個 service 各自的 fx app 組成
grep -n "ServiceProvider(" temporal/fx.go

# 所有會走 membership lookup 的地方（= 所有分片決策點）
grep -rn '\.Lookup(\|\.LookupN(' --include='*.go' service/ client/ common/ | grep -v _test

# 動態設定的精度階層
sed -n '13,22p' common/dynamicconfig/setting_gen.go

# 本機 all-in-one 開發設定長什麼樣（sqlite，1 個 shard）
sed -n '1,15p' config/development-sqlite.yaml
```

---

## 接下來讀什麼

- 想看 shard 內部怎麼跑 workflow → [請求流程導覽](./request-flow.zh-TW.md)
- 想看 `range_id` 租約與儲存結構 → [資料模型導覽](./data-model.zh-TW.md)
- 想看 history 服務內部的佇列處理框架 → [docs/architecture/history-service.md](./architecture/history-service.md)
- 想看 matching 的分區轉發與 backlog → [docs/architecture/matching-service.md](./architecture/matching-service.md)
- 想看 worker 角色到底跑了哪些系統 workflow → [能力地圖第 4 節](./feature-map.zh-TW.md)
- 想在本機把這套拓樸跑起來（含 XDC 三叢集） → [開發流程導覽](./dev-workflow.zh-TW.md)
