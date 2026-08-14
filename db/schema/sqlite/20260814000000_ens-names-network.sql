-- +goose Up
-- +goose StatementBegin
CREATE TABLE "ens_names_new" (
    address BLOB NOT NULL,
    network TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    resolved_time INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT ens_names_pkey PRIMARY KEY (address, network)
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO "ens_names_new" (address, network, name, resolved_time)
SELECT address, '', name, resolved_time FROM "ens_names";
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE "ens_names";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ens_names_new" RENAME TO "ens_names";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS "ens_names_name_idx" ON "ens_names" ("name") WHERE "name" != '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS "ens_names_name_idx";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "ens_names_old" (
    address BLOB NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    resolved_time INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT ens_names_pkey PRIMARY KEY (address)
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO "ens_names_old" (address, name, resolved_time)
SELECT address, name, resolved_time FROM "ens_names" WHERE network = '';
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE "ens_names";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ens_names_old" RENAME TO "ens_names";
-- +goose StatementEnd
