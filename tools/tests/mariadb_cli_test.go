package tests

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/suite"
	"go.temporal.io/server/common/persistence/sql/sqlplugin/mariadb"
	mariadbversionV10 "go.temporal.io/server/schema/mariadb/v10"
	"go.temporal.io/server/temporal/environment"
	"go.temporal.io/server/tools/sql/clitest"
)

func TestMariaDBConnTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, clitest.NewSQLConnTestSuite(
		environment.GetMariaDBAddress(),
		strconv.Itoa(environment.GetMariaDBPort()),
		mariadb.PluginName,
		testMariaDBQuery,
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
		testMariaDBQuery,
	))
}

func TestMariaDBUpdateSchemaTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, clitest.NewUpdateSchemaTestSuite(
		environment.GetMariaDBAddress(),
		strconv.Itoa(environment.GetMariaDBPort()),
		mariadb.PluginName,
		testMariaDBQuery,
		testMariaDBExecutionSchemaVersionDir,
		mariadbversionV10.Version,
		testMariaDBVisibilitySchemaVersionDir,
		mariadbversionV10.VisibilityVersion,
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
