package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/neeldholiya04/nexus/internal/memory"
)

const defaultPriorStaleAfter = 14 * 24 * time.Hour

type PriorStatusOptions struct {
	StaleAfter     time.Duration
	IncludeAll     bool
	Limit          int
	IncludeExpired bool
}

type PriorStatusResult struct {
	StaleAfterDays int               `json:"stale_after_days"`
	Total          int               `json:"total"`
	Reinforced     int               `json:"reinforced"`
	Pending        int               `json:"pending"`
	Unreinforced   int               `json:"unreinforced"`
	Stale          int               `json:"stale"`
	Contradicted   int               `json:"contradicted"`
	Items          []PriorStatusItem `json:"items"`
}

type PriorStatusItem struct {
	ID                  string          `json:"id"`
	ArchetypeID         string          `json:"archetype_id,omitempty"`
	PersonaID           string          `json:"persona_id,omitempty"`
	Layer               memory.Layer    `json:"layer"`
	Category            memory.Category `json:"category"`
	Status              string          `json:"status"`
	Confidence          float64         `json:"confidence"`
	EffectiveConfidence float64         `json:"effective_confidence"`
	AgeDays             int             `json:"age_days"`
	DaysSinceSignal     *int            `json:"days_since_signal,omitempty"`
	LastSignalAt        *time.Time      `json:"last_signal_at,omitempty"`
	Recommendation      string          `json:"recommendation,omitempty"`
	Content             string          `json:"content"`
}

func (s *MemoryService) PriorsStatus(ctx context.Context, opts PriorStatusOptions) (*PriorStatusResult, error) {
	if opts.StaleAfter <= 0 {
		opts.StaleAfter = defaultPriorStaleAfter
	}
	if opts.Limit <= 0 {
		opts.Limit = 1000
	}

	priors, err := s.store.List(ctx, memory.ListOptions{
		Tags:           []string{"archetype"},
		Limit:          opts.Limit,
		IncludeExpired: opts.IncludeExpired,
	})
	if err != nil {
		return nil, fmt.Errorf("priors status: list archetype priors: %w", err)
	}

	now := timeNowUTC()
	result := &PriorStatusResult{
		StaleAfterDays: int(opts.StaleAfter.Hours() / 24),
	}
	for _, prior := range priors {
		item := priorStatusItem(prior, now, opts.StaleAfter)
		result.Total++
		switch item.Status {
		case "reinforced":
			result.Reinforced++
		case "pending":
			result.Pending++
		case "unreinforced":
			result.Unreinforced++
		case "stale":
			result.Stale++
		case "contradicted":
			result.Contradicted++
		}
		if opts.IncludeAll || item.Status != "reinforced" {
			result.Items = append(result.Items, item)
		}
	}

	sort.Slice(result.Items, func(i, j int) bool {
		left, right := result.Items[i], result.Items[j]
		if priorStatusSeverity(left.Status) != priorStatusSeverity(right.Status) {
			return priorStatusSeverity(left.Status) > priorStatusSeverity(right.Status)
		}
		if left.AgeDays != right.AgeDays {
			return left.AgeDays > right.AgeDays
		}
		if left.PersonaID != right.PersonaID {
			return left.PersonaID < right.PersonaID
		}
		return left.ID < right.ID
	})
	return result, nil
}

func priorStatusItem(prior *memory.Memory, now time.Time, staleAfter time.Duration) PriorStatusItem {
	lastSignalAt := priorSignalTime(prior)
	var daysSinceSignal *int
	if lastSignalAt != nil {
		days := elapsedDays(now, *lastSignalAt)
		daysSinceSignal = &days
	}

	status := "pending"
	recommendation := ""
	switch {
	case prior.Confidence <= 0.30 || priorMetadataString(prior, "prior_status") == "contradicted":
		status = "contradicted"
		recommendation = "Review this prior and expire or replace it if the contradiction is valid."
	case lastSignalAt == nil && prior.Confidence < 0.65 && now.Sub(prior.CreatedAt) >= staleAfter:
		status = "unreinforced"
		recommendation = "Dogfood more sessions for this persona or manually remove the stale assumption."
	case lastSignalAt != nil && now.Sub(*lastSignalAt) >= staleAfter:
		status = "stale"
		recommendation = "Look for fresh evidence before relying on this prior."
	case prior.Confidence >= 0.65:
		status = "reinforced"
	default:
		status = "pending"
		recommendation = "Still inside the review window."
	}

	return PriorStatusItem{
		ID:                  prior.ID,
		ArchetypeID:         archetypeIDFromTags(prior.Tags),
		PersonaID:           prior.PersonaID(),
		Layer:               prior.Layer(),
		Category:            prior.Category,
		Status:              status,
		Confidence:          prior.Confidence,
		EffectiveConfidence: prior.EffectiveConfidence(now),
		AgeDays:             elapsedDays(now, prior.CreatedAt),
		DaysSinceSignal:     daysSinceSignal,
		LastSignalAt:        lastSignalAt,
		Recommendation:      recommendation,
		Content:             prior.Content,
	}
}

func priorStatusSeverity(status string) int {
	switch status {
	case "contradicted":
		return 5
	case "unreinforced":
		return 4
	case "stale":
		return 3
	case "pending":
		return 2
	case "reinforced":
		return 1
	default:
		return 0
	}
}

func priorSignalTime(prior *memory.Memory) *time.Time {
	for _, key := range []string{"prior_last_signal_at", "prior_reinforced_at", "prior_contradicted_at"} {
		if t, ok := priorMetadataTime(prior, key); ok {
			return &t
		}
	}
	if prior.Confidence >= 0.65 && !prior.UpdatedAt.IsZero() {
		t := prior.UpdatedAt
		return &t
	}
	return nil
}

func priorMetadataString(prior *memory.Memory, key string) string {
	if prior == nil || prior.Metadata == nil {
		return ""
	}
	value, _ := prior.Metadata[key].(string)
	return strings.TrimSpace(value)
}

func priorMetadataTime(prior *memory.Memory, key string) (time.Time, bool) {
	value := priorMetadataString(prior, key)
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func archetypeIDFromTags(tags []string) string {
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || strings.EqualFold(tag, "archetype") {
			continue
		}
		return tag
	}
	return ""
}

func elapsedDays(now, then time.Time) int {
	if then.IsZero() || now.Before(then) {
		return 0
	}
	return int(now.Sub(then).Hours() / 24)
}
