DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM hives
        WHERE deleted_at IS NULL
        GROUP BY apiary_id, name
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot add hive name uniqueness constraint: duplicate active hive names exist within at least one apiary';
    END IF;
END $$;

CREATE UNIQUE INDEX idx_hives_apiary_id_name_unique_active
    ON hives (apiary_id, name)
    WHERE deleted_at IS NULL;
