-- How often a later job of the run received each run file, and when it last did.
-- The admin Run Files page shows both, so a hand-off nothing read stands out.
ALTER TABLE run_files ADD COLUMN reads INTEGER NOT NULL DEFAULT 0;
ALTER TABLE run_files ADD COLUMN read_at DATETIME;
