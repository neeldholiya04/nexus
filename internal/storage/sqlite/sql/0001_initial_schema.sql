-- Migration: 0001_initial_schema.sql
-- Description: Core Nexus memory schema.
--   - memories: primary store with temporal validity, confidence, access tracking
--   - memories_fts: FTS5 virtual table for full-text search
--   - schema_migrations: migration version tracking
--
-- Design notes:
--   - embedding stored as BLOB (little-endian float32 array, 768 dims for nomic-embed-text)
--   - tags stored as JSON array (SQLite JSON1 extension, included in modernc.org/sqlite)
--   - metadata stored as JSON blob for schema-free extension
--   - valid_from / valid_until prepared for V3 Graphiti temporal windows
--   - FTS5 uses content= for external content table (memory-efficient, keeps one source of truth)

-- Enable WAL mode for better concurrent read performance.
-- SQLite is single-writer; WAL allows readers to not block writers.
-- PRAGMAs (journal_mode, foreign_keys, busy_timeout) are set by the
-- application before running migrations because some PRAGMAs cannot be
-- executed inside a transaction.

-- ============================================================
-- schema_migrations: tracks applied migrations
-- ============================================================
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL,
    applied_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- ============================================================
-- memories: core memory store
-- ============================================================
CREATE TABLE IF NOT EXISTS memories (
    id           TEXT     PRIMARY KEY,
    category     TEXT     NOT NULL CHECK (category IN ('FACT','PREFERENCE','WORKFLOW','PROJECT','CODING_STYLE','INFERRED')),
    content      TEXT     NOT NULL,
    source       TEXT     NOT NULL DEFAULT 'manual',
    confidence   REAL     NOT NULL DEFAULT 1.0 CHECK (confidence >= 0.0 AND confidence <= 1.0),
    tags         TEXT     NOT NULL DEFAULT '[]',    -- JSON array of strings
    embedding    BLOB,                              -- float32 array, NULL until embedded
    access_count INTEGER  NOT NULL DEFAULT 0,
    valid_from   DATETIME,
    valid_until  DATETIME,
    metadata     TEXT     NOT NULL DEFAULT '{}',   -- JSON object
    created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_accessed DATETIME
);

-- Index: category lookups (common filter in retrieval pipeline)
CREATE INDEX IF NOT EXISTS idx_memories_category
    ON memories (category);

-- Index: recency scoring (order by updated_at DESC)
CREATE INDEX IF NOT EXISTS idx_memories_updated_at
    ON memories (updated_at DESC);

-- Index: confidence filtering
CREATE INDEX IF NOT EXISTS idx_memories_confidence
    ON memories (confidence DESC);

-- Index: temporal validity window queries
CREATE INDEX IF NOT EXISTS idx_memories_validity
    ON memories (valid_from, valid_until);

-- Index: source filtering (for ingestion provenance tracking)
CREATE INDEX IF NOT EXISTS idx_memories_source
    ON memories (source);

-- ============================================================
-- memories_fts: FTS5 virtual table for full-text search
--
-- Uses content= (external content table) to avoid duplicating
-- the content column. Requires manual sync via triggers below.
-- ============================================================
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    content,
    content='memories',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

-- FTS5 sync triggers: keep the index in sync with the memories table.
-- INSERT trigger: index new memories
CREATE TRIGGER IF NOT EXISTS memories_fts_insert
    AFTER INSERT ON memories
BEGIN
    INSERT INTO memories_fts (rowid, content)
    VALUES (new.rowid, new.content);
END;

-- UPDATE trigger: update FTS index when content changes
CREATE TRIGGER IF NOT EXISTS memories_fts_update
    AFTER UPDATE OF content ON memories
BEGIN
    INSERT INTO memories_fts (memories_fts, rowid, content)
    VALUES ('delete', old.rowid, old.content);
    INSERT INTO memories_fts (rowid, content)
    VALUES (new.rowid, new.content);
END;

-- DELETE trigger: remove from FTS index when memory is deleted
CREATE TRIGGER IF NOT EXISTS memories_fts_delete
    BEFORE DELETE ON memories
BEGIN
    INSERT INTO memories_fts (memories_fts, rowid, content)
    VALUES ('delete', old.rowid, old.content);
END;

-- Auto-update updated_at on memory modification
CREATE TRIGGER IF NOT EXISTS memories_updated_at
    AFTER UPDATE ON memories
    WHEN NEW.updated_at = OLD.updated_at
BEGIN
    UPDATE memories
    SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
    WHERE id = NEW.id;
END;

-- Record this migration as applied
INSERT INTO schema_migrations (version, name)
VALUES (1, '0001_initial_schema');
