package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/neeldholiya04/nexus/internal/memory"
)

const (
	defaultStableTokenBudget  = 400
	defaultDynamicTokenBudget = 1100
	defaultStableLimit        = 8
	defaultDynamicLimit       = 12
)

type ContextOptions struct {
	Intent             string `json:"intent"`
	PersonaID          string `json:"persona_id,omitempty"`
	StableTokenBudget  int    `json:"stable_token_budget,omitempty"`
	DynamicTokenBudget int    `json:"dynamic_token_budget,omitempty"`
	StableLimit        int    `json:"stable_limit,omitempty"`
	DynamicLimit       int    `json:"dynamic_limit,omitempty"`
}

type ContextResult struct {
	Intent             string             `json:"intent"`
	Mode               string             `json:"mode"`
	PersonaID          string             `json:"persona_id,omitempty"`
	SecondaryPersonas  []string           `json:"secondary_personas,omitempty"`
	Resolution         *PersonaResolution `json:"resolution,omitempty"`
	StableTokenBudget  int                `json:"stable_token_budget"`
	DynamicTokenBudget int                `json:"dynamic_token_budget"`
	StableTokens       int                `json:"stable_tokens"`
	DynamicTokens      int                `json:"dynamic_tokens"`
	Stable             []ContextItem      `json:"stable"`
	Dynamic            []ContextItem      `json:"dynamic"`
	Text               string             `json:"text"`
}

type ContextItem struct {
	ID         string          `json:"id"`
	Layer      memory.Layer    `json:"layer"`
	Category   memory.Category `json:"category"`
	Content    string          `json:"content"`
	Confidence float64         `json:"confidence"`
	Score      float64         `json:"score"`
	Source     memory.Source   `json:"source"`
	Tags       []string        `json:"tags,omitempty"`
	Truncated  bool            `json:"truncated,omitempty"`
}

func (s *MemoryService) ComposeContext(ctx context.Context, opts ContextOptions) (*ContextResult, error) {
	opts = normalizeContextOptions(opts)
	if opts.Intent == "" {
		return nil, errors.New("compose context: intent cannot be empty")
	}
	if s.retriever == nil {
		return nil, errors.New("compose context: retriever is not configured")
	}

	resolution, err := s.ResolvePersona(ctx, opts.Intent)
	if err != nil {
		s.log.Warn("compose context: persona resolution failed", zap.Error(err))
		resolution = PersonaResolution{Mode: "broad", Explanation: "Persona resolution failed; using broad mode."}
	}
	if opts.PersonaID == "" && resolution.Primary != "" && (resolution.Mode == "primary" || resolution.Mode == "blend") {
		opts.PersonaID = resolution.Primary
	}

	result := &ContextResult{
		Intent:             opts.Intent,
		Mode:               resolution.Mode,
		PersonaID:          opts.PersonaID,
		SecondaryPersonas:  resolution.Secondary,
		Resolution:         &resolution,
		StableTokenBudget:  opts.StableTokenBudget,
		DynamicTokenBudget: opts.DynamicTokenBudget,
	}
	seen := make(map[string]struct{})
	allowedPersonas := allowedPersonaIDs(opts.PersonaID, resolution.Secondary, result.Mode)

	retrieveLimit := opts.StableLimit + opts.DynamicLimit
	if retrieveLimit < 20 {
		retrieveLimit = 20
	}
	ranked, err := s.Search(ctx, opts.Intent, SearchOptions{Limit: retrieveLimit})
	if err != nil {
		return nil, fmt.Errorf("compose context: search: %w", err)
	}
	focusedProjects := focusedProjectIDs(ranked, opts.Intent)
	allowUnfocusedProjects := wantsProjectContext(opts.Intent)
	filter := contextCandidateFilter{
		opts:                   opts,
		seen:                   seen,
		focusedProjects:        focusedProjects,
		allowUnfocusedProjects: allowUnfocusedProjects,
		allowedPersonas:        allowedPersonas,
	}
	for _, candidate := range ranked {
		addContextCandidate(result, candidate.Memory, candidate.FinalScore, filter)
	}

	if len(result.Stable) < opts.StableLimit || len(result.Dynamic) < opts.DynamicLimit {
		recent, err := s.store.List(ctx, memory.ListOptions{Limit: 100})
		if err != nil {
			return nil, fmt.Errorf("compose context: fallback list: %w", err)
		}
		for _, candidate := range recent {
			addContextCandidate(result, candidate, 0, filter)
		}
	}

	result.Text = formatContextText(result)
	return result, nil
}

func normalizeContextOptions(opts ContextOptions) ContextOptions {
	opts.Intent = strings.TrimSpace(opts.Intent)
	opts.PersonaID = strings.TrimSpace(opts.PersonaID)
	if opts.StableTokenBudget <= 0 {
		opts.StableTokenBudget = defaultStableTokenBudget
	}
	if opts.DynamicTokenBudget <= 0 {
		opts.DynamicTokenBudget = defaultDynamicTokenBudget
	}
	if opts.StableLimit <= 0 {
		opts.StableLimit = defaultStableLimit
	}
	if opts.DynamicLimit <= 0 {
		opts.DynamicLimit = defaultDynamicLimit
	}
	return opts
}

type contextCandidateFilter struct {
	opts                   ContextOptions
	seen                   map[string]struct{}
	focusedProjects        map[string]struct{}
	allowUnfocusedProjects bool
	allowedPersonas        map[string]struct{}
}

func addContextCandidate(result *ContextResult, m *memory.Memory, score float64, filter contextCandidateFilter) {
	if m == nil {
		return
	}
	if _, ok := filter.seen[m.ID]; ok {
		return
	}

	layer := m.Layer()
	switch layer {
	case memory.LayerStable:
		if len(result.Stable) >= filter.opts.StableLimit || result.StableTokens >= filter.opts.StableTokenBudget {
			return
		}
		item, tokens, ok := contextItem(m, score, layer, filter.opts.StableTokenBudget-result.StableTokens)
		if !ok {
			return
		}
		result.Stable = append(result.Stable, item)
		result.StableTokens += tokens
	case memory.LayerDynamic:
		if m.Category == memory.CategoryProject {
			if len(filter.focusedProjects) > 0 {
				if _, ok := filter.focusedProjects[m.ID]; !ok {
					return
				}
			} else if !filter.allowUnfocusedProjects {
				return
			}
		}
		if len(filter.allowedPersonas) > 0 && m.PersonaID() != "" {
			if _, ok := filter.allowedPersonas[m.PersonaID()]; !ok {
				return
			}
		}
		if filter.opts.PersonaID != "" && len(filter.allowedPersonas) == 0 && m.PersonaID() != "" && m.PersonaID() != filter.opts.PersonaID {
			return
		}
		if len(result.Dynamic) >= filter.opts.DynamicLimit || result.DynamicTokens >= filter.opts.DynamicTokenBudget {
			return
		}
		item, tokens, ok := contextItem(m, score, layer, filter.opts.DynamicTokenBudget-result.DynamicTokens)
		if !ok {
			return
		}
		result.Dynamic = append(result.Dynamic, item)
		result.DynamicTokens += tokens
	default:
		return
	}
	filter.seen[m.ID] = struct{}{}
}

func allowedPersonaIDs(primary string, secondary []string, mode string) map[string]struct{} {
	if mode != "primary" && mode != "blend" {
		return nil
	}
	out := map[string]struct{}{}
	if primary != "" {
		out[primary] = struct{}{}
	}
	if mode == "blend" {
		for _, id := range secondary {
			if id != "" {
				out[id] = struct{}{}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func contextItem(m *memory.Memory, score float64, layer memory.Layer, remainingTokens int) (ContextItem, int, bool) {
	content := strings.TrimSpace(m.Content)
	if content == "" || remainingTokens <= 0 {
		return ContextItem{}, 0, false
	}
	truncated := false
	tokens := estimateTokens(content)
	if tokens > remainingTokens {
		content = trimApproxTokens(content, remainingTokens)
		tokens = estimateTokens(content)
		truncated = true
	}
	if tokens <= 0 {
		return ContextItem{}, 0, false
	}

	return ContextItem{
		ID:         m.ID,
		Layer:      layer,
		Category:   m.Category,
		Content:    content,
		Confidence: m.Confidence,
		Score:      score,
		Source:     m.Source,
		Tags:       append([]string(nil), m.Tags...),
		Truncated:  truncated,
	}, tokens, true
}

func focusedProjectIDs(results []memory.RetrievalResult, intent string) map[string]struct{} {
	intentWords := wordSet(intent)
	if len(intentWords) == 0 {
		return nil
	}
	named := make(map[string]struct{})
	for _, result := range results {
		m := result.Memory
		if m == nil || m.Category != memory.CategoryProject || m.Layer() != memory.LayerDynamic {
			continue
		}
		if projectNameMatchesIntent(m, intentWords) {
			named[m.ID] = struct{}{}
		}
	}
	if len(named) > 0 {
		return named
	}
	return nil
}

func wantsProjectContext(intent string) bool {
	words := wordSet(intent)
	for _, word := range []string{"project", "projects", "repo", "repos", "repository", "workspace", "roadmap"} {
		if _, ok := words[word]; ok {
			return true
		}
	}
	return false
}

func projectNameMatchesIntent(m *memory.Memory, intentWords map[string]struct{}) bool {
	candidates := []string{extractProjectName(m.Content)}
	if path := extractProjectPath(m.Content); path != "" {
		candidates = append(candidates, projectNameFromPath(path))
	}
	for _, tag := range m.Tags {
		if strings.HasPrefix(strings.ToLower(tag), "project:") {
			candidates = append(candidates, strings.TrimSpace(tag[len("project:"):]))
		}
	}
	for _, candidate := range candidates {
		words := wordSet(candidate)
		if len(words) == 0 {
			continue
		}
		matched := 0
		for word := range words {
			if _, ok := intentWords[word]; ok {
				matched++
			}
		}
		if matched == len(words) {
			return true
		}
	}
	return false
}

func formatContextText(result *ContextResult) string {
	var sb strings.Builder
	sb.WriteString("Nexus Context\n")
	sb.WriteString(fmt.Sprintf("Intent: %s\n", result.Intent))
	sb.WriteString(fmt.Sprintf("Mode: %s\n", result.Mode))
	if result.PersonaID != "" {
		sb.WriteString(fmt.Sprintf("Persona: %s\n", result.PersonaID))
	}
	if len(result.SecondaryPersonas) > 0 {
		sb.WriteString(fmt.Sprintf("Secondary personas: %s\n", strings.Join(result.SecondaryPersonas, ", ")))
	}
	sb.WriteString(fmt.Sprintf("Budget: stable %d/%d tokens, dynamic %d/%d tokens\n\n",
		result.StableTokens, result.StableTokenBudget,
		result.DynamicTokens, result.DynamicTokenBudget))

	writeContextSection(&sb, "Stable Memory", result.Stable)
	sb.WriteString("\n")
	writeContextSection(&sb, "Dynamic Memory", result.Dynamic)

	if len(result.Stable) == 0 && len(result.Dynamic) == 0 {
		sb.WriteString("\nNo matching memories found.\n")
	}
	return strings.TrimSpace(sb.String())
}

func writeContextSection(sb *strings.Builder, title string, items []ContextItem) {
	sb.WriteString(title)
	sb.WriteString(":\n")
	if len(items) == 0 {
		sb.WriteString("- none\n")
		return
	}
	for _, item := range items {
		suffix := ""
		if item.Truncated {
			suffix = " [truncated]"
		}
		sb.WriteString(fmt.Sprintf("- [%s conf=%.2f score=%.3f] %s%s\n",
			item.Category, item.Confidence, item.Score, item.Content, suffix))
	}
}

func estimateTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	return (len(text) + 3) / 4
}

func trimApproxTokens(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	maxChars := maxTokens * 4
	if len(text) <= maxChars {
		return text
	}
	if maxChars <= 3 {
		return text[:maxChars]
	}
	return strings.TrimSpace(text[:maxChars-3]) + "..."
}
