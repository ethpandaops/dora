-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."blobs" RENAME TO "old_blobs"; 

CREATE TABLE IF NOT EXISTS public."blobs"
(
    "commitment" bytea NOT NULL,
    "proof" bytea NOT NULL,
    "size" INT NOT NULL,
    "blob" bytea NULL,
    PRIMARY KEY ("commitment")
);

INSERT INTO public."blobs" (
    commitment, proof, size, blob
)
SELECT 
    commitment, proof, size, blob
FROM public."old_blobs";

CREATE TABLE IF NOT EXISTS public."blob_assignments"
(
    "commitment" bytea NOT NULL,
    "slot" bigint NOT NULL,
    "root" bytea NOT NULL,
    PRIMARY KEY ("root", "commitment")
);

INSERT INTO public."blob_assignments" (
    root, commitment, slot
)
SELECT 
    root, commitment, slot
FROM public."old_blobs";

DROP TABLE public."old_blobs";

CREATE INDEX IF NOT EXISTS "blob_assignments_commitment_idx"
    ON public."blob_assignments" 
    ("commitment" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "blobs_commitment_idx"
    ON public."blobs" 
    ("commitment" ASC NULLS LAST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
