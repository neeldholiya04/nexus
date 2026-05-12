package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/neeldholiya04/nexus/internal/memory"
)

const defaultEmbeddingCandidateLimit = 2000

func (d *DB) Insert(ctx context.Context, m *memory.Memory) error {
	if m.ID == "" {
		return errors.New("sqlite: Insert: ID cannot be empty")
	}
	if !m.Category.Valid() {
		return fmt.Errorf("sqlite: Insert: invalid category %q", m.Category)
	}

	tagsJSON, err := json.Marshal(m.Tags)
	if err != nil {
		return fmt.Errorf("sqlite: Insert: marshal tags: %w", err)
	}
	metaJSON, err := json.Marshal(m.Metadata)
	if err != nil {
		return fmt.Errorf("sqlite: Insert: marshal metadata: %w", err)
	}
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = now
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO memories
			(id, category, content, source, confidence, tags, embedding, access_count,
			 valid_from, valid_until, metadata, created_at, updated_at, last_accessed)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, string(m.Category), m.Content, string(m.Source), m.Confidence,
		string(tagsJSON), float32SliceToBlob(m.Embedding), m.AccessCount,
		nullTime(m.ValidFrom), nullTime(m.ValidUntil), string(metaJSON),
		m.CreatedAt.Format(time.RFC3339Nano), m.UpdatedAt.Format(time.RFC3339Nano),
		nullTime(m.LastAccessed),
	)
	if err != nil {
		return fmt.Errorf("sqlite: Insert: %w", err)
	}
	return nil
}

func (d *DB) Update(ctx context.Context, m *memory.Memory) error {
	if m.ID == "" {
		return errors.New("sqlite: Update: ID cannot be empty")
	}
	tagsJSON, err := json.Marshal(m.Tags)
	if err != nil {
		return fmt.Errorf("sqlite: Update: marshal tags: %w", err)
	}
	metaJSON, err := json.Marshal(m.Metadata)
	if err != nil {
		return fmt.Errorf("sqlite: Update: marshal metadata: %w", err)
	}

	result, err := d.db.ExecContext(ctx, `
		UPDATE memories
		SET content=?, confidence=?, tags=?, embedding=?,
		    valid_from=?, valid_until=?, metadata=?
		WHERE id=?`,
		m.Content, m.Confidence, string(tagsJSON), float32SliceToBlob(m.Embedding),
		nullTime(m.ValidFrom), nullTime(m.ValidUntil), string(metaJSON), m.ID,
	)
	if err != nil {
		return fmt.Errorf("sqlite: Update: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("sqlite: Update: memory %q not found", m.ID)
	}
	return nil
}

func (d *DB) Delete(ctx context.Context, id string) error {
	result, err := d.db.ExecContext(ctx, `DELETE FROM memories WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("sqlite: Delete: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("sqlite: Delete: memory %q not found", id)
	}
	return nil
}

func (d *DB) UpdateEmbedding(ctx context.Context, id string, embedding []float32) error {
	result, err := d.db.ExecContext(ctx,
		`UPDATE memories SET embedding=? WHERE id=?`,
		float32SliceToBlob(embedding), id,
	)
	if err != nil {
		return fmt.Errorf("sqlite: UpdateEmbedding: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("sqlite: UpdateEmbedding: memory %q not found", id)
	}
	return nil
}

func (d *DB) RecordAccess(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	ph := strings.Repeat("?,", len(ids))
	ph = ph[:len(ph)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	_, err := d.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE memories
		SET access_count = access_count + 1,
		    last_accessed = strftime('%%Y-%%m-%%dT%%H:%%M:%%fZ', 'now')
		WHERE id IN (%s)`, ph), args...)
	return err
}

func (d *DB) GetByID(ctx context.Context, id string) (*memory.Memory, error) {
	row := d.db.QueryRowContext(ctx, selectCols+` WHERE id=?`, id)
	m, err := scanMemory(row)
	if err != nil {
		if errors.Is(err, errNoRows) {
			return nil, fmt.Errorf("sqlite: memory %q not found", id)
		}
		return nil, fmt.Errorf("sqlite: GetByID: %w", err)
	}
	return m, nil
}

func (d *DB) List(ctx context.Context, opts ListOptions) ([]*memory.Memory, error) {
	q, args := buildListQuery(opts)
	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: List: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (d *DB) FTSSearch(ctx context.Context, queryText string, limit int) ([]*memory.Memory, error) {
	if queryText == "" {
		return nil, errors.New("sqlite: FTSSearch: query cannot be empty")
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT m.id, m.category, m.content, m.source, m.confidence,
		       m.tags, m.embedding, m.access_count,
		       m.valid_from, m.valid_until, m.metadata,
		       m.created_at, m.updated_at, m.last_accessed
		FROM memories m
		JOIN memories_fts f ON m.rowid = f.rowid
		WHERE memories_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, queryText, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: FTSSearch: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (d *DB) GetUnembedded(ctx context.Context, limit int) ([]*memory.Memory, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.db.QueryContext(ctx,
		selectCols+` WHERE embedding IS NULL ORDER BY created_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: GetUnembedded: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (d *DB) GetAllWithEmbeddings(ctx context.Context) ([]*memory.Memory, error) {
	rows, err := d.db.QueryContext(ctx,
		selectCols+` WHERE embedding IS NOT NULL ORDER BY updated_at DESC LIMIT ?`, defaultEmbeddingCandidateLimit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: GetAllWithEmbeddings: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

type StoreStats = memory.StoreStats

func (d *DB) Stats(ctx context.Context) (*memory.StoreStats, error) {
	stats := &memory.StoreStats{ByCategory: make(map[memory.Category]int)}

	if err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories`).
		Scan(&stats.Total); err != nil {
		return nil, fmt.Errorf("sqlite: Stats total: %w", err)
	}
	if err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memories WHERE embedding IS NOT NULL`).
		Scan(&stats.Embedded); err != nil {
		return nil, fmt.Errorf("sqlite: Stats embedded: %w", err)
	}

	rows, err := d.db.QueryContext(ctx,
		`SELECT category, COUNT(*) FROM memories GROUP BY category`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: Stats by category: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cat string
		var count int
		if err := rows.Scan(&cat, &count); err != nil {
			return nil, err
		}
		stats.ByCategory[memory.Category(cat)] = count
	}
	return stats, rows.Err()
}
