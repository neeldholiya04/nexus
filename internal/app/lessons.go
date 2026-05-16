package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/neeldholiya04/nexus/internal/memory"
)

func (s *MemoryService) GenerateLessons(ctx context.Context, limit int) (string, error) {
	if limit <= 0 {
		limit = 20
	}
	memories, err := s.store.List(ctx, memory.ListOptions{Limit: limit})
	if err != nil {
		return "", fmt.Errorf("lessons: list memories: %w", err)
	}
	personas, err := s.store.ListPersonas(ctx)
	if err != nil {
		return "", fmt.Errorf("lessons: list personas: %w", err)
	}
	stats, err := s.store.Stats(ctx)
	if err != nil {
		return "", fmt.Errorf("lessons: stats: %w", err)
	}

	var stable, dynamic []*memory.Memory
	for _, m := range memories {
		if m.Layer() == memory.LayerStable {
			stable = append(stable, m)
		} else {
			dynamic = append(dynamic, m)
		}
	}

	var sb strings.Builder
	sb.WriteString("# Nexus Lessons\n\n")
	sb.WriteString("This file is generated from Nexus memory. Treat it as a human-readable digest, not the database of record.\n\n")
	sb.WriteString("## Snapshot\n\n")
	sb.WriteString(fmt.Sprintf("- Memories: %d total, %d embedded\n", stats.Total, stats.Embedded))
	sb.WriteString(fmt.Sprintf("- Personas: %d\n", len(personas)))
	if len(stats.ByCategory) > 0 {
		sb.WriteString("- Categories:")
		for _, cat := range sortedCategoryCounts(stats.ByCategory) {
			sb.WriteString(fmt.Sprintf(" %s=%d", cat.Category, cat.Count))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n## Active Personas\n\n")
	if len(personas) == 0 {
		sb.WriteString("- none\n")
	} else {
		for _, p := range personas {
			sb.WriteString(fmt.Sprintf("- %s (%s): activation %.2f, sessions %d\n",
				p.ID, p.Name, p.ActivationScore, p.SessionCount))
		}
	}

	sb.WriteString("\n## Stable Lessons\n\n")
	writeLessonsMemories(&sb, stable)

	sb.WriteString("\n## Dynamic Context\n\n")
	writeLessonsMemories(&sb, dynamic)

	return strings.TrimSpace(sb.String()) + "\n", nil
}

type categoryCount struct {
	Category memory.Category
	Count    int
}

func sortedCategoryCounts(in map[memory.Category]int) []categoryCount {
	out := make([]categoryCount, 0, len(in))
	for cat, count := range in {
		out = append(out, categoryCount{Category: cat, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Category < out[j].Category
	})
	return out
}

func writeLessonsMemories(sb *strings.Builder, memories []*memory.Memory) {
	if len(memories) == 0 {
		sb.WriteString("- none\n")
		return
	}
	for _, m := range memories {
		persona := ""
		if m.PersonaID() != "" {
			persona = " persona=" + m.PersonaID()
		}
		sb.WriteString(fmt.Sprintf("- [%s conf=%.2f%s] %s\n", m.Category, m.Confidence, persona, m.Content))
	}
}
