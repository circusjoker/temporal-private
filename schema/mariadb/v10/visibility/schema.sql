CREATE TABLE executions_visibility (
  namespace_id            CHAR(64)      NOT NULL,
  run_id                  CHAR(64)      NOT NULL,
  _version                BIGINT        NOT NULL DEFAULT 0, -- increasing version, used to reject upserts which are out of order
  start_time              DATETIME(6)   NOT NULL,
  execution_time          DATETIME(6)   NOT NULL,
  workflow_id             VARCHAR(255)  NOT NULL,
  workflow_type_name      VARCHAR(255)  NOT NULL,
  status                  INT           NOT NULL,  -- enum WorkflowExecutionStatus {RUNNING, COMPLETED, FAILED, CANCELED, TERMINATED, CONTINUED_AS_NEW, TIMED_OUT}
  close_time              DATETIME(6)   NULL,
  history_length          BIGINT        NULL,
  history_size_bytes      BIGINT        NULL,
  execution_duration      BIGINT        NULL,
  state_transition_count  BIGINT        NULL,
  memo                    BLOB          NULL,
  encoding                VARCHAR(64)   NOT NULL,
  task_queue              VARCHAR(255)  NOT NULL DEFAULT '',
  search_attributes       JSON          NULL,
  parent_workflow_id      VARCHAR(255)  NULL,
  parent_run_id           VARCHAR(255)  NULL,
  root_workflow_id        VARCHAR(255)  NOT NULL DEFAULT '',
  root_run_id             VARCHAR(255)  NOT NULL DEFAULT '',

  -- MariaDB does not support indexes on expressions, so the
  -- COALESCE(close_time, <max datetime>) sort key shared by every visibility
  -- query is materialized into a persistent generated column instead.
  close_time_or_max       DATETIME(6)   GENERATED ALWAYS AS (COALESCE(close_time, '9999-12-31 23:59:59')) PERSISTENT,

  -- Each search attribute has its own generated column.
  -- MariaDB does not implement the `->` / `->>` JSON operators, so scalar
  -- attributes are extracted with JSON_VALUE (which already unquotes strings)
  -- and JSON array attributes with JSON_EXTRACT.
  -- JSON_VALUE renders JSON booleans as 1/0, hence the `IN ('true', '1')` form,
  -- which yields NULL when the attribute is absent.
  -- Text types must be PERSISTENT instead of VIRTUAL so a full-text index can
  -- be created on them.
  -- For datetime type, MariaDB can't cast a datetime string with timezone to
  -- datetime type directly, so we need to call CONVERT_TZ to convert to UTC.
  -- Check the `custom_search_attributes` table for complete set of examples.

  -- Predefined search attributes
  TemporalChangeVersion         JSON          GENERATED ALWAYS AS (JSON_EXTRACT(search_attributes, '$.TemporalChangeVersion')),
  BinaryChecksums               JSON          GENERATED ALWAYS AS (JSON_EXTRACT(search_attributes, '$.BinaryChecksums')),
  BatcherUser                   VARCHAR(255)  GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.BatcherUser')),
  TemporalScheduledStartTime    DATETIME(6)   GENERATED ALWAYS AS (
    CONVERT_TZ(
      REGEXP_REPLACE(JSON_VALUE(search_attributes, '$.TemporalScheduledStartTime'), 'Z|[+-][0-9]{2}:[0-9]{2}$', ''),
      SUBSTR(REPLACE(JSON_VALUE(search_attributes, '$.TemporalScheduledStartTime'), 'Z', '+00:00'), -6, 6),
      '+00:00'
    )
  ) PERSISTENT,
  TemporalScheduledById         VARCHAR(255)  GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.TemporalScheduledById')),
  TemporalSchedulePaused        BOOLEAN       GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.TemporalSchedulePaused') IN ('true', '1')),
  TemporalNamespaceDivision     VARCHAR(255)  GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.TemporalNamespaceDivision')),
  BuildIds                      JSON          GENERATED ALWAYS AS (JSON_EXTRACT(search_attributes, '$.BuildIds')),
  TemporalPauseInfo             JSON          GENERATED ALWAYS AS (JSON_EXTRACT(search_attributes, '$.TemporalPauseInfo')),
  TemporalReportedProblems      JSON          GENERATED ALWAYS AS (JSON_EXTRACT(search_attributes, '$.TemporalReportedProblems')),
  TemporalWorkerDeploymentVersion    VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.TemporalWorkerDeploymentVersion')),
  TemporalWorkflowVersioningBehavior VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.TemporalWorkflowVersioningBehavior')),
  TemporalWorkerDeployment           VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.TemporalWorkerDeployment')),
  TemporalUsedWorkerDeploymentVersions JSON GENERATED ALWAYS AS (JSON_EXTRACT(search_attributes, '$.TemporalUsedWorkerDeploymentVersions')),
  TemporalExternalPayloadSizeBytes BIGINT GENERATED ALWAYS AS (CAST(JSON_VALUE(search_attributes, '$.TemporalExternalPayloadSizeBytes') AS SIGNED)),
  TemporalExternalPayloadCount BIGINT GENERATED ALWAYS AS (CAST(JSON_VALUE(search_attributes, '$.TemporalExternalPayloadCount') AS SIGNED)),
  PRIMARY KEY (namespace_id, run_id)
);

CREATE INDEX default_idx                ON executions_visibility (namespace_id, close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_execution_time          ON executions_visibility (namespace_id, execution_time,         close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_workflow_id             ON executions_visibility (namespace_id, workflow_id,            close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_workflow_type           ON executions_visibility (namespace_id, workflow_type_name,     close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_status                  ON executions_visibility (namespace_id, status,                 close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_history_length          ON executions_visibility (namespace_id, history_length,         close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_history_size_bytes      ON executions_visibility (namespace_id, history_size_bytes,     close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_execution_duration      ON executions_visibility (namespace_id, execution_duration,     close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_state_transition_count  ON executions_visibility (namespace_id, state_transition_count, close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_task_queue              ON executions_visibility (namespace_id, task_queue,             close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_parent_workflow_id      ON executions_visibility (namespace_id, parent_workflow_id,     close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_parent_run_id           ON executions_visibility (namespace_id, parent_run_id,          close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_root_workflow_id        ON executions_visibility (namespace_id, root_workflow_id,       close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_root_run_id             ON executions_visibility (namespace_id, root_run_id,            close_time_or_max DESC, start_time DESC, run_id);

-- NOTE: MariaDB does not support multi-valued indexes
-- (CAST(<json array> AS CHAR(255) ARRAY)), so the JSON array search attributes
-- (KeywordList, BuildIds, BinaryChecksums, TemporalChangeVersion,
-- TemporalPauseInfo, TemporalReportedProblems,
-- TemporalUsedWorkerDeploymentVersions) are queried with JSON_CONTAINS /
-- JSON_OVERLAPS without an index.

-- Indexes for the predefined search attributes
CREATE INDEX by_temporal_worker_deployment_version    ON executions_visibility (namespace_id, TemporalWorkerDeploymentVersion,  close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_temporal_workflow_versioning_behavior ON executions_visibility (namespace_id, TemporalWorkflowVersioningBehavior,  close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_temporal_worker_deployment            ON executions_visibility (namespace_id, TemporalWorkerDeployment,  close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_batcher_user                  ON executions_visibility (namespace_id, BatcherUser,                close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_temporal_scheduled_start_time ON executions_visibility (namespace_id, TemporalScheduledStartTime, close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_temporal_scheduled_by_id      ON executions_visibility (namespace_id, TemporalScheduledById,      close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_temporal_schedule_paused      ON executions_visibility (namespace_id, TemporalSchedulePaused,     close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_temporal_namespace_division   ON executions_visibility (namespace_id, TemporalNamespaceDivision,  close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_temporal_external_payload_size_bytes ON executions_visibility (namespace_id, TemporalExternalPayloadSizeBytes, close_time_or_max DESC, start_time DESC, run_id);
CREATE INDEX by_temporal_external_payload_count ON executions_visibility (namespace_id, TemporalExternalPayloadCount, close_time_or_max DESC, start_time DESC, run_id); 

CREATE TABLE custom_search_attributes (
  namespace_id      CHAR(64)  NOT NULL,
  run_id            CHAR(64)  NOT NULL,
  _version           BIGINT    NOT NULL DEFAULT 0,
  search_attributes JSON      NULL,
  Bool01            BOOLEAN         GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Bool01') IN ('true', '1')),
  Bool02            BOOLEAN         GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Bool02') IN ('true', '1')),
  Bool03            BOOLEAN         GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Bool03') IN ('true', '1')),
  Datetime01        DATETIME(6)     GENERATED ALWAYS AS (
    CONVERT_TZ(
      REGEXP_REPLACE(JSON_VALUE(search_attributes, '$.Datetime01'), 'Z|[+-][0-9]{2}:[0-9]{2}$', ''),
      SUBSTR(REPLACE(JSON_VALUE(search_attributes, '$.Datetime01'), 'Z', '+00:00'), -6, 6),
      '+00:00'
    )
  ) PERSISTENT,
  Datetime02        DATETIME(6)     GENERATED ALWAYS AS (
    CONVERT_TZ(
      REGEXP_REPLACE(JSON_VALUE(search_attributes, '$.Datetime02'), 'Z|[+-][0-9]{2}:[0-9]{2}$', ''),
      SUBSTR(REPLACE(JSON_VALUE(search_attributes, '$.Datetime02'), 'Z', '+00:00'), -6, 6),
      '+00:00'
    )
  ) PERSISTENT,
  Datetime03        DATETIME(6)     GENERATED ALWAYS AS (
    CONVERT_TZ(
      REGEXP_REPLACE(JSON_VALUE(search_attributes, '$.Datetime03'), 'Z|[+-][0-9]{2}:[0-9]{2}$', ''),
      SUBSTR(REPLACE(JSON_VALUE(search_attributes, '$.Datetime03'), 'Z', '+00:00'), -6, 6),
      '+00:00'
    )
  ) PERSISTENT,
  Double01          DECIMAL(20, 5)  GENERATED ALWAYS AS (CAST(JSON_VALUE(search_attributes, '$.Double01') AS DECIMAL(20, 5))),
  Double02          DECIMAL(20, 5)  GENERATED ALWAYS AS (CAST(JSON_VALUE(search_attributes, '$.Double02') AS DECIMAL(20, 5))),
  Double03          DECIMAL(20, 5)  GENERATED ALWAYS AS (CAST(JSON_VALUE(search_attributes, '$.Double03') AS DECIMAL(20, 5))),
  Int01             BIGINT          GENERATED ALWAYS AS (CAST(JSON_VALUE(search_attributes, '$.Int01') AS SIGNED)),
  Int02             BIGINT          GENERATED ALWAYS AS (CAST(JSON_VALUE(search_attributes, '$.Int02') AS SIGNED)),
  Int03             BIGINT          GENERATED ALWAYS AS (CAST(JSON_VALUE(search_attributes, '$.Int03') AS SIGNED)),
  Keyword01         VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Keyword01')),
  Keyword02         VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Keyword02')),
  Keyword03         VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Keyword03')),
  Keyword04         VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Keyword04')),
  Keyword05         VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Keyword05')),
  Keyword06         VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Keyword06')),
  Keyword07         VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Keyword07')),
  Keyword08         VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Keyword08')),
  Keyword09         VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Keyword09')),
  Keyword10         VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Keyword10')),
  Text01            TEXT            GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Text01')) PERSISTENT,
  Text02            TEXT            GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Text02')) PERSISTENT,
  Text03            TEXT            GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.Text03')) PERSISTENT,
  KeywordList01     JSON            GENERATED ALWAYS AS (JSON_EXTRACT(search_attributes, '$.KeywordList01')),
  KeywordList02     JSON            GENERATED ALWAYS AS (JSON_EXTRACT(search_attributes, '$.KeywordList02')),
  KeywordList03     JSON            GENERATED ALWAYS AS (JSON_EXTRACT(search_attributes, '$.KeywordList03')),

  PRIMARY KEY (namespace_id, run_id)
);

CREATE INDEX by_bool_01           ON custom_search_attributes (namespace_id, Bool01);
CREATE INDEX by_bool_02           ON custom_search_attributes (namespace_id, Bool02);
CREATE INDEX by_bool_03           ON custom_search_attributes (namespace_id, Bool03);
CREATE INDEX by_datetime_01       ON custom_search_attributes (namespace_id, Datetime01);
CREATE INDEX by_datetime_02       ON custom_search_attributes (namespace_id, Datetime02);
CREATE INDEX by_datetime_03       ON custom_search_attributes (namespace_id, Datetime03);
CREATE INDEX by_double_01         ON custom_search_attributes (namespace_id, Double01);
CREATE INDEX by_double_02         ON custom_search_attributes (namespace_id, Double02);
CREATE INDEX by_double_03         ON custom_search_attributes (namespace_id, Double03);
CREATE INDEX by_int_01            ON custom_search_attributes (namespace_id, Int01);
CREATE INDEX by_int_02            ON custom_search_attributes (namespace_id, Int02);
CREATE INDEX by_int_03            ON custom_search_attributes (namespace_id, Int03);
CREATE INDEX by_keyword_01        ON custom_search_attributes (namespace_id, Keyword01);
CREATE INDEX by_keyword_02        ON custom_search_attributes (namespace_id, Keyword02);
CREATE INDEX by_keyword_03        ON custom_search_attributes (namespace_id, Keyword03);
CREATE INDEX by_keyword_04        ON custom_search_attributes (namespace_id, Keyword04);
CREATE INDEX by_keyword_05        ON custom_search_attributes (namespace_id, Keyword05);
CREATE INDEX by_keyword_06        ON custom_search_attributes (namespace_id, Keyword06);
CREATE INDEX by_keyword_07        ON custom_search_attributes (namespace_id, Keyword07);
CREATE INDEX by_keyword_08        ON custom_search_attributes (namespace_id, Keyword08);
CREATE INDEX by_keyword_09        ON custom_search_attributes (namespace_id, Keyword09);
CREATE INDEX by_keyword_10        ON custom_search_attributes (namespace_id, Keyword10);
CREATE FULLTEXT INDEX by_text_01  ON custom_search_attributes (Text01);
CREATE FULLTEXT INDEX by_text_02  ON custom_search_attributes (Text02);
CREATE FULLTEXT INDEX by_text_03  ON custom_search_attributes (Text03);

CREATE TABLE chasm_search_attributes (
  namespace_id      CHAR(64)        NOT NULL,
  run_id            CHAR(64)        NOT NULL,
  _version          BIGINT          NOT NULL DEFAULT 0,
  search_attributes JSON            NULL,

  -- Pre-allocated CHASM search attributes
  TemporalBool01            BOOLEAN         GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.TemporalBool01') IN ('true', '1')),
  TemporalBool02            BOOLEAN         GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.TemporalBool02') IN ('true', '1')),
  TemporalDatetime01        DATETIME(6)     GENERATED ALWAYS AS (
    CONVERT_TZ(
      REGEXP_REPLACE(JSON_VALUE(search_attributes, '$.TemporalDatetime01'), 'Z|[+-][0-9]{2}:[0-9]{2}$', ''),
      SUBSTR(REPLACE(JSON_VALUE(search_attributes, '$.TemporalDatetime01'), 'Z', '+00:00'), -6, 6),
      '+00:00'
    )
  ) PERSISTENT,
  TemporalDatetime02        DATETIME(6)     GENERATED ALWAYS AS (
    CONVERT_TZ(
      REGEXP_REPLACE(JSON_VALUE(search_attributes, '$.TemporalDatetime02'), 'Z|[+-][0-9]{2}:[0-9]{2}$', ''),
      SUBSTR(REPLACE(JSON_VALUE(search_attributes, '$.TemporalDatetime02'), 'Z', '+00:00'), -6, 6),
      '+00:00'
    )
  ) PERSISTENT,
  TemporalDouble01                DECIMAL(20, 5)  GENERATED ALWAYS AS (CAST(JSON_VALUE(search_attributes, '$.TemporalDouble01') AS DECIMAL(20, 5))),
  TemporalDouble02                DECIMAL(20, 5)  GENERATED ALWAYS AS (CAST(JSON_VALUE(search_attributes, '$.TemporalDouble02') AS DECIMAL(20, 5))),
  TemporalInt01                   BIGINT          GENERATED ALWAYS AS (CAST(JSON_VALUE(search_attributes, '$.TemporalInt01') AS SIGNED)),
  TemporalInt02                   BIGINT          GENERATED ALWAYS AS (CAST(JSON_VALUE(search_attributes, '$.TemporalInt02') AS SIGNED)),
  TemporalKeyword01               VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.TemporalKeyword01')),
  TemporalKeyword02               VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.TemporalKeyword02')),
  TemporalKeyword03               VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.TemporalKeyword03')),
  TemporalKeyword04               VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.TemporalKeyword04')),
  TemporalLowCardinalityKeyword01 VARCHAR(255)    GENERATED ALWAYS AS (JSON_VALUE(search_attributes, '$.TemporalLowCardinalityKeyword01')),
  TemporalKeywordList01           JSON            GENERATED ALWAYS AS (JSON_EXTRACT(search_attributes, '$.TemporalKeywordList01')),
  TemporalKeywordList02           JSON            GENERATED ALWAYS AS (JSON_EXTRACT(search_attributes, '$.TemporalKeywordList02')),

  PRIMARY KEY (namespace_id, run_id)
);

CREATE INDEX by_temporal_bool_01                    ON chasm_search_attributes (namespace_id, TemporalBool01);
CREATE INDEX by_temporal_bool_02                    ON chasm_search_attributes (namespace_id, TemporalBool02);
CREATE INDEX by_temporal_datetime_01                ON chasm_search_attributes (namespace_id, TemporalDatetime01);
CREATE INDEX by_temporal_datetime_02                ON chasm_search_attributes (namespace_id, TemporalDatetime02);
CREATE INDEX by_temporal_double_01                  ON chasm_search_attributes (namespace_id, TemporalDouble01);
CREATE INDEX by_temporal_double_02                  ON chasm_search_attributes (namespace_id, TemporalDouble02);
CREATE INDEX by_temporal_int_01                     ON chasm_search_attributes (namespace_id, TemporalInt01);
CREATE INDEX by_temporal_int_02                     ON chasm_search_attributes (namespace_id, TemporalInt02);
CREATE INDEX by_temporal_keyword_01                 ON chasm_search_attributes (namespace_id, TemporalKeyword01);
CREATE INDEX by_temporal_keyword_02                 ON chasm_search_attributes (namespace_id, TemporalKeyword02);
CREATE INDEX by_temporal_keyword_03                 ON chasm_search_attributes (namespace_id, TemporalKeyword03);
CREATE INDEX by_temporal_keyword_04                 ON chasm_search_attributes (namespace_id, TemporalKeyword04);
CREATE INDEX by_temporal_low_cardinality_keyword_01 ON chasm_search_attributes (namespace_id, TemporalLowCardinalityKeyword01);
