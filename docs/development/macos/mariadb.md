# Run MariaDB on macOS

Temporal's MariaDB support uses the `mariadb10` SQL plugin and the `schema/mariadb/v10`
schema. It is a single-store setup: both the `temporal` and `temporal_visibility`
databases live in MariaDB, no Elasticsearch required.

### Install
```bash
brew install mariadb
```

### Start
```bash
brew services start mariadb
```

### Stop
```bash
brew services stop mariadb
```

### Post Installation
Verify MariaDB is running and accessible:
```bash
mariadb -h 127.0.0.1 -P 3306 -u root
```

Within the `mariadb` shell, create the user and password used by the dev config and
by the tests:
```sql
ALTER USER 'root'@'localhost' IDENTIFIED BY 'root';
CREATE USER 'temporal'@'localhost' IDENTIFIED BY 'temporal';
GRANT ALL PRIVILEGES ON *.* TO 'temporal'@'localhost';
```

Verify the password:
```bash
mariadb -h 127.0.0.1 -P 3306 -u root -p
mariadb -h 127.0.0.1 -P 3306 -u temporal -p
```

### Port

The `docker compose` based dev environment publishes MariaDB on **3307** so that it can
run alongside MySQL, and that is the default the server config and the tests assume.
A Homebrew MariaDB listens on 3306, so either change `port` in
`config/development-mariadb10.yaml` and export `MARIADB_PORT=3306` for the tests, or
start MariaDB on 3307:

```bash
mariadbd --port=3307
```

### Install schema and run the server
```bash
make install-schema-mariadb
make start-mariadb
```
