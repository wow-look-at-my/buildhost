-- One artifact row can cover several platforms. An Actually Portable Executable
-- is one file that boots natively on linux, macOS and Windows; publishing it as
-- N per-platform rows gives a consumer N download links for one binary.
--
-- artifact_platforms is the authority on which (os, arch) slots an artifact
-- occupies. Every artifact has at least one row here, including the
-- single-platform ones backfilled below, so lookup and slot-uniqueness have one
-- code path rather than two. release_id and kind are carried so the unique index
-- can enforce exactly the constraint artifacts.UNIQUE(release_id, os, arch, kind)
-- enforces for canonical slots: within a release, one (os, arch, kind) slot is
-- owned by at most one artifact.
-- ordinal is the publisher's declared order. Ordinal 0 is the artifact's
-- canonical slot -- the pair mirrored into artifacts.os/arch, the one every
-- covered platform's dl redirect folds to so all of them share one static URL,
-- one digest and one ETag.
CREATE TABLE artifact_platforms (
    artifact_id INTEGER NOT NULL REFERENCES artifacts(id),
    release_id  INTEGER NOT NULL REFERENCES releases(id),
    kind        TEXT NOT NULL,
    os          TEXT NOT NULL,
    arch        TEXT NOT NULL,
    ordinal     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (artifact_id, os, arch)
);

CREATE UNIQUE INDEX idx_artifact_platforms_slot
    ON artifact_platforms(release_id, kind, os, arch);

INSERT INTO artifact_platforms (artifact_id, release_id, kind, os, arch, ordinal)
SELECT id, release_id, kind, os, arch, 0 FROM artifacts;

-- The executable format detected from the uploaded bytes, '' when nothing was
-- detected. Multi-platform ingest requires a recognized format, so this names
-- what the artifact actually is rather than leaving the UI to assume: the
-- release page renders the badge as "<EXE_FORMAT>: <platforms>".
ALTER TABLE artifacts ADD COLUMN exe_format TEXT NOT NULL DEFAULT '';
