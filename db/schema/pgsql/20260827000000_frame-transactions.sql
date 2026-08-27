-- +goose Up
-- +goose StatementBegin

-- Frames of an EIP-8141 frame transaction. Such a transaction is an ordered list of
-- calls rather than one, so the recipient, value, gas budget and status that
-- el_transactions holds once per transaction exist once per frame here. Its
-- el_transactions row carries to_id 0, the id no account has.
CREATE TABLE IF NOT EXISTS public."el_tx_frames" (
    tx_uid BIGINT NOT NULL,
    frame_index SMALLINT NOT NULL,
    mode SMALLINT NOT NULL DEFAULT 0,
    flags SMALLINT NOT NULL DEFAULT 0,
    status SMALLINT NOT NULL DEFAULT 0,
    rolled_back bool NOT NULL DEFAULT FALSE,
    to_id BIGINT NOT NULL DEFAULT 0,
    amount DOUBLE PRECISION NOT NULL DEFAULT 0,
    amount_raw bytea NOT NULL,
    method_id bytea NULL,
    data_len INT NOT NULL DEFAULT 0,
    exec_gas_limit BIGINT NOT NULL DEFAULT 0,
    state_gas_limit BIGINT NOT NULL DEFAULT 0,
    exec_gas_used BIGINT NOT NULL DEFAULT 0,
    state_gas_used BIGINT NOT NULL DEFAULT 0,
    log_count SMALLINT NOT NULL DEFAULT 0,
    trace_count INT NOT NULL DEFAULT 0,
    CONSTRAINT el_tx_frames_pkey PRIMARY KEY (tx_uid, frame_index)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS public."el_tx_frames";

-- +goose StatementEnd
