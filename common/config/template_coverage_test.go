package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEmbeddedTemplateSQLPlugins renders the embedded config template for every
// SQL value of the DB environment variable and checks that both the default and
// the visibility store end up on that plugin. mariadb10 shares the mysql8 branch
// (same driver, same MYSQL_* variables), so this is what keeps it wired up.
func TestEmbeddedTemplateSQLPlugins(t *testing.T) {
	for _, db := range []string{"mysql8", "mariadb10", "postgres12", "postgres12_pgx"} {
		t.Run(db, func(t *testing.T) {
			t.Setenv("DB", db)

			cfg, err := Load(WithEmbedded())
			require.NoError(t, err)

			require.Equal(t, "default", cfg.Persistence.DefaultStore)
			require.Equal(t, "visibility", cfg.Persistence.VisibilityStore)

			for _, store := range []string{"default", "visibility"} {
				ds, ok := cfg.Persistence.DataStores[store]
				require.True(t, ok, "missing %s datastore", store)
				require.NotNil(t, ds.SQL, "%s datastore is not SQL", store)
				require.Equal(t, db, ds.SQL.PluginName)
			}
		})
	}
}

// TestDockerTemplateSQLPlugins does the same for config/docker.yaml, which is a
// separate template shipped with the Docker image and validates DB explicitly.
func TestDockerTemplateSQLPlugins(t *testing.T) {
	const dockerTemplate = "../../config/docker.yaml"

	t.Run("mariadb10", func(t *testing.T) {
		t.Setenv("DB", "mariadb10")
		t.Setenv("MYSQL_SEEDS", "mariadb")
		t.Setenv("MYSQL_USER", "temporal")
		t.Setenv("MYSQL_PWD", "temporal")

		cfg, err := Load(WithConfigFile(dockerTemplate))
		require.NoError(t, err)

		for _, store := range []string{"default", "visibility"} {
			ds := cfg.Persistence.DataStores[store]
			require.NotNil(t, ds.SQL, "%s datastore is not SQL", store)
			require.Equal(t, "mariadb10", ds.SQL.PluginName)
			require.Equal(t, "mariadb:3306", ds.SQL.ConnectAddr)
		}
	})

	t.Run("unsupported db is rejected", func(t *testing.T) {
		t.Setenv("DB", "notadb")

		_, err := Load(WithConfigFile(dockerTemplate))
		require.ErrorContains(t, err, "Invalid DB value")
	})
}
