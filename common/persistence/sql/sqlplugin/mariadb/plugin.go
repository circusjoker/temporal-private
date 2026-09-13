// Package mariadb registers Temporal's MariaDB SQL plugin.
//
// MariaDB speaks the MySQL wire protocol and shares every CRUD statement with
// sqlplugin/mysql, so this package supplies only what differs and reuses that
// package for the rest through the seams it exports (mysql.Flavor,
// mysql.NewPlugin, mysql.VisibilityDialect, mysql.NewQueryConverter).
//
// What differs, all verified against mariadb:11.4:
//
//   - the visibility schema (schema/mariadb/v11/visibility): MariaDB has no
//     expression indexes, no multi-valued (ARRAY) indexes and no `->` / `->>`
//     JSON operators;
//   - three cluster_membership columns in the execution schema, which are
//     TIMESTAMP(6) because MariaDB truncates sub-second values where MySQL
//     rounds them (see schema/mariadb/v11/temporal/schema.sql);
//   - the database collation, pinned to NO PAD below; and
//   - the KeywordList and close-time SQL in query_converter.go.
//
// Minimum supported version is MariaDB 10.10 (for utf8mb4_uca1400_nopad_ai_ci);
// only 11.4 has actually been exercised.
package mariadb

import (
	"go.temporal.io/server/common/persistence/sql"
	"go.temporal.io/server/common/persistence/sql/sqlplugin/mysql"
	mariadbschemaV11 "go.temporal.io/server/schema/mariadb/v11"
)

// PluginName is the name of the plugin.
const PluginName = "mariadb"

// createDatabaseQuery pins the collation.
//
// Left to its own default MariaDB picks utf8mb4_uca1400_ai_ci, which is PAD
// SPACE, while MySQL 8's utf8mb4_0900_ai_ci is NO PAD. That is not cosmetic:
// under PAD SPACE, 'wf-id' and 'wf-id ' are the same value, so two workflow ids,
// namespace names or task queue names differing only by trailing whitespace
// collide on a unique key that MySQL would accept.
// utf8mb4_uca1400_nopad_ai_ci restores MySQL's semantics and keeps the same
// accent- and case-insensitivity.
const createDatabaseQuery = "CREATE DATABASE IF NOT EXISTS %v CHARACTER SET utf8mb4 COLLATE utf8mb4_uca1400_nopad_ai_ci"

// Flavor returns the MariaDB flavor of the shared MySQL-protocol plugin.
func Flavor() mysql.Flavor {
	return mysql.Flavor{
		PluginName:              PluginName,
		SchemaVersion:           mariadbschemaV11.Version,
		VisibilitySchemaVersion: mariadbschemaV11.VisibilityVersion,
		CreateDatabaseQuery:     createDatabaseQuery,
	}
}

func init() {
	sql.RegisterPlugin(PluginName, mysql.NewPlugin(Flavor(), mysql.NewQueryConverter(dialect{})))
}
