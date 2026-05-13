-- +goose Up
-- +goose StatementBegin

-- Per-CL-client peer-score samples and downscore events. Two tables
-- keep the hot-path INSERT shapes small: samples are a continuous
-- time series, events fire only when the reporter actually moved the
-- target's score.

CREATE TABLE IF NOT EXISTS public."cl_peer_score_samples"
(
    "observed_at"          BIGINT NOT NULL,
    "reporter_peer_id"     TEXT NOT NULL,
    "reporter_client_type" SMALLINT NOT NULL,
    "target_peer_id"       TEXT NOT NULL,
    "target_client_type"   SMALLINT,
    "score"                DOUBLE PRECISION NOT NULL,
    "score_normalized"     DOUBLE PRECISION NOT NULL,
    "score_state"          TEXT NOT NULL,
    "components_json"      TEXT,
    "last_event_json"      TEXT,
    PRIMARY KEY ("observed_at", "reporter_peer_id", "target_peer_id")
);

CREATE INDEX IF NOT EXISTS "idx_cl_peer_score_samples_target"
    ON public."cl_peer_score_samples" ("target_peer_id", "observed_at");
CREATE INDEX IF NOT EXISTS "idx_cl_peer_score_samples_reporter"
    ON public."cl_peer_score_samples" ("reporter_peer_id", "observed_at");

CREATE TABLE IF NOT EXISTS public."cl_peer_score_events"
(
    "observed_at"      BIGINT NOT NULL,
    "reporter_peer_id" TEXT NOT NULL,
    "target_peer_id"   TEXT NOT NULL,
    "reason_code"      TEXT NOT NULL,
    "native_reason"    TEXT NOT NULL,
    "category"         TEXT NOT NULL,
    "delta"            DOUBLE PRECISION,
    "topic"            TEXT,
    "direction"        TEXT,
    PRIMARY KEY ("observed_at", "reporter_peer_id", "target_peer_id", "reason_code")
);

CREATE INDEX IF NOT EXISTS "idx_cl_peer_score_events_pair"
    ON public."cl_peer_score_events" ("reporter_peer_id", "target_peer_id", "observed_at");
CREATE INDEX IF NOT EXISTS "idx_cl_peer_score_events_reason"
    ON public."cl_peer_score_events" ("reason_code", "observed_at");

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public."cl_peer_score_events";
DROP TABLE IF EXISTS public."cl_peer_score_samples";
-- +goose StatementEnd
