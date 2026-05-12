package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/neeldholiya04/nexus/internal/memory"
)

func (d *DB) UpsertArchetype(ctx context.Context, a *memory.Archetype) error {
	keywords, err := json.Marshal(a.Keywords)
	if err != nil {
		return fmt.Errorf("sqlite: UpsertArchetype: marshal keywords: %w", err)
	}
	stable, err := json.Marshal(a.StablePriors)
	if err != nil {
		return fmt.Errorf("sqlite: UpsertArchetype: marshal stable priors: %w", err)
	}
	dynamic, err := json.Marshal(a.DynamicPriors)
	if err != nil {
		return fmt.Errorf("sqlite: UpsertArchetype: marshal dynamic priors: %w", err)
	}
	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = now
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO archetypes
			(id, name, description, keywords, stable_priors, dynamic_priors, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			description=excluded.description,
			keywords=excluded.keywords,
			stable_priors=excluded.stable_priors,
			dynamic_priors=excluded.dynamic_priors`,
		a.ID, a.Name, a.Description, string(keywords), string(stable), string(dynamic),
		a.CreatedAt.Format(time.RFC3339Nano), a.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("sqlite: UpsertArchetype: %w", err)
	}
	return nil
}

func (d *DB) ListArchetypes(ctx context.Context) ([]*memory.Archetype, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, name, description, keywords, stable_priors, dynamic_priors, created_at, updated_at
		FROM archetypes
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: ListArchetypes: %w", err)
	}
	defer rows.Close()

	var out []*memory.Archetype
	for rows.Next() {
		a, err := scanArchetype(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (d *DB) UpsertPersona(ctx context.Context, p *memory.Persona) error {
	meta, err := json.Marshal(p.Metadata)
	if err != nil {
		return fmt.Errorf("sqlite: UpsertPersona: marshal metadata: %w", err)
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	}
	if p.Status == "" {
		p.Status = memory.PersonaActive
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO personas
			(id, name, archetype_id, centroid, activation_score, session_count, last_active, status, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			archetype_id=excluded.archetype_id,
			centroid=excluded.centroid,
			activation_score=excluded.activation_score,
			session_count=excluded.session_count,
			last_active=excluded.last_active,
			status=excluded.status,
			metadata=excluded.metadata`,
		p.ID, p.Name, nullableString(p.ArchetypeID), float32SliceToBlob(p.Centroid), p.ActivationScore, p.SessionCount,
		nullTime(p.LastActive), string(p.Status), string(meta),
		p.CreatedAt.Format(time.RFC3339Nano), p.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("sqlite: UpsertPersona: %w", err)
	}
	return nil
}

func (d *DB) ListPersonas(ctx context.Context) ([]*memory.Persona, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, name, archetype_id, centroid, activation_score, session_count, last_active, status, metadata, created_at, updated_at
		FROM personas
		ORDER BY activation_score DESC, updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: ListPersonas: %w", err)
	}
	defer rows.Close()

	var out []*memory.Persona
	for rows.Next() {
		p, err := scanPersona(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) RecordSession(ctx context.Context, s *memory.Session) error {
	meta, err := json.Marshal(s.Metadata)
	if err != nil {
		return fmt.Errorf("sqlite: RecordSession: marshal metadata: %w", err)
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO sessions (id, persona_id, summary, tool, raw_path, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ID, nullableString(s.PersonaID), s.Summary, s.Tool, s.RawPath, string(meta),
		s.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("sqlite: RecordSession: %w", err)
	}
	return nil
}

func (d *DB) UpsertProject(ctx context.Context, p *memory.Project) error {
	frameworks, err := json.Marshal(p.Frameworks)
	if err != nil {
		return fmt.Errorf("sqlite: UpsertProject: marshal frameworks: %w", err)
	}
	meta, err := json.Marshal(p.Metadata)
	if err != nil {
		return fmt.Errorf("sqlite: UpsertProject: marshal metadata: %w", err)
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	}
	active := 0
	if p.Active {
		active = 1
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO projects
			(id, name, path, lang, frameworks, active, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			name=excluded.name,
			lang=excluded.lang,
			frameworks=excluded.frameworks,
			active=excluded.active,
			metadata=excluded.metadata`,
		p.ID, p.Name, p.Path, p.Lang, string(frameworks), active, string(meta),
		p.CreatedAt.Format(time.RFC3339Nano), p.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("sqlite: UpsertProject: %w", err)
	}
	return nil
}

func (d *DB) ListProjects(ctx context.Context) ([]*memory.Project, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, name, path, lang, frameworks, active, metadata, created_at, updated_at
		FROM projects
		ORDER BY active DESC, updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: ListProjects: %w", err)
	}
	defer rows.Close()

	var out []*memory.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanArchetype(rows *sql.Rows) (*memory.Archetype, error) {
	var a memory.Archetype
	var keywords, stable, dynamic, created, updated string
	if err := rows.Scan(&a.ID, &a.Name, &a.Description, &keywords, &stable, &dynamic, &created, &updated); err != nil {
		return nil, fmt.Errorf("scan archetype: %w", err)
	}
	if err := json.Unmarshal([]byte(keywords), &a.Keywords); err != nil {
		return nil, fmt.Errorf("decode archetype keywords %q: %w", a.ID, err)
	}
	if err := json.Unmarshal([]byte(stable), &a.StablePriors); err != nil {
		return nil, fmt.Errorf("decode archetype stable priors %q: %w", a.ID, err)
	}
	if err := json.Unmarshal([]byte(dynamic), &a.DynamicPriors); err != nil {
		return nil, fmt.Errorf("decode archetype dynamic priors %q: %w", a.ID, err)
	}
	var err error
	a.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, fmt.Errorf("parse archetype created_at %q: %w", a.ID, err)
	}
	a.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return nil, fmt.Errorf("parse archetype updated_at %q: %w", a.ID, err)
	}
	return &a, nil
}

func scanPersona(rows *sql.Rows) (*memory.Persona, error) {
	var p memory.Persona
	var archetypeID, lastActive sql.NullString
	var centroid []byte
	var status, meta, created, updated string
	if err := rows.Scan(&p.ID, &p.Name, &archetypeID, &centroid, &p.ActivationScore, &p.SessionCount, &lastActive, &status, &meta, &created, &updated); err != nil {
		return nil, fmt.Errorf("scan persona: %w", err)
	}
	if archetypeID.Valid {
		p.ArchetypeID = archetypeID.String
	}
	p.Status = memory.PersonaStatus(status)
	p.Centroid = blobToFloat32Slice(centroid)
	if meta == "" {
		meta = "{}"
	}
	if err := json.Unmarshal([]byte(meta), &p.Metadata); err != nil {
		return nil, fmt.Errorf("decode persona metadata %q: %w", p.ID, err)
	}
	if lastActive.Valid {
		t, err := time.Parse(time.RFC3339Nano, lastActive.String)
		if err != nil {
			return nil, fmt.Errorf("parse persona last_active %q: %w", p.ID, err)
		}
		p.LastActive = &t
	}
	var err error
	p.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, fmt.Errorf("parse persona created_at %q: %w", p.ID, err)
	}
	p.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return nil, fmt.Errorf("parse persona updated_at %q: %w", p.ID, err)
	}
	return &p, nil
}

func scanProject(rows *sql.Rows) (*memory.Project, error) {
	var p memory.Project
	var frameworks, meta, created, updated string
	var active int
	if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Lang, &frameworks, &active, &meta, &created, &updated); err != nil {
		return nil, fmt.Errorf("scan project: %w", err)
	}
	if err := json.Unmarshal([]byte(frameworks), &p.Frameworks); err != nil {
		return nil, fmt.Errorf("decode project frameworks %q: %w", p.ID, err)
	}
	if meta == "" {
		meta = "{}"
	}
	if err := json.Unmarshal([]byte(meta), &p.Metadata); err != nil {
		return nil, fmt.Errorf("decode project metadata %q: %w", p.ID, err)
	}
	p.Active = active != 0
	var err error
	p.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, fmt.Errorf("parse project created_at %q: %w", p.ID, err)
	}
	p.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return nil, fmt.Errorf("parse project updated_at %q: %w", p.ID, err)
	}
	return &p, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
