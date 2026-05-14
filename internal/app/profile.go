package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/neeldholiya04/nexus/internal/memory"
)

type InitProfileInput struct {
	Name                   string
	Timezone               string
	ArchetypeIDs           []string
	PrimaryLanguage        string
	ExplanationDepth       string
	CurrentProject         string
	CurrentProjectPath     string
	ArchitecturePreference string
	CurrentFocus           string
}

type InitProfileResult struct {
	ArchetypesSeeded int              `json:"archetypes_seeded"`
	PersonasSeeded   int              `json:"personas_seeded"`
	MemoriesInserted int              `json:"memories_inserted"`
	MemoriesUpdated  int              `json:"memories_updated"`
	Personas         []memory.Persona `json:"personas"`
}

type PersonaResolution struct {
	Mode        string             `json:"mode"`
	Primary     string             `json:"primary,omitempty"`
	Secondary   []string           `json:"secondary,omitempty"`
	Scores      map[string]float64 `json:"scores,omitempty"`
	Personas    []memory.Persona   `json:"personas,omitempty"`
	Archetypes  []memory.Archetype `json:"archetypes,omitempty"`
	Explanation string             `json:"explanation,omitempty"`
}

type WorkflowProfile struct {
	Persona PersonaResolution `json:"persona"`
	Stable  []ContextItem     `json:"stable"`
	Dynamic []ContextItem     `json:"dynamic"`
	Text    string            `json:"text"`
}

func (s *MemoryService) InitializeProfile(ctx context.Context, in InitProfileInput) (*InitProfileResult, error) {
	archetypes := BuiltinArchetypes()
	archetypeByID := make(map[string]memory.Archetype, len(archetypes))
	for _, archetype := range archetypes {
		archetypeByID[archetype.ID] = archetype
		a := archetype
		if err := s.store.UpsertArchetype(ctx, &a); err != nil {
			return nil, err
		}
	}

	selected := normalizeArchetypeIDs(in.ArchetypeIDs)
	if len(selected) == 0 {
		selected = []string{"sre_infra", "startup_builder", "fullstack_dev"}
	}

	result := &InitProfileResult{ArchetypesSeeded: len(archetypes)}
	now := time.Now().UTC()
	for _, id := range selected {
		archetype, ok := archetypeByID[id]
		if !ok {
			return nil, fmt.Errorf("unknown archetype %q", id)
		}
		persona := memory.Persona{
			ID:              id,
			Name:            archetype.Name,
			ArchetypeID:     archetype.ID,
			Centroid:        s.archetypeCentroid(ctx, archetype),
			ActivationScore: 0.50,
			Status:          memory.PersonaActive,
			LastActive:      &now,
			Metadata: map[string]any{
				"initialized_by": "nexus init",
				"profile_name":   strings.TrimSpace(in.Name),
				"timezone":       strings.TrimSpace(in.Timezone),
				"keywords":       archetype.Keywords,
			},
		}
		if err := s.store.UpsertPersona(ctx, &persona); err != nil {
			return nil, err
		}
		result.PersonasSeeded++
		result.Personas = append(result.Personas, persona)

		for _, prior := range archetype.StablePriors {
			inserted, updated, err := s.seedProfileMemory(ctx, prior, memory.CategoryPreference, memory.LayerStable, persona.ID, 0.50, []string{"archetype", archetype.ID})
			if err != nil {
				return nil, err
			}
			result.addMemoryCounts(inserted, updated)
		}
		for _, prior := range archetype.DynamicPriors {
			inserted, updated, err := s.seedProfileMemory(ctx, prior, memory.CategoryWorkflow, memory.LayerDynamic, persona.ID, 0.50, []string{"archetype", archetype.ID})
			if err != nil {
				return nil, err
			}
			result.addMemoryCounts(inserted, updated)
		}
	}

	for _, seed := range bootstrapMemories(in) {
		inserted, updated, err := s.seedProfileMemory(ctx, seed.Content, seed.Category, seed.Layer, seed.PersonaID, seed.Confidence, seed.Tags)
		if err != nil {
			return nil, err
		}
		result.addMemoryCounts(inserted, updated)
	}
	if strings.TrimSpace(in.CurrentProjectPath) != "" {
		project := memory.Project{
			ID:     s.newID(),
			Name:   strings.TrimSpace(in.CurrentProject),
			Path:   strings.TrimSpace(in.CurrentProjectPath),
			Active: true,
			Metadata: map[string]any{
				"seeded_by": "nexus init",
			},
		}
		if project.Name == "" {
			project.Name = projectNameFromPath(project.Path)
		}
		if err := s.store.UpsertProject(ctx, &project); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *MemoryService) ResolvePersona(ctx context.Context, intent string) (PersonaResolution, error) {
	intent = strings.TrimSpace(intent)
	personas, err := s.store.ListPersonas(ctx)
	if err != nil {
		return PersonaResolution{}, err
	}
	archetypes, err := s.store.ListArchetypes(ctx)
	if err != nil {
		return PersonaResolution{}, err
	}
	resolution := PersonaResolution{
		Mode:       "broad",
		Scores:     make(map[string]float64),
		Personas:   derefPersonas(personas),
		Archetypes: derefArchetypes(archetypes),
	}
	if intent == "" || len(personas) == 0 {
		resolution.Explanation = "No intent or personas available; using broad mode."
		return resolution, nil
	}

	results, err := s.Search(ctx, intent, SearchOptions{Limit: 30})
	if err != nil {
		return resolution, err
	}
	archetypeKeywords := archetypeKeywordIndex(archetypes)
	intentWords := wordSet(intent)
	intentEmbedding := s.intentEmbedding(ctx, intent)

	for _, persona := range personas {
		memoryScore := 0.0
		for _, result := range results {
			m := result.Memory
			if m == nil {
				continue
			}
			if m.PersonaID() == persona.ID && result.FinalScore > memoryScore {
				memoryScore = result.FinalScore
			}
		}
		keywordScore := 0.0
		for _, keyword := range archetypeKeywords[persona.ArchetypeID] {
			if _, ok := intentWords[strings.ToLower(keyword)]; ok {
				keywordScore += 0.20
			}
		}
		if keywordScore > 0.60 {
			keywordScore = 0.60
		}
		centroidScore := 0.0
		if len(intentEmbedding) > 0 && len(persona.Centroid) > 0 {
			centroidScore = memory.CosineSimilarity(intentEmbedding, persona.Centroid)
		}
		score := weightedPersonaScore(centroidScore, memoryScore, keywordScore)
		if score > 1.0 {
			score = 1.0
		}
		resolution.Scores[persona.ID] = score
	}

	ordered := sortedScores(resolution.Scores)
	if len(ordered) == 0 || ordered[0].Score < 0.30 {
		resolution.Mode = "cold_start"
		resolution.Explanation = "No persona crossed the cold-start threshold."
		return resolution, nil
	}
	resolution.Primary = ordered[0].ID
	switch {
	case ordered[0].Score >= 0.70:
		resolution.Mode = "primary"
		resolution.Explanation = "One persona clearly matched the intent via centroid, memory, or archetype signals."
	case ordered[0].Score >= 0.40:
		resolution.Mode = "broad"
		resolution.Explanation = "Persona signal is useful but below primary threshold."
	default:
		resolution.Mode = "cold_start"
		resolution.Explanation = "Persona signal is below broad threshold."
	}
	for _, item := range ordered[1:] {
		if item.Score >= 0.45 {
			resolution.Secondary = append(resolution.Secondary, item.ID)
		}
	}
	if len(resolution.Secondary) > 0 && resolution.Mode == "primary" {
		resolution.Mode = "blend"
		resolution.Explanation = "Multiple personas matched the intent."
	}
	return resolution, nil
}

func (s *MemoryService) archetypeCentroid(ctx context.Context, archetype memory.Archetype) []float32 {
	if s.embedder == nil {
		return nil
	}
	texts := append([]string{}, archetype.StablePriors...)
	texts = append(texts, archetype.DynamicPriors...)
	if len(texts) == 0 {
		return nil
	}
	vecs, err := s.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		s.log.Warn("profile: archetype centroid embedding failed")
		return nil
	}
	return meanEmbedding(vecs)
}

func (s *MemoryService) intentEmbedding(ctx context.Context, intent string) []float32 {
	if s.embedder == nil {
		return nil
	}
	vec, err := s.embedder.Embed(ctx, firstWords(intent, 50))
	if err != nil {
		s.log.Warn("profile: intent embedding failed")
		return nil
	}
	return vec
}

func weightedPersonaScore(centroidScore, memoryScore, keywordScore float64) float64 {
	if centroidScore > 0 {
		return 0.70*centroidScore + 0.20*memoryScore + 0.10*keywordScore
	}
	score := memoryScore
	if keywordScore > score {
		score = keywordScore
	}
	return score
}

func meanEmbedding(vecs [][]float32) []float32 {
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil
	}
	dims := len(vecs[0])
	out := make([]float32, dims)
	count := 0
	for _, vec := range vecs {
		if len(vec) != dims {
			continue
		}
		for i, v := range vec {
			out[i] += v
		}
		count++
	}
	if count == 0 {
		return nil
	}
	for i := range out {
		out[i] /= float32(count)
	}
	return out
}

func firstWords(text string, n int) string {
	words := strings.Fields(text)
	if len(words) <= n {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:n], " ")
}

func (s *MemoryService) WorkflowProfile(ctx context.Context, personaID string) (*WorkflowProfile, error) {
	personaID = strings.TrimSpace(personaID)
	if personaID == "" {
		return nil, fmt.Errorf("workflow profile: persona id cannot be empty")
	}
	all, err := s.store.List(ctx, memory.ListOptions{Limit: 500})
	if err != nil {
		return nil, err
	}
	profile := &WorkflowProfile{
		Persona: PersonaResolution{Mode: "primary", Primary: personaID},
	}
	for _, m := range all {
		if m.PersonaID() != personaID {
			continue
		}
		item, _, ok := contextItem(m, 0, m.Layer(), 240)
		if !ok {
			continue
		}
		if item.Layer == memory.LayerStable {
			profile.Stable = append(profile.Stable, item)
		} else {
			profile.Dynamic = append(profile.Dynamic, item)
		}
	}
	profile.Text = formatWorkflowProfileText(personaID, profile.Stable, profile.Dynamic)
	return profile, nil
}

func (s *MemoryService) seedProfileMemory(ctx context.Context, content string, category memory.Category, layer memory.Layer, personaID string, confidence float64, tags []string) (bool, bool, error) {
	result, err := s.AddMemory(ctx, AddMemoryInput{
		Content:    content,
		Category:   category,
		Layer:      layer,
		PersonaID:  personaID,
		Source:     memory.SourceInferred,
		Confidence: confidence,
		Tags:       tags,
		Metadata: map[string]any{
			"seeded_by": "v0_profile",
		},
	})
	if err != nil {
		return false, false, err
	}
	return result.Inserted, result.Updated, nil
}

func BuiltinArchetypes() []memory.Archetype {
	return []memory.Archetype{
		{
			ID:          "sre_infra",
			Name:        "SRE / Infra",
			Description: "Infrastructure, reliability, Go, Kubernetes, observability, runbooks, and latency-first debugging.",
			Keywords:    []string{"sre", "infra", "go", "kubernetes", "datadog", "latency", "runbook", "oncall", "terraform", "cluster", "production", "incident", "rollback", "observability", "pod"},
			StablePriors: []string{
				"Likely values explicit error handling, operational clarity, and runbook-style explanations.",
				"Likely cares about latency, reliability, observability, and blast-radius control.",
			},
			DynamicPriors: []string{
				"Active infrastructure work should surface project state, current incident context, and deployment constraints.",
			},
		},
		{
			ID:          "cs_student",
			Name:        "CS Student",
			Description: "Academic learning, papers, from-scratch implementation, exam prep, and conceptual explanations.",
			Keywords:    []string{"cs", "student", "paper", "algorithm", "exam", "concept", "implementation"},
			StablePriors: []string{
				"Likely benefits from first-principles explanations and clear conceptual framing.",
			},
			DynamicPriors: []string{
				"Learning sessions should preserve topic, objective, gaps, and next practice problems.",
			},
		},
		{
			ID:          "startup_builder",
			Name:        "Startup Builder",
			Description: "Lean MVPs, user feedback, GTM, metrics, fundraising, and product intuition.",
			Keywords:    []string{"startup", "mvp", "gtm", "growth", "metrics", "fundraising", "product", "launch", "pricing", "retention", "customer", "validation"},
			StablePriors: []string{
				"Likely prefers shipping narrow useful MVPs before overbuilding infrastructure.",
				"Likely values product clarity, fast feedback loops, and practical tradeoffs.",
			},
			DynamicPriors: []string{
				"Startup work should preserve current hypothesis, target user, next build step, and validation signal.",
			},
		},
		{
			ID:          "fullstack_dev",
			Name:        "Full-Stack Dev",
			Description: "React, APIs, SQL, TypeScript, CI/CD, review, and deployment workflows.",
			Keywords:    []string{"react", "typescript", "api", "sql", "frontend", "backend", "deploy", "vite", "rest", "endpoint", "route", "schema", "migration", "forms", "page", "client"},
			StablePriors: []string{
				"Likely wants implementation details to match the existing application architecture.",
				"Likely values clean API contracts, predictable UI state, and maintainable module boundaries.",
			},
			DynamicPriors: []string{
				"Full-stack work should preserve route, API, schema, and deployment context.",
			},
		},
		{
			ID:          "ml_ai_engineer",
			Name:        "ML / AI Engineer",
			Description: "Model evaluation, experiments, datasets, prompt systems, GPU optimization, and research.",
			Keywords:    []string{"ml", "ai", "model", "dataset", "eval", "prompt", "gpu", "rag"},
			StablePriors: []string{
				"Likely values measurable model behavior, evals, and clear assumptions.",
			},
			DynamicPriors: []string{
				"AI engineering work should preserve model, prompt, eval criteria, and observed failures.",
			},
		},
		{
			ID:          "product_manager",
			Name:        "Product Manager",
			Description: "PRDs, stakeholders, OKRs, roadmap prioritization, user stories, and metrics framing.",
			Keywords:    []string{"prd", "roadmap", "okr", "stakeholder", "prioritization", "user", "metrics"},
			StablePriors: []string{
				"Likely benefits from crisp tradeoff framing, user impact, and decision records.",
			},
			DynamicPriors: []string{
				"Product work should preserve user segment, decision, metric, and open risk.",
			},
		},
	}
}

type bootstrapMemory struct {
	Content    string
	Category   memory.Category
	Layer      memory.Layer
	PersonaID  string
	Confidence float64
	Tags       []string
}

func bootstrapMemories(in InitProfileInput) []bootstrapMemory {
	var out []bootstrapMemory
	if text := strings.TrimSpace(in.PrimaryLanguage); text != "" {
		out = append(out, bootstrapMemory{
			Content:    "Primary coding language: " + text,
			Category:   memory.CategoryCodingStyle,
			Layer:      memory.LayerStable,
			Confidence: 0.90,
			Tags:       []string{"bootstrap", "coding"},
		})
	}
	if text := strings.TrimSpace(in.ExplanationDepth); text != "" {
		out = append(out, bootstrapMemory{
			Content:    "Preferred explanation depth: " + text,
			Category:   memory.CategoryPreference,
			Layer:      memory.LayerStable,
			Confidence: 0.90,
			Tags:       []string{"bootstrap", "communication"},
		})
	}
	if text := strings.TrimSpace(in.ArchitecturePreference); text != "" {
		out = append(out, bootstrapMemory{
			Content:    "Architecture preference: " + text,
			Category:   memory.CategoryPreference,
			Layer:      memory.LayerStable,
			Confidence: 0.90,
			Tags:       []string{"bootstrap", "architecture"},
		})
	}
	if text := strings.TrimSpace(in.CurrentProject); text != "" {
		if path := strings.TrimSpace(in.CurrentProjectPath); path != "" {
			text = fmt.Sprintf("Project: %s at %s", text, path)
		}
		out = append(out, bootstrapMemory{
			Content:    text,
			Category:   memory.CategoryProject,
			Layer:      memory.LayerDynamic,
			PersonaID:  "startup_builder",
			Confidence: 1.0,
			Tags:       []string{"bootstrap", "project"},
		})
	}
	if text := strings.TrimSpace(in.CurrentFocus); text != "" {
		out = append(out, bootstrapMemory{
			Content:    "Current focus: " + text,
			Category:   memory.CategoryWorkflow,
			Layer:      memory.LayerDynamic,
			PersonaID:  "startup_builder",
			Confidence: 1.0,
			Tags:       []string{"bootstrap", "focus"},
		})
	}
	return out
}

func normalizeArchetypeIDs(ids []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, id := range ids {
		for _, part := range strings.Split(id, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			part = strings.ReplaceAll(part, "-", "_")
			if part == "" {
				continue
			}
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			out = append(out, part)
		}
	}
	return out
}

func (r *InitProfileResult) addMemoryCounts(inserted, updated bool) {
	if inserted {
		r.MemoriesInserted++
	}
	if updated {
		r.MemoriesUpdated++
	}
}

type scorePair struct {
	ID    string
	Score float64
}

func sortedScores(scores map[string]float64) []scorePair {
	out := make([]scorePair, 0, len(scores))
	for id, score := range scores {
		out = append(out, scorePair{ID: id, Score: score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].ID < out[j].ID
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func archetypeKeywordIndex(archetypes []*memory.Archetype) map[string][]string {
	out := make(map[string][]string, len(archetypes))
	for _, archetype := range archetypes {
		out[archetype.ID] = archetype.Keywords
	}
	return out
}

func derefPersonas(in []*memory.Persona) []memory.Persona {
	out := make([]memory.Persona, 0, len(in))
	for _, item := range in {
		out = append(out, *item)
	}
	return out
}

func derefArchetypes(in []*memory.Archetype) []memory.Archetype {
	out := make([]memory.Archetype, 0, len(in))
	for _, item := range in {
		out = append(out, *item)
	}
	return out
}

func formatWorkflowProfileText(personaID string, stable, dynamic []ContextItem) string {
	var sb strings.Builder
	sb.WriteString("Nexus Workflow Profile\n")
	sb.WriteString(fmt.Sprintf("Persona: %s\n\n", personaID))
	writeContextSection(&sb, "Stable Profile", stable)
	sb.WriteString("\n")
	writeContextSection(&sb, "Dynamic Profile", dynamic)
	return strings.TrimSpace(sb.String())
}

func projectNameFromPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}
