package tests

import (
	"net"
	"strconv"
	"testing"

	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/metrics/metricstest"
	"go.temporal.io/server/common/persistence/serialization"
	"go.temporal.io/server/common/persistence/sql"
	"go.temporal.io/server/common/persistence/sql/sqlplugin/mysql"
	"go.temporal.io/server/common/resolver"
	"go.temporal.io/server/common/shuffle"
	"go.temporal.io/server/temporal/environment"
	"go.uber.org/zap/zaptest"
)

// MariaDB shares MySQL's test credentials but has its own MARIADB_SEEDS /
// MARIADB_PORT env vars (default 3307), so MYSQL_PORT cannot silently point this
// suite at a MySQL server -- which would pass and misreport what was tested.
const (
	testMariaDBClusterName = "temporal_mariadb_cluster"

	testMariaDBExecutionSchema  = "../../../schema/mariadb/v11/temporal/schema.sql"
	testMariaDBVisibilitySchema = "../../../schema/mariadb/v11/visibility/schema.sql"
)

func setUpMariaDBTest(t *testing.T) (MySQLTestData, func()) {
	var testData MySQLTestData
	testData.Cfg = NewMariaDBConfig()
	testData.Logger = log.NewZapLogger(zaptest.NewLogger(t))
	mh := metricstest.NewCaptureHandler()
	testData.Metrics = mh.StartCapture()
	SetupMySQLDatabase(t, testData.Cfg)
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
		TearDownMySQLDatabase(t, testData.Cfg)
	}

	return testData, tearDown
}

// NewMariaDBConfig returns a new MariaDB config for test
func NewMariaDBConfig() *config.SQL {
	return &config.SQL{
		User:     testMySQLUser,
		Password: testMySQLPassword,
		ConnectAddr: net.JoinHostPort(
			environment.GetMariaDBAddress(),
			strconv.Itoa(environment.GetMariaDBPort()),
		),
		ConnectProtocol: testMySQLConnectionProtocol,
		PluginName:      mysql.PluginNameMariaDB,
		DatabaseName:    testMySQLDatabaseNamePrefix + shuffle.String(testMySQLDatabaseNameSuffix),
	}
}

func SetupMariaDBSchema(t *testing.T, cfg *config.SQL) {
	setupSQLSchema(t, cfg, testMariaDBExecutionSchema, testMariaDBVisibilitySchema)
}
