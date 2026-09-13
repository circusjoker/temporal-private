package tests

import (
	"net"
	"path/filepath"
	"strconv"
	"testing"

	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/metrics"
	"go.temporal.io/server/common/metrics/metricstest"
	p "go.temporal.io/server/common/persistence"
	"go.temporal.io/server/common/persistence/serialization"
	"go.temporal.io/server/common/persistence/sql"
	"go.temporal.io/server/common/persistence/sql/sqlplugin"
	"go.temporal.io/server/common/persistence/sql/sqlplugin/mariadb"
	"go.temporal.io/server/common/resolver"
	"go.temporal.io/server/common/shuffle"
	"go.temporal.io/server/temporal/environment"
	"go.uber.org/zap/zaptest"
)

// TODO merge the initialization with existing persistence setup
const (
	testMariaDBClusterName = "temporal_mariadb_cluster"

	testMariaDBUser               = "temporal"
	testMariaDBPassword           = "temporal"
	testMariaDBConnectionProtocol = "tcp"
	testMariaDBDatabaseNamePrefix = "test_"
	testMariaDBDatabaseNameSuffix = "temporal_persistence"

	// TODO hard code this dir for now
	//  need to merge persistence test config / initialization in one place
	testMariaDBExecutionSchema  = "../../../schema/mariadb/v10/temporal/schema.sql"
	testMariaDBVisibilitySchema = "../../../schema/mariadb/v10/visibility/schema.sql"
)

type (
	MariaDBTestData struct {
		Cfg     *config.SQL
		Factory *sql.Factory
		Logger  log.Logger
		Metrics *metricstest.Capture
	}
)

func setUpMariaDBTest(t *testing.T) (MariaDBTestData, func()) {
	var testData MariaDBTestData
	testData.Cfg = NewMariaDBConfig()
	testData.Logger = log.NewZapLogger(zaptest.NewLogger(t))
	mh := metricstest.NewCaptureHandler()
	testData.Metrics = mh.StartCapture()
	SetupMariaDBDatabase(t, testData.Cfg)
	SetupMariaDBSchema(t, testData.Cfg)

	testData.Factory = sql.NewFactory(
		*testData.Cfg,
		resolver.NewNoopResolver(),
		testMariaDBClusterName,
		testData.Logger,
		mh,
		serialization.NewSerializer(),
	)

	tearDown := func() {
		testData.Factory.Close()
		mh.StopCapture(testData.Metrics)
		TearDownMariaDBDatabase(t, testData.Cfg)
	}

	return testData, tearDown
}

// NewMariaDBConfig returns a new MariaDB config for test
func NewMariaDBConfig() *config.SQL {
	return &config.SQL{
		User:     testMariaDBUser,
		Password: testMariaDBPassword,
		ConnectAddr: net.JoinHostPort(
			environment.GetMariaDBAddress(),
			strconv.Itoa(environment.GetMariaDBPort()),
		),
		ConnectProtocol: testMariaDBConnectionProtocol,
		PluginName:      mariadb.PluginName,
		DatabaseName:    testMariaDBDatabaseNamePrefix + shuffle.String(testMariaDBDatabaseNameSuffix),
	}
}

func SetupMariaDBDatabase(t *testing.T, cfg *config.SQL) {
	adminCfg := *cfg
	// NOTE need to connect with empty name to create new database
	adminCfg.DatabaseName = ""

	db, err := sql.NewSQLAdminDB(sqlplugin.DbKindUnknown, &adminCfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB admin DB: %v", err)
	}
	defer func() { _ = db.Close() }()

	err = db.CreateDatabase(cfg.DatabaseName)
	if err != nil {
		t.Fatalf("unable to create MariaDB database: %v", err)
	}
}

func SetupMariaDBSchema(t *testing.T, cfg *config.SQL) {
	db, err := sql.NewSQLAdminDB(sqlplugin.DbKindUnknown, cfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB admin DB: %v", err)
	}
	defer func() { _ = db.Close() }()

	schemaPath, err := filepath.Abs(testMariaDBExecutionSchema)
	if err != nil {
		t.Fatal(err)
	}

	statements, err := p.LoadAndSplitQuery([]string{schemaPath})
	if err != nil {
		t.Fatal(err)
	}

	for _, stmt := range statements {
		if err = db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	schemaPath, err = filepath.Abs(testMariaDBVisibilitySchema)
	if err != nil {
		t.Fatal(err)
	}

	statements, err = p.LoadAndSplitQuery([]string{schemaPath})
	if err != nil {
		t.Fatal(err)
	}

	for _, stmt := range statements {
		if err = db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

func TearDownMariaDBDatabase(t *testing.T, cfg *config.SQL) {
	adminCfg := *cfg
	// NOTE need to connect with empty name to create new database
	adminCfg.DatabaseName = ""

	db, err := sql.NewSQLAdminDB(sqlplugin.DbKindUnknown, &adminCfg, resolver.NewNoopResolver(), log.NewTestLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatalf("unable to create MariaDB admin DB: %v", err)
	}
	defer func() { _ = db.Close() }()

	err = db.DropDatabase(cfg.DatabaseName)
	if err != nil {
		t.Fatalf("unable to drop MariaDB database: %v", err)
	}
}
