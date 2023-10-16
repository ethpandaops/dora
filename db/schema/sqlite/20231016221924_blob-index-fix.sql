-- +goose Up
-- +goose StatementBegin

ALTER TABLE "blobs" RENAME TO "old_blobs"; 

CREATE TABLE IF NOT EXISTS "blobs"
(
    "commitment" BLOB NOT NULL,
    "proof" BLOB NOT NULL,
    "size" INT NOT NULL,
    "blob" BLOB NULL,
    PRIMARY KEY ("commitment")
);

INSERT INTO "blobs" (
    commitment, proof, size, blob
)
SELECT 
    commitment, proof, size, blob
FROM "old_blobs";

CREATE TABLE IF NOT EXISTS "blob_assignments"
(
    "root" BLOB NOT NULL,
    "commitment" BLOB NOT NULL,
    "slot" bigint NOT NULL,
    PRIMARY KEY ("root", "commitment")
);

INSERT INTO "blob_assignments" (
    root, commitment, slot
)
SELECT 
    root, commitment, slot
FROM "old_blobs";


DROP TABLE "old_blobs";

CREATE INDEX IF NOT EXISTS "blob_assignments_commitment_idx"
    ON "blob_assignments" 
    ("commitment" ASC);

CREATE INDEX IF NOT EXISTS "blob_assignments_root_idx"
    ON "blob_assignments" 
    ("root" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
