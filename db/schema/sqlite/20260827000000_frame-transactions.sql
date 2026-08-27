-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS "el_tx_frames" (
    tx_uid BIGINT NOT NULL,
    frame_index INTEGER NOT NULL,
    mode INTEGER NOT NULL DEFAULT 0,
    flags INTEGER NOT NULL DEFAULT 0,
    status INTEGER NOT NULL DEFAULT 0,
    rolled_back bool NOT NULL DEFAULT FALSE,
    to_id BIGINT NOT NULL DEFAULT 0,
    amount REAL NOT NULL DEFAULT 0,
    amount_raw BLOB NOT NULL,
    method_id BLOB NULL,
    data_len INTEGER NOT NULL DEFAULT 0,
    exec_gas_limit BIGINT NOT NULL DEFAULT 0,
    state_gas_limit BIGINT NOT NULL DEFAULT 0,
    exec_gas_used BIGINT NOT NULL DEFAULT 0,
    state_gas_used BIGINT NOT NULL DEFAULT 0,
    log_count INTEGER NOT NULL DEFAULT 0,
    trace_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (tx_uid, frame_index)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS "el_tx_frames";

-- +goose StatementEnd
