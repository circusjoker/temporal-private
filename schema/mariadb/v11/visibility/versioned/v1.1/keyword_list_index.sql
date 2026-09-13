-- MariaDB has no multi-valued indexes, so the 11 `CAST(col AS CHAR(255) ARRAY)`
-- indexes MySQL 8 puts on KeywordList search attributes cannot be created. Without
-- them every KeywordList predicate is a table scan of the namespace.
--
-- This table is the replacement: one row per (execution, attribute, value), kept in
-- the same transaction as the visibility row it describes, so it is an index rather
-- than a cache. See common/persistence/sql/sqlplugin/mariadb/keyword_list.go.
--
-- `attr` is the physical column name the value would have lived in on MySQL --
-- BuildIds, KeywordList01, TemporalKeywordList01 and so on -- so one table covers
-- executions_visibility, custom_search_attributes and chasm_search_attributes.
CREATE TABLE keyword_list_search_attributes (
  namespace_id  CHAR(64)     NOT NULL,
  run_id        CHAR(64)     NOT NULL,
  attr          VARCHAR(64)  NOT NULL,
  value         VARCHAR(255) NOT NULL,
  PRIMARY KEY (namespace_id, run_id, attr, value)
);

-- The lookup index. Leading namespace_id matches how every visibility query is
-- scoped; run_id is last so the index covers the semi-join back to
-- executions_visibility without touching the table.
CREATE INDEX by_attr_value ON keyword_list_search_attributes (namespace_id, attr, value, run_id);
