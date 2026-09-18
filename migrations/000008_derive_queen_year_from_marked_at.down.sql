ALTER TABLE hive_queens
    ADD COLUMN year INTEGER,
    ALTER COLUMN marked_at DROP NOT NULL;

UPDATE hive_queens SET year = EXTRACT(YEAR FROM marked_at)::INTEGER;

ALTER TABLE hive_queens ALTER COLUMN year SET NOT NULL;
