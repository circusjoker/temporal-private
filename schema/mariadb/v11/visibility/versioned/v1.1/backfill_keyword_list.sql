-- Backfill for executions that already existed when this migration ran.
--
-- Without this, every KeywordList query against an upgraded database returns
-- nothing for every execution written before the upgrade, because the shipped
-- query ANDs the JSON predicate with a side-table lookup. Closed executions are
-- never rewritten, so those results would not come back for the rest of
-- retention.
--
-- Each attribute is read from the table whose generated column a query reads:
-- the three visibility tables are written from the same JSON but guarded
-- independently, so they can disagree.
--
-- Values longer than the column are skipped, matching what the write path and
-- the query converter do -- nothing asks the index about a value it cannot hold.
--
-- NOTE for large installations: this is a single statement over the whole
-- visibility table. On a big database run it during a maintenance window, or
-- split it by namespace_id, rather than letting the schema tool do it inline.
INSERT INTO keyword_list_search_attributes (namespace_id, run_id, attr, value)
SELECT DISTINCT namespace_id, run_id, attr, value FROM (
  SELECT ev.namespace_id AS namespace_id, ev.run_id AS run_id,
         'TemporalChangeVersion' AS attr, jt.value AS value
    FROM executions_visibility ev,
         JSON_TABLE(ev.search_attributes, '$.TemporalChangeVersion[*]'
                    COLUMNS (value VARCHAR(4096) PATH '$')) jt
   WHERE jt.value IS NOT NULL AND CHAR_LENGTH(jt.value) <= 255
  UNION ALL
  SELECT ev.namespace_id AS namespace_id, ev.run_id AS run_id,
         'BinaryChecksums' AS attr, jt.value AS value
    FROM executions_visibility ev,
         JSON_TABLE(ev.search_attributes, '$.BinaryChecksums[*]'
                    COLUMNS (value VARCHAR(4096) PATH '$')) jt
   WHERE jt.value IS NOT NULL AND CHAR_LENGTH(jt.value) <= 255
  UNION ALL
  SELECT ev.namespace_id AS namespace_id, ev.run_id AS run_id,
         'BuildIds' AS attr, jt.value AS value
    FROM executions_visibility ev,
         JSON_TABLE(ev.search_attributes, '$.BuildIds[*]'
                    COLUMNS (value VARCHAR(4096) PATH '$')) jt
   WHERE jt.value IS NOT NULL AND CHAR_LENGTH(jt.value) <= 255
  UNION ALL
  SELECT ev.namespace_id AS namespace_id, ev.run_id AS run_id,
         'TemporalPauseInfo' AS attr, jt.value AS value
    FROM executions_visibility ev,
         JSON_TABLE(ev.search_attributes, '$.TemporalPauseInfo[*]'
                    COLUMNS (value VARCHAR(4096) PATH '$')) jt
   WHERE jt.value IS NOT NULL AND CHAR_LENGTH(jt.value) <= 255
  UNION ALL
  SELECT ev.namespace_id AS namespace_id, ev.run_id AS run_id,
         'TemporalReportedProblems' AS attr, jt.value AS value
    FROM executions_visibility ev,
         JSON_TABLE(ev.search_attributes, '$.TemporalReportedProblems[*]'
                    COLUMNS (value VARCHAR(4096) PATH '$')) jt
   WHERE jt.value IS NOT NULL AND CHAR_LENGTH(jt.value) <= 255
  UNION ALL
  SELECT ev.namespace_id AS namespace_id, ev.run_id AS run_id,
         'TemporalUsedWorkerDeploymentVersions' AS attr, jt.value AS value
    FROM executions_visibility ev,
         JSON_TABLE(ev.search_attributes, '$.TemporalUsedWorkerDeploymentVersions[*]'
                    COLUMNS (value VARCHAR(4096) PATH '$')) jt
   WHERE jt.value IS NOT NULL AND CHAR_LENGTH(jt.value) <= 255
  UNION ALL
  SELECT csa.namespace_id AS namespace_id, csa.run_id AS run_id,
         'KeywordList01' AS attr, jt.value AS value
    FROM custom_search_attributes csa,
         JSON_TABLE(csa.search_attributes, '$.KeywordList01[*]'
                    COLUMNS (value VARCHAR(4096) PATH '$')) jt
   WHERE jt.value IS NOT NULL AND CHAR_LENGTH(jt.value) <= 255
  UNION ALL
  SELECT csa.namespace_id AS namespace_id, csa.run_id AS run_id,
         'KeywordList02' AS attr, jt.value AS value
    FROM custom_search_attributes csa,
         JSON_TABLE(csa.search_attributes, '$.KeywordList02[*]'
                    COLUMNS (value VARCHAR(4096) PATH '$')) jt
   WHERE jt.value IS NOT NULL AND CHAR_LENGTH(jt.value) <= 255
  UNION ALL
  SELECT csa.namespace_id AS namespace_id, csa.run_id AS run_id,
         'KeywordList03' AS attr, jt.value AS value
    FROM custom_search_attributes csa,
         JSON_TABLE(csa.search_attributes, '$.KeywordList03[*]'
                    COLUMNS (value VARCHAR(4096) PATH '$')) jt
   WHERE jt.value IS NOT NULL AND CHAR_LENGTH(jt.value) <= 255
  UNION ALL
  SELECT chasm.namespace_id AS namespace_id, chasm.run_id AS run_id,
         'TemporalKeywordList01' AS attr, jt.value AS value
    FROM chasm_search_attributes chasm,
         JSON_TABLE(chasm.search_attributes, '$.TemporalKeywordList01[*]'
                    COLUMNS (value VARCHAR(4096) PATH '$')) jt
   WHERE jt.value IS NOT NULL AND CHAR_LENGTH(jt.value) <= 255
  UNION ALL
  SELECT chasm.namespace_id AS namespace_id, chasm.run_id AS run_id,
         'TemporalKeywordList02' AS attr, jt.value AS value
    FROM chasm_search_attributes chasm,
         JSON_TABLE(chasm.search_attributes, '$.TemporalKeywordList02[*]'
                    COLUMNS (value VARCHAR(4096) PATH '$')) jt
   WHERE jt.value IS NOT NULL AND CHAR_LENGTH(jt.value) <= 255
) src;
