# 開發流程導覽：從改一行程式碼到通過 CI（繁體中文）

> 前四份文件回答「這個系統長什麼樣」，這份回答「我要怎麼動手改它」。
> 涵蓋：本機跑起來、建置系統的 build tag 設計、四層測試金字塔、程式碼生成鏈、分層 lint、CI 怎麼決定跑多少測試、除錯工具。
> 前置閱讀：[專案總覽](./project-overview.zh-TW.md)。英文的權威來源是 [`CONTRIBUTING.md`](../CONTRIBUTING.md) 與 [`docs/development/testing.md`](./development/testing.md)，本文是導覽與補充，不取代它們。

---

## 1. 五分鐘跑起來

最短路徑**完全不需要 Docker**：

```bash
make bins     # 建出 temporal-server / tdbg / 三個 schema 工具
make start    # = make start-sqlite（Makefile:733）
```

`make start` 用的 `config/development-sqlite.yaml` 是純記憶體 SQLite（`connectAttributes.mode: "memory"`，
`config/development-sqlite.yaml:19`），而且 `numHistoryShards: 1`（`config/development-sqlite.yaml:8`）——
**單 shard、無外部依賴、關掉就清空**，適合快速驗證一個行為。注意 shard 數是啟動時寫進 DB 且不可再改的
（見[執行期拓樸導覽](./runtime-topology.zh-TW.md)），所以這份設定不適合用來觀察 shard 相關行為。

想要完整的開發環境（含 Web UI 與 metrics）再開第二個終端機：

```bash
make start-dependencies   # docker compose up（Makefile:715）
```

`develop/docker-compose/docker-compose.yml` 起的是 **mysql、cassandra、postgresql、elasticsearch、
prometheus、grafana、tempo、temporal-ui** 八個服務。Web UI 在 `localhost:8080`。
用完 `make stop-dependencies`。

其他 persistence 組合都是「先裝 schema，再用對應設定啟動」的兩步：

| 目標 | 裝 schema | 啟動 | 設定檔 |
| --- | --- | --- | --- |
| SQLite 記憶體（預設） | 不需要 | `make start` | `config/development-sqlite.yaml` |
| SQLite 檔案 | 不需要 | `make start-sqlite-file` | `config/development-sqlite-file.yaml` |
| Cassandra + ES | `make install-schema-cass-es` | `make start-cass-es` | `config/development-cass-es.yaml` |
| MySQL 8 | `make install-schema-mysql` | `make start-mysql` | `config/development-mysql8.yaml` |
| PostgreSQL 12 | `make install-schema-postgresql` | `make start-postgres` | `config/development-postgres12.yaml` |
| XDC 三叢集 | `make install-schema-xdc` | `make start-xdc-cluster-a`／`-b`／`-c` | `config/development-cluster-{a,b,c}.yaml` |
| JWT 授權 | 同上擇一 | `make start-jwt` | `config/development-jwt.yaml` |

SQLite 兩種模式不需要裝 schema，是因為 plugin 自己會建：`setupSQLiteDatabase` 在連上之後直接呼叫
`sqliteschema.SetupSchemaOnDB(db)` 把所有表建起來（`common/persistence/sql/sqlplugin/sqlite/plugin.go:152-163`）。
其餘後端的 schema 目標定義在 `Makefile:639-710`；`install-schema-*` 一律先 `drop -f` 再 `create`，
**是破壞性的**，不要對著有資料的環境跑。除了 `start-jwt` 之外，所有啟動目標都帶 `--allow-no-auth`（`Makefile:736-776`）——本機開發不做授權檢查。

起來之後建一個 namespace 才能跑 sample：

```bash
temporal operator namespace create -n default
```

---

## 2. 建置系統：三個 build tag 與 `.bin` 自舉

### Build tag

| Tag | 作用 | 何時需要 |
| --- | --- | --- |
| `disable_grpc_modules` | 排除 `cloud.google.com/go/storage` 的 gRPC 依賴，binary 少 16MB | **所有建置預設帶上**（`Makefile:50`） |
| `test_dep` | 啟用 `testhooks` 的真實實作 | **所有測試預設帶上**（`Makefile:51`）；少數測試沒有它會直接失敗 |
| `TEMPORAL_DEBUG` | 放大 functional test 的 timeout，讓你有時間在中斷點停留 | 手動 debug 時；`make temporal-server-debug`（`Makefile:390`） |

所以正確的手動指令永遠是 `go test -tags disable_grpc_modules,test_dep ...`——
這就是 `AGENTS.md` 反覆強調「一律加 `-tags test_dep`」的由來。IDE 裡也要把這串填進 build tags 才跑得動
（`docs/development/testing.md` 的 GoLand 一節）。

`CGO_ENABLED ?= 0`（`Makefile:37`）是預設，編譯明顯較快；唯一明確覆寫成 `CGO_ENABLED=1` 的是
`mixed-brain-test`（`Makefile:563`）。

> **一個已過期的約定**：`AGENTS.md` 寫「integration 測試才加 `integration` tag」，但 repo 內現在**沒有任何
> `//go:build integration`**（`grep -rn "go:build integration" --include="*.go" .` 為空）。
> integration 測試是靠**目錄**（`Makefile:128`）區分的，不是 build tag。Makefile 的 `TEST_TAG` 變數留給你臨時加 tag 用，預設為空。

### 工具自舉

所有開發工具（golangci-lint、buf、api-linter、gotestsum、nilaway、yamlfmt、actionlint、
protoc-gen-*、mockgen、stringer、gowrap…）都以**釘死的版本號**裝進 repo 內的 `.bin/`
（`Makefile:80`），並把 `.bin` 前置到 `PATH`（`Makefile:82`）。檔名帶版本（如 `.bin/golangci-lint-v2.13.0`），
所以升版會自動重裝、不會用到你系統上的舊版。清掉重來：`make clean-tools`。

### 產出的 binary

`make bins` = `temporal-server`、`temporal-cassandra-tool`、`temporal-sql-tool`、
`temporal-elasticsearch-tool`、`tdbg`（`Makefile:6`）。另外兩個非預設目標：
`temporal-server-debug`（`Makefile:390`）與 `fairsim`（`Makefile:374`，matching fairness 模擬器）。

---

## 3. 四層測試金字塔

| 層 | 目標 | 位置 | 需要外部依賴 | 指令 |
| --- | --- | --- | --- | --- |
| Unit | 單一 package，只用 gomock | 與程式碼同目錄 | 否 | `make unit-test` |
| Integration | 真的打 DB／schema 工具 | `common/persistence/tests`、`tools/tests`、`temporaltest`（`Makefile:128`） | 是（SQLite 除外） | `make integration-test` |
| Functional | 端到端，起一整個測試叢集 | `tests/`、`tests/ndc`、`tests/xdc`（`Makefile:122-124`） | 是（SQLite 除外） | `make functional-test` |
| Mixed-brain | 跨版本／跨叢集混合行為 | `tests/mixedbrain`（`Makefile:125`） | 是（PostgreSQL） | `make mixed-brain-test` |

另外兩個專項：`make functional-with-fault-injection-test`（`Makefile:554`，對 persistence 注入故障）
與 `make leak-test`（`Makefile:571`，反覆開關叢集抓 goroutine 洩漏）。

### 「單元測試」是用扣除法定義的

`UNIT_TEST_DIRS` 不是列舉，而是**從所有含 `_test.go` 的目錄裡扣掉 functional／integration 的目錄**
（`Makefile:131`），再手動把 `tests/testcore` 自己的單元測試加回來（`Makefile:134`）。
實務含意：**你在任何新目錄下寫的測試，預設就會被 `make unit-test` 跑到**，不需要註冊；
但如果它需要 DB，就會在 CI 的 unit test job 裡爆掉——要嘛放進 integration/functional 目錄，要嘛去掉依賴。

### 每次測試都帶的旗標

`COMPILED_TEST_ARGS`（`Makefile:70-75`）預設含 **`-race`**（`TEST_RACE_FLAG ?= on`，`Makefile:66`）、
**`-shuffle on`**（`Makefile:68`）與 `-timeout 35m`（`Makefile:59`）。
兩個推論：本機跑測試比你想的慢是因為 race detector；測試順序每次都不同，
**任何測試間的隱性順序依賴都會隨機爆炸**，這是刻意的。要臨時關掉：`make TEST_RACE_FLAG=off unit-test`。

### 成功判定不看 exit code

`unit-test` 等目標把輸出 `tee` 進 `test.log`，再跑 `verify-test-log`（`Makefile:582-585`）：
檔案不得為空、必須有 `^ok`、不得有 `^--- FAIL`。因為 pipe 到 `tee` 之後 make 看到的是 `tee` 的 exit code，
所以這個文字檢查才是真正的守門員。

> **本機踩雷點**：`test.log` 是用 `tee -a` **累加**的，而 `clean-test-output`（`Makefile:528-531`）只刪
> `./.testoutput`（`Makefile:141`）與 testcache，**沒有任何 make 目標會刪掉 `test.log`**（它被 `.gitignore:21` 忽略所以你也不會注意到）。
> 後果：一旦有過失敗的測試，那行 `--- FAIL` 會永遠留在檔案裡，之後就算全部修好，`verify-test-log` 還是會報失敗。
> 修好之後記得先 `rm test.log` 再重跑。

### 只編譯不執行

`make build-tests`（`Makefile:533`）用 `-exec="true" -count=0` 只做編譯檢查——
改了共用介面想先確認全 repo 測試還編得過時最快。

### 測試撰寫慣例（細節見 `docs/development/testing.md`）

- 叢集：`testcore.NewEnv(t)`（`tests/testcore/test_env.go:258`），每個測試自己的 namespace；舊的 `FunctionalTestBase` 已不建議用於新測試。
- 識別字：用 `testvars`（`tv.WorkflowID()`、`tv.Any()`）而不是手寫字串。
- 等待：用 `await.Require` / `await.RequireTrue`，**`time.Sleep` 被 linter 直接禁止**（`.github/.golangci.yml:35`）。
- 歷史事件斷言：`historyrequire` 的 `EqualHistoryEvents` 可以直接貼一段文字化的事件序列。
- proto 比對：`protorequire.ProtoEqual`，可用 `IgnoreFields` 忽略時間戳。
- 平行化：所有測試與子測試都該 `t.Parallel()`，`make parallelize-tests`（`Makefile:494`）會自動補上，
  不想被補的加 `//parallelize:ignore`。

---

## 4. 程式碼生成鏈

這個 repo 有大量產生碼，改錯地方會被 CI 的 `ensure-no-changes`（`Makefile:809`）抓出來——
它單純檢查 `git status --porcelain` 是否乾淨。

| 你改了什麼 | 要跑什麼 | 產出到哪 |
| --- | --- | --- |
| `proto/internal/**.proto`、`chasm/lib/**.proto` | `make proto` | `api/`（含 gRPC stub 與 mock） |
| 對外 API（在 `go.temporal.io/api` repo） | `make update-go-api` | `go.mod`／`go.sum`（`Makefile:351`） |
| 服務間 client 介面 | `make proto-codegen`（`Makefile:341`） | `client/`、`common/rpc/interceptor/` |
| 任何 `//go:generate`（mock、stringer、gowrap） | `make go-generate`（`Makefile:805`） | 各自目錄 |
| `common/dynamicconfig` 的設定鍵定義 | `go generate` 經 `cmd/tools/gendynamicconfig` | 產生各精度的 `Get` 方法 |

`make proto`（`Makefile:27`）其實是 `lint-protos → lint-api → protoc → proto-codegen` 四步；
`protoc` 這步（`Makefile:326`）是由 `cmd/tools/protogen` 這支自製工具統籌呼叫 protoc 與各 plugin，
不是直接的 protoc 指令，所以**本機要有 `protoc`**（macOS: `brew install protobuf`）。

新增一個 RPC 的完整步驟見 [`docs/development/new-rpcs.md`](./development/new-rpcs.md)：
除了 proto 與產生碼，還要動 `common/metrics/defs.go`、`service/frontend/redirection_interceptor.go`
與 `service/<service>/configs/quotas.go`。
（註：該文件第 12 行寫的 `make service-clients` 目標在現行 Makefile 已不存在，對應的是 `make proto-codegen`。）

---

## 5. 分層 lint

`make check` = `make lint` + `shell-check`；`make lint`（`Makefile:400`）再展開成五個：

| 目標 | 檢查什麼 |
| --- | --- |
| `lint-code` | golangci-lint + 自製 `errortype` vet 工具（`Makefile:426`） |
| `lint-actions` | GitHub Actions 語法（actionlint） |
| `lint-api` | proto 是否符合 Google API 設計規範（api-linter） |
| `lint-protos` | buf lint，內部 proto 與 CHASM proto 各一份 |
| `lint-yaml` | yamlfmt 格式 |

### 只看你新增的問題

`lint-code` 帶 `--new-from-rev=$(GOLANGCI_LINT_BASE_REV)`，預設值是 `main`（`Makefile:177`）。
意思是**既有程式碼的既有違規不會擋你**，只有你這次新增的才報。
`lint-code-fast`（`Makefile:409`）更進一步：先用 `git diff` + `git ls-files --others` 算出有改動的目錄，
**在分析前就縮小輸入範圍**，所以快得多——日常開發用這個。
另外預設 `GOLANGCI_LINT_FIX ?= true`（`Makefile:178`），**它會直接改你的檔案**，跑完記得看 `git diff`。

### 啟用的檢查器與專案自訂規則

`.github/.golangci.yml:9-21` 明確 `default: none` 再逐一啟用：`errcheck`、`importas`、`depguard`、
`revive`、`staticcheck`、`govet`、`forbidigo`、`exhaustive`、`godox`、`iotamixing`、`testifylint`。
其中 `forbidigo` 是專案的價值觀所在（`.github/.golangci.yml:33-40`）：禁止 `time.Sleep`（要用 `await`）、
禁止 `panic`、在 `chasm/lib` 非測試檔禁止 `time.Now`（要用 `ctx.Now(component)`，因為狀態機必須是決定性的）。

### 兩個特殊 linter

- `make workflowcheck`（`Makefile:518`）：對 `service/worker` 下每個目錄跑 SDK 的 `workflowcheck`，
  驗證**系統 workflow 的決定性**——呼應「平台功能本身就是 Workflow」的 dogfooding 設計（見[能力地圖](./feature-map.zh-TW.md)）。
- `make lint-nilaway`（`Makefile:448`）：nil 安全分析，但 `NILAWAY_SCOPE` 預設只有 `chasm/lib/scheduler`
  一個 package（`Makefile:447`）。註解說明原因：不限制範圍的話 nilaway 會分析整個依賴圖並把 CI runner 撐爆記憶體。
  這是**漸進式導入**，隨著更多 package 變 nil-clean 才會擴大。

---

## 6. CI 怎麼決定跑多少測試

`.github/workflows/run-tests.yml` 不是每次都跑全部。決策在 `Determine test scope`（`:67`）：

**跑全量（8 種 DB 組合 × 全部 functional test）的三個條件**（`:80-92`）——
1. 是 push 事件（main 或 release branch）；
2. PR 帶 `test-all-dbs` label；
3. diff 動到 `common/persistence/` 或 `schema/`。

否則只有 `required: true` 的兩組（**cass_es** 與 **postgres12**）跑全量，其餘六組
（cass_es8、cass_os2、cass_os3、sqlite、mysql8、postgres12_pgx，`:105-140`）只跑 smoke——
也就是 `-run=TestActivityTestSuite|TestSignalWorkflowTestSuite|TestWorkflowTestSuite` 這三個套件（`:150`）。

**實務含意**：你改 persistence 以外的東西，CI 綠燈**不代表**在 MySQL／OpenSearch 上也綠。
懷疑跟 DB 有關就自己加 `test-all-dbs` label。

### 分片與那個神祕的 salt

Functional test 切成 5 個 shard（`SHARD_COUNT: 5`，`:28`）。分片不是靠外部工具，而是測試自己做的：
`tests/testcore/test_env.go:719-740` 的 `checkTestShard` 把 `t.Name()` 加上一段 salt 做 farmhash，
取模後不屬於本 shard 的測試直接 `t.Skip`。

salt 來自 `//go:embed shard_salt.txt`（`tests/testcore/test_env.go:42-46`），目前內容是 `-salt-2661`。
**這個字串是機器算出來的**：`.github/workflows/optimize-test-sharding.yml` 每天 07:00 UTC（`:6`）
用 `cmd/tools/optimize-test-sharding` 搜尋能讓五個 shard 耗時最平均的 salt，然後自動開 PR、自動核准、自動合併。
所以 repo 裡看到一個沒人看得懂的數字檔案，不要手動改它。

---

## 7. 除錯工具箱

- **`tdbg`**（`tools/tdbg/`）：管理員 CLI。直接讀 Mutable State、解碼 DLQ、操作 CHASM 元件——
  在「server 說 workflow 卡住了但歷史看不出來」時用它。DLQ 的處理流程見 [`docs/admin/dlq.md`](./admin/dlq.md)。
- **`temporal-server-debug`**（`Makefile:390`）：帶 `TEMPORAL_DEBUG` tag，functional test 的 timeout 會被放大。
- **測試 log 環境變數**（`docs/development/testing.md` 的 Environment variables 一節）：
  `TEMPORAL_TEST_LOG_LEVEL`、`TEMPORAL_TEST_LOG_FILE`、`TEMPORAL_TEST_TIMEOUT`、
  `TEMPORAL_TEST_SHARED_CLUSTERS` / `TEMPORAL_TEST_DEDICATED_CLUSTERS`（測試叢集池大小）。
- **OTEL tracing**：`make OTEL=true ...` 會一次設好五個環境變數（`Makefile:85-92`），
  搭配 `make start-dependencies` 起的 Grafana Tempo 看 trace。細節見 [`docs/development/tracing.md`](./development/tracing.md)。
- **`temporaltest` package**：在**你自己的 Go 測試**裡用 `temporaltest.NewServer(...)` 起一台行程內的
  精簡 server（`temporaltest/server.go`），適合測 SDK 層行為而不是 server 內部。
- **故障注入**：persistence 層有 `faultinjection/` 實作；gRPC／HTTP 層有 `env.InjectRequestFault`
  等 API（見 `docs/development/testing.md`），可精準模擬「執行了但回應遺失」。

---

## 8. 一次完整的修改循環

```bash
# 1. 改程式碼
# 2. 若動到 proto 或 //go:generate 標註
make proto          # 或 make go-generate
# 3. 最快的回饋
go test -tags disable_grpc_modules,test_dep ./service/history/api/startworkflow/...
# 4. 整理 import 與格式
make fmt-imports
# 5. 只 lint 改動的 package（會自動修，記得看 git diff）
make lint-code-fast
# 6. 全量單元測試
make unit-test
# 7. 需要時才跑（要先 make start-dependencies）
make functional-test
```

---

## 9. 自驗指令

```bash
# 確認四層測試各自的目錄定義
grep -n 'FUNCTIONAL_TEST_ROOT\|INTEGRATION_TEST_DIRS\|UNIT_TEST_DIRS :=\|MIXED_BRAIN_TEST_ROOT' Makefile

# 列出所有 make 目標
grep -nE '^[a-zA-Z0-9_%-]+:' Makefile | wc -l

# 確認測試預設帶 -race 與 -shuffle
sed -n '64,76p' Makefile

# 看 CI 的 8 種 DB 組合與哪兩組是 required
sed -n '105,140p' .github/workflows/run-tests.yml

# 確認 shard salt 的來源與目前值
sed -n '42,46p' tests/testcore/test_env.go && cat tests/testcore/shard_salt.txt

# 確認被禁止的 Go 寫法
sed -n '33,45p' .github/.golangci.yml

# 確認 docker compose 會起哪些服務（最後的 temporal-dev-network 屬於 networks: 區段，不是服務）
grep -nE '^  [a-z0-9-]+:' develop/docker-compose/docker-compose.yml
```

---

## 延伸閱讀

- [專案總覽](./project-overview.zh-TW.md) — 這個 repo 在做什麼。
- [請求流程導覽](./request-flow.zh-TW.md) — 改動會落在哪一段程式碼路徑上。
- [資料模型與持久層導覽](./data-model.zh-TW.md) — 為什麼動到 `common/persistence/` 會觸發全量 CI。
- [執行期拓樸導覽](./runtime-topology.zh-TW.md) — 本機設定檔與正式部署設定的關係。
- [能力地圖](./feature-map.zh-TW.md) — 新增 RPC 時它會落在哪個 service 面上。
- [`CONTRIBUTING.md`](../CONTRIBUTING.md)、[`docs/development/testing.md`](./development/testing.md) — 英文權威版。
