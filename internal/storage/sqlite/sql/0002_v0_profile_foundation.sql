-- Migration: 0002_v0_profile_foundation.sql
-- Description: V0 persona/archetype/session/project foundations.
--
-- Memory layer is stored in memories.metadata for backward compatibility with
-- the existing single memories table. These tables add the V0 profile model
-- without forcing a destructive table split.

CREATE TABLE IF NOT EXISTS archetypes (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    keywords       TEXT NOT NULL DEFAULT '[]',
    stable_priors  TEXT NOT NULL DEFAULT '[]',
    dynamic_priors TEXT NOT NULL DEFAULT '[]',
    created_at     DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at     DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS personas (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    archetype_id     TEXT,
    activation_score REAL NOT NULL DEFAULT 0.0,
    session_count    INTEGER NOT NULL DEFAULT 0,
    last_active      DATETIME,
    status           TEXT NOT NULL DEFAULT 'active',
    metadata         TEXT NOT NULL DEFAULT '{}',
    created_at       DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (archetype_id) REFERENCES archetypes(id)
);

CREATE INDEX IF NOT EXISTS idx_personas_archetype
    ON personas (archetype_id);

CREATE INDEX IF NOT EXISTS idx_personas_status
    ON personas (status);

CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT PRIMARY KEY,
    persona_id  TEXT,
    summary     TEXT NOT NULL DEFAULT '',
    tool        TEXT NOT NULL DEFAULT '',
    raw_path    TEXT NOT NULL DEFAULT '',
    metadata    TEXT NOT NULL DEFAULT '{}',
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (persona_id) REFERENCES personas(id)
);

CREATE INDEX IF NOT EXISTS idx_sessions_persona
    ON sessions (persona_id, created_at DESC);

CREATE TABLE IF NOT EXISTS projects (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    path        TEXT NOT NULL,
    lang        TEXT NOT NULL DEFAULT '',
    frameworks  TEXT NOT NULL DEFAULT '[]',
    active      INTEGER NOT NULL DEFAULT 1,
    metadata    TEXT NOT NULL DEFAULT '{}',
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_path
    ON projects (path);

CREATE TRIGGER IF NOT EXISTS archetypes_updated_at
    AFTER UPDATE ON archetypes
    WHEN NEW.updated_at = OLD.updated_at
BEGIN
    UPDATE archetypes
    SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
    WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS personas_updated_at
    AFTER UPDATE ON personas
    WHEN NEW.updated_at = OLD.updated_at
BEGIN
    UPDATE personas
    SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
    WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS projects_updated_at
    AFTER UPDATE ON projects
    WHEN NEW.updated_at = OLD.updated_at
BEGIN
    UPDATE projects
    SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
    WHERE id = NEW.id;
END;

INSERT INTO schema_migrations (version, name)
VALUES (2, '0002_v0_profile_foundation');
