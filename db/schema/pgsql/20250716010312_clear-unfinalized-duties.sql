-- +goose Up
-- +goose StatementBegin

DELETE FROM public."unfinalized_duties";

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd