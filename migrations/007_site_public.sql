-- A site may be published as public even when its project is private, so a PR
-- preview of a private repo is servable without a token while the project's
-- release artifacts stay private. The sites read path checks this per branch;
-- the project's own visibility is unchanged.
ALTER TABLE sites ADD COLUMN is_public INTEGER NOT NULL DEFAULT 0;
