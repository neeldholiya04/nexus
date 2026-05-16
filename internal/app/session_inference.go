package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	inferpkg "github.com/neeldholiya04/nexus/internal/inference"
	"github.com/neeldholiya04/nexus/internal/memory"
)

const InferSystemPrompt = `You are a memory extraction engine for a personal AI context system called Nexus.

Your job: read a conversation and extract durable facts, preferences, workflows, projects, coding patterns, and current context about the user.

RULES:
- Extract only information ABOUT THE USER, not general knowledge
- Be specific and concrete — "prefers early returns in Go" not "has coding preferences"
- Each memory must be a single, standalone fact
- Choose layer: "stable" for durable identity/preferences/workflows, "dynamic" for current projects/tasks/temporary context
- Include evidence: short quotes or message summaries that support the memory
- Confidence: 0.9+ for explicit statements, 0.7-0.9 for strong implications, 0.5-0.7 for inferences
- Skip pleasantries, filler, and anything not about the user
- Return at most 8 memories
- Return ONLY a valid JSON array, no markdown fences, no explanation

CATEGORIES (use exactly these strings):
- FACT: concrete verifiable facts (name, job, location, tools used)
- PREFERENCE: stated or implied preferences
- WORKFLOW: how the user approaches recurring tasks
- PROJECT: specific projects they are working on
- CODING_STYLE: how they write code
- INFERRED: behavioral patterns you infer from context

OUTPUT FORMAT (JSON array, nothing else):
[
  {
    "category": "FACT",
    "layer": "stable",
    "content": "Works at RubixKube as an SRE-focused engineer",
    "confidence": 0.95,
    "tags": ["work", "sre"],
    "evidence": ["User said they work at RubixKube"]
  }
]

If nothing meaningful can be extracted, return an empty array: []`

type ExtractedMemory struct {
	Category   string   `json:"category"`
	Layer      string   `json:"layer"`
	Content    string   `json:"content"`
	Confidence float64  `json:"confidence"`
	Tags       []string `json:"tags"`
	Evidence   []string `json:"evidence"`
}

type InferSessionInput struct {
	Text       string
	Source     string
	Tool       string
	DryRun     bool
	MaxChars   int
	MaxResults int
	Timeout    time.Duration
}

type InferSessionResult struct {
	Extracted    []ExtractedMemory `json:"extracted"`
	Inserted     int               `json:"inserted"`
	Updated      int               `json:"updated"`
	Skipped      int               `json:"skipped"`
	InputTokens  int               `json:"input_tokens"`
	OutputTokens int               `json:"output_tokens"`
	Provider     string            `json:"provider"`
	Model        string            `json:"model"`
	DryRun       bool              `json:"dry_run"`
}

func (s *MemoryService) InferSession(ctx context.Context, provider inferpkg.Provider, in InferSessionInput) (*InferSessionResult, error) {
	if provider == nil {
		return nil, fmt.Errorf("infer session: provider is nil")
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil, fmt.Errorf("infer session: text cannot be empty")
	}

	userMessage := BuildInferUserMessage(text, in.MaxChars)
	llmCtx := ctx
	var cancel context.CancelFunc
	if in.Timeout > 0 {
		llmCtx, cancel = context.WithTimeout(ctx, in.Timeout)
		defer cancel()
	}
	resp, err := provider.Complete(llmCtx, inferpkg.CompletionRequest{
		SystemPrompt: InferSystemPrompt,
		UserMessage:  userMessage,
	})
	if err != nil {
		return nil, fmt.Errorf("infer session: LLM call failed: %w", err)
	}

	extracted, err := ParseExtractionResponse(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("infer session: parse LLM response: %w\n\nRaw output:\n%s", err, resp.Content)
	}
	if in.MaxResults <= 0 {
		in.MaxResults = 8
	}
	if len(extracted) > in.MaxResults {
		extracted = extracted[:in.MaxResults]
	}

	result := &InferSessionResult{
		Extracted:    extracted,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
		Provider:     resp.Provider,
		Model:        resp.Model,
		DryRun:       in.DryRun,
	}
	if in.DryRun || len(extracted) == 0 {
		return result, nil
	}

	resolution, _ := s.ResolvePersona(ctx, text)
	personaID := resolution.Primary
	var upsertedMemories []*memory.Memory
	for _, e := range extracted {
		cat := memory.Category(e.Category)
		if !cat.Valid() {
			result.Skipped++
			continue
		}
		layer, ok := memory.ParseLayer(e.Layer)
		if !ok {
			layer = memory.DefaultLayer(cat)
		}
		conf := e.Confidence
		if conf < 0.50 {
			conf = 0.50
		}
		if conf > 1.0 {
			conf = 1.0
		}
		tags := append([]string(nil), e.Tags...)
		if layer == memory.LayerDynamic && personaID == "" {
			personaID = "default"
		}

		m := &memory.Memory{
			ID:         s.newID(),
			Category:   cat,
			Content:    e.Content,
			Source:     memory.SourceIngestion,
			Confidence: conf,
			Tags:       tags,
			Metadata: map[string]any{
				"inferred_by": provider.Name(),
				"model":       resp.Model,
				"source":      strings.TrimSpace(in.Source),
				"tool":        strings.TrimSpace(in.Tool),
				"evidence":    e.Evidence,
			},
		}
		m.SetLayer(layer)
		if layer == memory.LayerDynamic {
			m.SetPersonaID(personaID)
		}

		upsert, err := s.UpsertMemory(ctx, m)
		if err != nil {
			result.Skipped++
			continue
		}
		upsertedMemories = append(upsertedMemories, upsert.Memory)
		if upsert.Inserted {
			result.Inserted++
		} else if upsert.Updated {
			result.Updated++
		}
	}
	if err := s.bootstrapArchetypePriors(ctx, upsertedMemories); err != nil {
		s.log.Warn("infer session: confidence bootstrap failed")
	}

	if err := s.RecordSession(ctx, memory.Session{
		ID:        s.newID(),
		PersonaID: personaID,
		Summary:   summarizeSessionText(text),
		Tool:      strings.TrimSpace(in.Tool),
		RawPath:   strings.TrimSpace(in.Source),
		Metadata: map[string]any{
			"inferred_by": provider.Name(),
			"model":       resp.Model,
			"memories":    len(extracted),
		},
	}); err != nil {
		s.log.Warn("infer session: record session failed")
	}
	if personaID != "" {
		s.updatePersonaFromSession(ctx, personaID, text, resolution.Scores[personaID])
	}

	return result, nil
}

func (s *MemoryService) RecordSession(ctx context.Context, session memory.Session) error {
	if session.ID == "" {
		session.ID = s.newID()
	}
	return s.store.RecordSession(ctx, &session)
}

func BuildInferUserMessage(text string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 12000
	}
	if len(text) > maxChars {
		text = text[:maxChars] + "\n\n[CONVERSATION TRUNCATED — extract from what is shown above]"
	}
	return fmt.Sprintf("Extract memories from this conversation:\n\n%s", text)
}

func ParseExtractionResponse(raw string) ([]ExtractedMemory, error) {
	cleaned := strings.TrimSpace(raw)
	if strings.HasPrefix(cleaned, "```") {
		var lines []string
		scanner := bufio.NewScanner(strings.NewReader(cleaned))
		inFence := false
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				if inFence {
					break
				}
				inFence = true
				continue
			}
			if inFence {
				lines = append(lines, line)
			}
		}
		if len(lines) > 0 {
			cleaned = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}

	start := strings.Index(cleaned, "[")
	end := strings.LastIndex(cleaned, "]")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON array found in LLM response")
	}
	cleaned = cleaned[start : end+1]

	var memories []ExtractedMemory
	if err := json.Unmarshal([]byte(cleaned), &memories); err != nil {
		return nil, fmt.Errorf("JSON parse: %w", err)
	}
	return memories, nil
}

func summarizeSessionText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= 240 {
		return text
	}
	return text[:237] + "..."
}

func (s *MemoryService) bootstrapArchetypePriors(ctx context.Context, inferred []*memory.Memory) error {
	if len(inferred) == 0 {
		return nil
	}
	all, err := s.store.List(ctx, memory.ListOptions{Tags: []string{"archetype"}, Limit: 500})
	if err != nil {
		return err
	}
	for _, prior := range all {
		newConf := prior.Confidence
		changed := false
		now := timeNowUTC()
		for _, m := range inferred {
			if m == nil || m.ID == prior.ID || m.PersonaID() == "" {
				continue
			}
			if prior.PersonaID() != m.PersonaID() || prior.Layer() != m.Layer() {
				continue
			}
			similarity := jaccardSimilarity(prior.Content, m.Content)
			if similarity < 0.25 {
				continue
			}
			if containsContradiction(prior.Content, m.Content) {
				newConf = 0.30
				stampPriorSignal(prior, "contradicted", now)
				changed = true
				break
			}
			if newConf < 0.65 {
				newConf = 0.65
			}
			stampPriorSignal(prior, "reinforced", now)
			changed = true
		}
		if newConf == prior.Confidence && !changed {
			continue
		}
		prior.Confidence = newConf
		if err := s.store.Update(ctx, prior); err != nil {
			return err
		}
	}
	return nil
}

func (s *MemoryService) updatePersonaFromSession(ctx context.Context, personaID, text string, score float64) {
	personas, err := s.store.ListPersonas(ctx)
	if err != nil {
		s.log.Warn("persona update: list failed")
		return
	}
	var persona *memory.Persona
	for _, candidate := range personas {
		if candidate.ID == personaID {
			persona = candidate
			break
		}
	}
	if persona == nil {
		return
	}
	now := timeNowUTC()
	persona.LastActive = &now
	persona.SessionCount++
	if score <= 0 {
		score = 0.50
	}
	if persona.ActivationScore <= 0 {
		persona.ActivationScore = score
	} else {
		persona.ActivationScore = 0.90*persona.ActivationScore + 0.10*score
	}
	if s.embedder != nil {
		if vec, err := s.embedder.Embed(ctx, firstWords(text, 50)); err == nil {
			persona.Centroid = emaEmbedding(persona.Centroid, vec, 0.10)
		}
	}
	if err := s.store.UpsertPersona(ctx, persona); err != nil {
		s.log.Warn("persona update: upsert failed")
	}
}

func emaEmbedding(existing, incoming []float32, alpha float32) []float32 {
	if len(existing) == 0 || len(existing) != len(incoming) {
		return append([]float32(nil), incoming...)
	}
	out := make([]float32, len(existing))
	for i := range existing {
		out[i] = (1-alpha)*existing[i] + alpha*incoming[i]
	}
	return out
}

func stampPriorSignal(prior *memory.Memory, status string, at time.Time) {
	if prior.Metadata == nil {
		prior.Metadata = map[string]any{}
	}
	ts := at.UTC().Format(time.RFC3339Nano)
	prior.Metadata["prior_status"] = status
	prior.Metadata["prior_last_signal_at"] = ts
	switch status {
	case "reinforced":
		prior.Metadata["prior_reinforced_at"] = ts
	case "contradicted":
		prior.Metadata["prior_contradicted_at"] = ts
	}
}

func containsContradiction(priorText, incomingText string) bool {
	incoming := strings.ToLower(incomingText)
	if jaccardSimilarity(priorText, incomingText) < 0.20 {
		return false
	}
	markers := []string{
		"does not want", "doesn't want", "do not want", "don't want",
		"does not prefer", "doesn't prefer", "do not prefer", "don't prefer",
		"not prefer", "no longer", "hates",
	}
	for _, marker := range markers {
		if strings.Contains(incoming, marker) {
			return true
		}
	}
	if strings.Contains(incoming, "avoid") && !containsPositiveAvoidance(incoming) {
		return true
	}
	return false
}

func containsPositiveAvoidance(text string) bool {
	positiveAvoidance := []string{
		"avoid over-engineering",
		"avoid over engineering",
		"avoid overbuilding",
		"avoid unnecessary",
		"avoid needless",
		"avoid premature",
		"avoid excessive",
		"avoid too much",
	}
	for _, phrase := range positiveAvoidance {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func timeNowUTC() time.Time {
	return time.Now().UTC()
}
