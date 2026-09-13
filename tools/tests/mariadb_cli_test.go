package tests

// temporal-sql-tool CLI coverage for the mariadb plugin. Mirrors mysql_cli_test.go;
// MariaDB shares the MySQL host/port environment variables.

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/suite"
	"go.temporal.io/server/common/persistence/sql/sqlplugin/mysql"
	mariadbversionV11 "go.temporal.io/server/schema/mariadb/v11"
	"go.temporal.io/server/temporal/environment"
	"go.temporal.io/server/tools/sql/clitest"
)

func TestMariaDBConnTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, clitest.NewSQLConnTestSuite(
		environment.GetMySQLAddress(),
		strconv.Itoa(environment.GetMySQLPort()),
		mysql.PluginNameMariaDB,
		testMySQLQuery,
	))
}

func TestMariaDBHandlerTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, clitest.NewHandlerTestSuite(
		environment.GetMySQLAddress(),
		strconv.Itoa(environment.GetMySQLPort()),
		mysql.PluginNameMariaDB,
	))
}

func TestMariaDBSetupSchemaTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, clitest.NewSetupSchemaTestSuite(
		environment.GetMySQLAddress(),
		strconv.Itoa(environment.GetMySQLPort()),
		mysql.PluginNameMariaDB,
		testMySQLQuery,
	))
}

func TestMariaDBUpdateSchemaTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, clitest.NewUpdateSchemaTestSuite(
		environment.GetMySQLAddress(),
		strconv.Itoa(environment.GetMySQLPort()),
		mysql.PluginNameMariaDB,
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
		environment.GetMySQLAddress(),
		strconv.Itoa(environment.GetMySQLPort()),
		mysql.PluginNameMariaDB,
		testMariaDBExecutionSchemaFile,
		testMariaDBVisibilitySchemaFile,
	))
}
