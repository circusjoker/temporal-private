package tests

// temporal-sql-tool CLI coverage for the mariadb plugin. Mirrors mysql_cli_test.go,
// against MARIADB_SEEDS / MARIADB_PORT rather than the MySQL ones.

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/suite"
	"go.temporal.io/server/common/persistence/sql/sqlplugin/mariadb"
	mariadbversionV11 "go.temporal.io/server/schema/mariadb/v11"
	"go.temporal.io/server/temporal/environment"
	"go.temporal.io/server/tools/sql/clitest"
)

func TestMariaDBConnTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, clitest.NewSQLConnTestSuite(
		environment.GetMariaDBAddress(),
		strconv.Itoa(environment.GetMariaDBPort()),
		mariadb.PluginName,
		testMySQLQuery,
	))
}

func TestMariaDBHandlerTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, clitest.NewHandlerTestSuite(
		environment.GetMariaDBAddress(),
		strconv.Itoa(environment.GetMariaDBPort()),
		mariadb.PluginName,
	))
}

func TestMariaDBSetupSchemaTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, clitest.NewSetupSchemaTestSuite(
		environment.GetMariaDBAddress(),
		strconv.Itoa(environment.GetMariaDBPort()),
		mariadb.PluginName,
		testMySQLQuery,
	))
}

func TestMariaDBUpdateSchemaTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, clitest.NewUpdateSchemaTestSuite(
		environment.GetMariaDBAddress(),
		strconv.Itoa(environment.GetMariaDBPort()),
		mariadb.PluginName,
		testMySQLQuery,
		testMariaDBExecutionSchemaVersionDir,
		mariadbversionV11.Version,
		testMariaDBVisibilitySchemaVersionDir,
		mariadbversionV11.VisibilityVersion,
	))
}

func TestMariaDBVersionTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, clitest.NewVersionTestSuite(
		environment.GetMariaDBAddress(),
		strconv.Itoa(environment.GetMariaDBPort()),
		mariadb.PluginName,
		testMariaDBExecutionSchemaFile,
		testMariaDBVisibilitySchemaFile,
	))
}
