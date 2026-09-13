package tests

import (
	"math"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/suite"
	"go.temporal.io/server/chasm"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/metrics"
	persistencetests "go.temporal.io/server/common/persistence/persistence-tests"
	"go.temporal.io/server/common/persistence/serialization"
	"go.temporal.io/server/common/persistence/sql"
	"go.temporal.io/server/common/persistence/sql/sqlplugin"
	_ "go.temporal.io/server/common/persistence/sql/sqlplugin/mariadb"
	sqltests "go.temporal.io/server/common/persistence/sql/sqlplugin/tests"
	"go.temporal.io/server/common/resolver"
)

func TestMariaDBShardStoreSuite(t *testing.T) {
	t.Parallel()
	testData, tearDown := setUpMariaDBTest(t)
	defer tearDown()

	shardStore, err := testData.Factory.NewShardStore()
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}

	s := NewShardSuite(
		t,
		shardStore,
		serialization.NewSerializer(),
		testData.Logger,
	)
	suite.Run(t, s)
}

func TestMariaDBExecutionMutableStateStoreSuite(t *testing.T) {
	t.Parallel()
	testData, tearDown := setUpMariaDBTest(t)
	defer tearDown()

	shardStore, err := testData.Factory.NewShardStore()
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	executionStore, err := testData.Factory.NewExecutionStore()
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	db, err := sql.NewSQLDB(sqlplugin.DbKindMain, testData.Cfg, resolver.NewNoopResolver(), testData.Logger, metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() { _ = db.Close() }()

	s := NewExecutionMutableStateSuite(
		t,
		shardStore,
		executionStore,
		serialization.NewSerializer(),
		testData.Logger,
	)
	s.MutableStateTableCounts = sqlMutableStateTableCounts(db)
	suite.Run(t, s)
}

func TestMariaDBExecutionMutableStateTaskStoreSuite(t *testing.T) {
	t.Parallel()
	testData, tearDown := setUpMariaDBTest(t)
	defer tearDown()

	shardStore, err := testData.Factory.NewShardStore()
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	executionStore, err := testData.Factory.NewExecutionStore()
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}

	s := NewExecutionMutableStateTaskSuite(
		t,
		shardStore,
		executionStore,
		serialization.NewSerializer(),
		testData.Logger,
	)
	suite.Run(t, s)
}

func TestMariaDBHistoryStoreSuite(t *testing.T) {
	t.Parallel()
	testData, tearDown := setUpMariaDBTest(t)
	defer tearDown()

	store, err := testData.Factory.NewExecutionStore()
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}

	s := NewHistoryEventsSuite(t, store, testData.Logger)
	suite.Run(t, s)
}

func TestMariaDBTaskQueueSuite(t *testing.T) {
	t.Parallel()
	testData, tearDown := setUpMariaDBTest(t)
	defer tearDown()

	taskQueueStore, err := testData.Factory.NewTaskStore()
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		testData.Factory.Close()
		TearDownMariaDBDatabase(t, testData.Cfg)
	}()

	s := NewTaskQueueSuite(t, taskQueueStore, testData.Logger)
	suite.Run(t, s)
}

func TestMariaDBFairTaskQueueSuite(t *testing.T) {
	t.Parallel()
	testData, tearDown := setUpMariaDBTest(t)
	defer tearDown()

	taskQueueStore, err := testData.Factory.NewFairTaskStore()
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		testData.Factory.Close()
		TearDownMariaDBDatabase(t, testData.Cfg)
	}()

	s := NewTaskQueueSuite(t, taskQueueStore, testData.Logger) // same suite, different store
	suite.Run(t, s)
}

func TestMariaDBTaskQueueTaskSuite(t *testing.T) {
	t.Parallel()
	testData, tearDown := setUpMariaDBTest(t)
	defer tearDown()

	taskQueueStore, err := testData.Factory.NewTaskStore()
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}

	s := NewTaskQueueTaskSuite(t, taskQueueStore, testData.Logger)
	suite.Run(t, s)
}

func TestMariaDBTaskQueueFairTaskSuite(t *testing.T) {
	t.Parallel()
	testData, tearDown := setUpMariaDBTest(t)
	defer tearDown()

	taskQueueStore, err := testData.Factory.NewFairTaskStore()
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}

	s := NewTaskQueueFairTaskSuite(t, taskQueueStore, testData.Logger)
	suite.Run(t, s)
}

func TestMariaDBTaskQueueUserDataSuite(t *testing.T) {
	t.Parallel()
	testData, tearDown := setUpMariaDBTest(t)
	defer tearDown()

	taskQueueStore, err := testData.Factory.NewTaskStore()
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}

	s := NewTaskQueueUserDataSuite(t, taskQueueStore, testData.Logger)
	suite.Run(t, s)
}

func TestMariaDBVisibilityPersistenceSuite(t *testing.T) {
	t.Parallel()
	s := &VisibilityPersistenceSuite{
		TestBase: persistencetests.NewTestBaseWithSQL(persistencetests.GetMariaDBTestClusterOption()),
	}
	suite.Run(t, s)
}

// TODO: Merge persistence-tests into the tests directory.

func TestMariaDBHistoryV2PersistenceSuite(t *testing.T) {
	t.Parallel()
	s := new(persistencetests.HistoryV2PersistenceSuite)
	s.TestBase = persistencetests.NewTestBaseWithSQL(persistencetests.GetMariaDBTestClusterOption())
	s.Setup(nil)
	suite.Run(t, s)
}

func TestMariaDBMetadataPersistenceSuiteV2(t *testing.T) {
	t.Parallel()
	s := new(persistencetests.MetadataPersistenceSuiteV2)
	s.TestBase = persistencetests.NewTestBaseWithSQL(persistencetests.GetMariaDBTestClusterOption())
	s.Setup(nil)
	suite.Run(t, s)
}

func TestMariaDBQueuePersistence(t *testing.T) {
	t.Parallel()
	s := new(persistencetests.QueuePersistenceSuite)
	s.TestBase = persistencetests.NewTestBaseWithSQL(persistencetests.GetMariaDBTestClusterOption())
	s.Setup(nil)
	suite.Run(t, s)
}

func TestMariaDBClusterMetadataPersistence(t *testing.T) {
	t.Parallel()
	s := new(persistencetests.ClusterMetadataManagerSuite)
	s.TestBase = persistencetests.NewTestBaseWithSQL(persistencetests.GetMariaDBTestClusterOption())
	s.Setup(nil)
	suite.Run(t, s)
}

// SQL Store tests

func TestMariaDBNamespaceSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewNamespaceSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBQueueMessageSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewQueueMessageSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBQueueMetadataSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewQueueMetadataSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBMatchingTaskSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewMatchingTaskSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBMatchingTaskV2Suite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewMatchingTaskV2Suite(t, store)
	suite.Run(t, s)
}

func TestMariaDBMatchingTaskQueueSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewMatchingTaskQueueSuite(t, store, sqlplugin.MatchingTaskVersion1)
	suite.Run(t, s)
}

func TestMariaDBMatchingFairTaskQueueSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewMatchingTaskQueueSuite(t, store, sqlplugin.MatchingTaskVersion2)
	suite.Run(t, s)
}

func TestMariaDBHistoryShardSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryShardSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryNodeSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryNodeSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryTreeSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryTreeSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryCurrentExecutionSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryCurrentExecutionSuite(t, store, chasm.WorkflowArchetypeID)
	suite.Run(t, s)
}

func TestMariaDBHistoryCurrentChasmExecutionSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryCurrentExecutionSuite(t, store, math.MaxUint32)
	suite.Run(t, s)
}

func TestMariaDBHistoryExecutionSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryExecutionSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryTransferTaskSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryTransferTaskSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryTimerTaskSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryTimerTaskSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryReplicationTaskSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryReplicationTaskSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryVisibilityTaskSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryVisibilityTaskSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryReplicationDLQTaskSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryReplicationDLQTaskSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryExecutionBufferSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryExecutionBufferSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryExecutionActivitySuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryExecutionActivitySuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryExecutionChildWorkflowSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryExecutionChildWorkflowSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryExecutionTimerSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryExecutionTimerSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryExecutionChasmSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryExecutionChasmSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryExecutionRequestCancelSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryExecutionRequestCancelSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryExecutionSignalSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryExecutionSignalSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBHistoryExecutionSignalRequestSuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewHistoryExecutionSignalRequestSuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBVisibilitySuite(t *testing.T) {
	t.Parallel()
	cfg := NewMariaDBConfig()
	SetupMariaDBDatabase(t, cfg)
	SetupMariaDBSchema(t, cfg)
	store, err := sql.NewSQLDB(sqlplugin.DbKindVisibility, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB DB: %v", err)
	}
	defer func() {
		_ = store.Close()
		TearDownMariaDBDatabase(t, cfg)
	}()

	s := sqltests.NewVisibilitySuite(t, store)
	suite.Run(t, s)
}

func TestMariaDBClosedConnectionError(t *testing.T) {
	t.Parallel()
	testData, tearDown := setUpMariaDBTest(t)
	defer tearDown()

	s := newConnectionSuite(t, testData.Factory)
	suite.Run(t, s)
}

func TestMariaDBQueueV2(t *testing.T) {
	t.Parallel()
	testData, tearDown := setUpMariaDBTest(t)
	t.Cleanup(tearDown)
	RunQueueV2TestSuiteForSQL(t, testData.Factory)
}

func TestMariaDBNexusEndpointPersistence(t *testing.T) {
	t.Parallel()
	testData, tearDown := setUpMariaDBTest(t)
	defer tearDown()

	store, err := testData.Factory.NewNexusEndpointStore()
	if err != nil {
		t.Fatalf("unable to create MariaDB NexusEndpointStore: %v", err)
	}

	tableVersion := atomic.Int64{}
	t.Run("Generic", func(t *testing.T) {
		RunNexusEndpointTestSuite(t, store, &tableVersion)
	})
}
