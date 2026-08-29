DROP INDEX idx_hives_user_id_created_at;

CREATE INDEX idx_hives_user_id ON hives (user_id) WHERE deleted_at IS NULL;
