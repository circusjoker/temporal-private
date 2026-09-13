package mariadb

import (
	"fmt"

	"go.temporal.io/server/common/persistence/sql/sqlplugin"
)

// storeDB is the set of interfaces callers assert the object returned by
// CreateDB to. sqlplugin/mysql's db satisfies both, and wrapping it keeps every
// method it already implements while letting MariaDB override the few that must
// also maintain its keyword-list index.
type storeDB interface {
	sqlplugin.DB
	sqlplugin.AdminDB
	// Conn is included so the wrapper is not narrower than what
	// sqlplugin/mysql returns. Nothing in-tree asserts Conn on a DB today, but
	// silently dropping methods MySQL's db has would be a divergence waiting to
	// surprise someone.
	sqlplugin.Conn
}

// db decorates the shared MySQL-protocol db. Only the visibility write methods
// are overridden; the other ~180 are forwarded by the embedded interface.
type db struct {
	storeDB
}

var _ sqlplugin.DB = (*db)(nil)
var _ sqlplugin.AdminDB = (*db)(nil)

// wrapDB decorates the db mysql handed back.
func wrapDB(generic sqlplugin.GenericDB) (sqlplugin.GenericDB, error) {
	sdb, ok := generic.(storeDB)
	if !ok {
		// Never silently fall through: an unwrapped db would write visibility
		// rows without their keyword-list index, and queries would quietly miss
		// executions.
		return nil, fmt.Errorf(
			"mariadb: underlying db %T does not implement sqlplugin.DB, AdminDB and Conn", generic)
	}
	return &db{storeDB: sdb}, nil
}
