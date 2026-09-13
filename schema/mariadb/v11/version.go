package v11

// NOTE: whenever there is a new database schema update, plz update the following versions

// Version is the MariaDB database release version.
// The execution/persistence schema is the MySQL v8 schema except for the three
// cluster_membership TIMESTAMP(6) columns (see temporal/schema.sql for why), so the
// version lineage is kept in sync with it.
const Version = "1.19"

// VisibilityVersion is the MariaDB visibility database release version.
// The visibility schema had to be rewritten for MariaDB (no expression indexes,
// no multi-valued indexes, no `->`/`->>` operators), so it starts its own
// lineage at 1.0 rather than tracking MySQL's visibility versions.
const VisibilityVersion = "1.0"
