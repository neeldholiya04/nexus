-- Migration: 0003_v0_completion.sql
-- Description: Complete V0 roadmap compatibility pieces.
-- Adds persona centroids and stable/dynamic memory views over the canonical
-- memories table. The views keep existing data and APIs intact while exposing
-- the two-layer model requested by the V0 plan.

ALTER TABLE personas ADD COLUMN centroid BLOB;

CREATE VIEW IF NOT EXISTS stable_memories AS
SELECT rowid AS memory_rowid, *
FROM memories
WHERE json_extract(metadata, '$.layer') = 'stable'
   OR (
        json_extract(metadata, '$.layer') IS NULL
        AND category NOT IN ('PROJECT', 'INFERRED')
   );

CREATE VIEW IF NOT EXISTS dynamic_memories AS
SELECT rowid AS memory_rowid, *
FROM memories
WHERE json_extract(metadata, '$.layer') = 'dynamic'
   OR (
        json_extract(metadata, '$.layer') IS NULL
        AND category IN ('PROJECT', 'INFERRED')
   );

CREATE VIRTUAL TABLE IF NOT EXISTS stable_fts USING fts5(
    content,
    tokenize='porter unicode61'
);

CREATE VIRTUAL TABLE IF NOT EXISTS dynamic_fts USING fts5(
    content,
    tokenize='porter unicode61'
);

INSERT INTO stable_fts(rowid, content)
SELECT memory_rowid, content
FROM stable_memories
WHERE memory_rowid NOT IN (SELECT rowid FROM stable_fts);

INSERT INTO dynamic_fts(rowid, content)
SELECT memory_rowid, content
FROM dynamic_memories
WHERE memory_rowid NOT IN (SELECT rowid FROM dynamic_fts);

INSERT INTO schema_migrations (version, name)
VALUES (3, '0003_v0_completion');
