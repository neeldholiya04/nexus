package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/neeldholiya04/nexus/internal/memory"
)

const selectCols = `
	SELECT id, category, content, source, confidence,
	       tags, embedding, access_count,
	       valid_from, valid_until, metadata,
	       created_at, updated_at, last_accessed
	FROM memories`

type ListOptions = memory.ListOptions

func buildListQuery(opts ListOptions) (string, []any) {
	var conds []string
	var args []any

	if len(opts.Categories) > 0 {
		ph := strings.Repeat("?,", len(opts.Categories))
		ph = ph[:len(ph)-1]
		conds = append(conds, fmt.Sprintf("category IN (%s)", ph))
		for _, c := range opts.Categories {
			args = append(args, string(c))
		}
	}

	if opts.MinConfidence > 0 {
		conds = append(conds, "confidence >= ?")
		args = append(args, opts.MinConfidence)
	}

	if !opts.IncludeExpired {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		conds = append(conds,
			"(valid_from IS NULL OR valid_from <= ?) AND (valid_until IS NULL OR valid_until >= ?)")
		args = append(args, now, now)
	}

	for _, tag := range opts.Tags {
		conds = append(conds,
			"EXISTS (SELECT 1 FROM json_each(tags) WHERE value = ?)")
		args = append(args, tag)
	}

	q := selectCols
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY updated_at DESC"

	if opts.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, opts.Limit)
	}
	if opts.Offset > 0 {
		q += " OFFSET ?"
		args = append(args, opts.Offset)
	}
	return q, args
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMemory(s scanner) (*memory.Memory, error) {
	var (
		m                                            memory.Memory
		catStr, srcStr                               string
		tagsJSON, metaJSON                           string
		blob                                         []byte
		createdStr, updatedStr                       string
		lastAccessedStr, validFromStr, validUntilStr sql.NullString
	)

	err := s.Scan(
		&m.ID, &catStr, &m.Content, &srcStr, &m.Confidence,
		&tagsJSON, &blob, &m.AccessCount,
		&validFromStr, &validUntilStr, &metaJSON,
		&createdStr, &updatedStr, &lastAccessedStr,
	)
	if err != nil {
		return nil, err
	}

	m.Category = memory.Category(catStr)
	m.Source = memory.Source(srcStr)

	if tagsJSON == "" {
		tagsJSON = "[]"
	}
	if err := json.Unmarshal([]byte(tagsJSON), &m.Tags); err != nil {
		return nil, fmt.Errorf("decode tags for memory %q: %w", m.ID, err)
	}
	if m.Tags == nil {
		m.Tags = []string{}
	}
	if metaJSON == "" {
		metaJSON = "{}"
	}
	if err := json.Unmarshal([]byte(metaJSON), &m.Metadata); err != nil {
		return nil, fmt.Errorf("decode metadata for memory %q: %w", m.ID, err)
	}
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}

	m.Embedding = blobToFloat32Slice(blob)

	parseTime := func(name, s string) (time.Time, error) {
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse %s for memory %q: %w", name, m.ID, err)
		}
		return t, nil
	}
	parseNullTime := func(name string, ns sql.NullString) (*time.Time, error) {
		if !ns.Valid {
			return nil, nil
		}
		t, err := time.Parse(time.RFC3339Nano, ns.String)
		if err != nil {
			return nil, fmt.Errorf("parse %s for memory %q: %w", name, m.ID, err)
		}
		return &t, nil
	}

	m.CreatedAt, err = parseTime("created_at", createdStr)
	if err != nil {
		return nil, err
	}
	m.UpdatedAt, err = parseTime("updated_at", updatedStr)
	if err != nil {
		return nil, err
	}
	m.LastAccessed, err = parseNullTime("last_accessed", lastAccessedStr)
	if err != nil {
		return nil, err
	}
	m.ValidFrom, err = parseNullTime("valid_from", validFromStr)
	if err != nil {
		return nil, err
	}
	m.ValidUntil, err = parseNullTime("valid_until", validUntilStr)
	if err != nil {
		return nil, err
	}

	return &m, nil
}

func scanMemories(rows *sql.Rows) ([]*memory.Memory, error) {
	var out []*memory.Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}
