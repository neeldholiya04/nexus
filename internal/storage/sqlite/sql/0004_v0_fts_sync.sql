-- Migration: 0004_v0_fts_sync.sql
-- Description: Keep V0 stable/dynamic FTS indexes synchronized with memories.

DELETE FROM stable_fts;
DELETE FROM dynamic_fts;

INSERT INTO stable_fts(rowid, content)
SELECT memory_rowid, content
FROM stable_memories;

INSERT INTO dynamic_fts(rowid, content)
SELECT memory_rowid, content
FROM dynamic_memories;

CREATE TRIGGER IF NOT EXISTS stable_dynamic_fts_insert
    AFTER INSERT ON memories
BEGIN
    INSERT INTO stable_fts(rowid, content)
    SELECT new.rowid, new.content
    WHERE json_extract(new.metadata, '$.layer') = 'stable'
       OR (
            json_extract(new.metadata, '$.layer') IS NULL
            AND new.category NOT IN ('PROJECT', 'INFERRED')
       );

    INSERT INTO dynamic_fts(rowid, content)
    SELECT new.rowid, new.content
    WHERE json_extract(new.metadata, '$.layer') = 'dynamic'
       OR (
            json_extract(new.metadata, '$.layer') IS NULL
            AND new.category IN ('PROJECT', 'INFERRED')
       );
END;

CREATE TRIGGER IF NOT EXISTS stable_dynamic_fts_update
    AFTER UPDATE OF content, category, metadata ON memories
BEGIN
    DELETE FROM stable_fts WHERE rowid = old.rowid;
    DELETE FROM dynamic_fts WHERE rowid = old.rowid;

    INSERT INTO stable_fts(rowid, content)
    SELECT new.rowid, new.content
    WHERE json_extract(new.metadata, '$.layer') = 'stable'
       OR (
            json_extract(new.metadata, '$.layer') IS NULL
            AND new.category NOT IN ('PROJECT', 'INFERRED')
       );

    INSERT INTO dynamic_fts(rowid, content)
    SELECT new.rowid, new.content
    WHERE json_extract(new.metadata, '$.layer') = 'dynamic'
       OR (
            json_extract(new.metadata, '$.layer') IS NULL
            AND new.category IN ('PROJECT', 'INFERRED')
       );
END;

CREATE TRIGGER IF NOT EXISTS stable_dynamic_fts_delete
    BEFORE DELETE ON memories
BEGIN
    DELETE FROM stable_fts WHERE rowid = old.rowid;
    DELETE FROM dynamic_fts WHERE rowid = old.rowid;
END;

INSERT INTO schema_migrations (version, name)
VALUES (4, '0004_v0_fts_sync');
