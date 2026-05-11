package memory

import (
	"math"
	"strings"
	"time"
)

type Category string

const (
	CategoryFact        Category = "FACT"
	CategoryPreference  Category = "PREFERENCE"
	CategoryWorkflow    Category = "WORKFLOW"
	CategoryProject     Category = "PROJECT"
	CategoryCodingStyle Category = "CODING_STYLE"
	CategoryInferred    Category = "INFERRED"
)

var AllCategories = []Category{
	CategoryFact,
	CategoryPreference,
	CategoryWorkflow,
	CategoryProject,
	CategoryCodingStyle,
	CategoryInferred,
}

func (c Category) Valid() bool {
	for _, cat := range AllCategories {
		if c == cat {
			return true
		}
	}
	return false
}

type Layer string

const (
	LayerStable  Layer = "stable"
	LayerDynamic Layer = "dynamic"
)

var AllLayers = []Layer{LayerStable, LayerDynamic}

func ParseLayer(value string) (Layer, bool) {
	layer := Layer(strings.ToLower(strings.TrimSpace(value)))
	return layer, layer.Valid()
}

func (l Layer) Valid() bool {
	for _, layer := range AllLayers {
		if l == layer {
			return true
		}
	}
	return false
}

func DefaultLayer(category Category) Layer {
	switch category {
	case CategoryProject, CategoryInferred:
		return LayerDynamic
	default:
		return LayerStable
	}
}

type Source string

const (
	SourceManual    Source = "manual"
	SourceInferred  Source = "inferred"
	SourceIngestion Source = "ingestion"
	SourceMCP       Source = "mcp"
	SourceBrowser   Source = "browser"
)

const (
	ConfidenceDecayHalfLifeDays = 60.0
	ReinforcementPerAccess      = 0.01
	MaxEffectiveConfidence      = 1.0
	MinEffectiveConfidence      = 0.10
)

type Memory struct {
	ID         string   `json:"id"`
	Category   Category `json:"category"`
	Content    string   `json:"content"`
	Source     Source   `json:"source"`
	Confidence float64  `json:"confidence"`

	Tags         []string       `json:"tags"`
	Embedding    []float32      `json:"embedding,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	LastAccessed *time.Time     `json:"last_accessed,omitempty"`
	AccessCount  int            `json:"access_count"`
	ValidFrom    *time.Time     `json:"valid_from,omitempty"`
	ValidUntil   *time.Time     `json:"valid_until,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

func (m *Memory) Layer() Layer {
	if m == nil {
		return LayerStable
	}
	if layer, ok := metadataLayer(m.Metadata); ok {
		return layer
	}
	return DefaultLayer(m.Category)
}

func (m *Memory) SetLayer(layer Layer) {
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}
	if !layer.Valid() {
		layer = DefaultLayer(m.Category)
	}
	m.Metadata["layer"] = string(layer)
}

func (m *Memory) PersonaID() string {
	if m == nil || m.Metadata == nil {
		return ""
	}
	value, ok := m.Metadata["persona_id"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func (m *Memory) SetPersonaID(personaID string) {
	personaID = strings.TrimSpace(personaID)
	if personaID == "" {
		return
	}
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}
	m.Metadata["persona_id"] = personaID
}

func metadataLayer(metadata map[string]any) (Layer, bool) {
	if metadata == nil {
		return "", false
	}
	value, ok := metadata["layer"].(string)
	if !ok {
		return "", false
	}
	return ParseLayer(value)
}

func (m *Memory) EffectiveConfidence(now time.Time) float64 {
	reinforced := m.Confidence + float64(m.AccessCount)*ReinforcementPerAccess
	if reinforced > MaxEffectiveConfidence {
		reinforced = MaxEffectiveConfidence
	}
	reference := m.CreatedAt
	if m.LastAccessed != nil {
		reference = *m.LastAccessed
	}

	ageDays := now.Sub(reference).Hours() / 24.0
	if ageDays < 0 {
		ageDays = 0
	}
	decayFactor := math.Pow(2, -ageDays/ConfidenceDecayHalfLifeDays)

	effective := reinforced * decayFactor
	if effective < MinEffectiveConfidence {
		effective = MinEffectiveConfidence
	}

	return effective
}

func DefaultConfidence(source Source) float64 {
	switch source {
	case SourceManual:
		return 1.0
	case SourceMCP:
		return 0.95
	case SourceIngestion:
		return 0.85
	case SourceBrowser:
		return 0.80
	case SourceInferred:
		return 0.60
	default:
		return 0.75
	}
}

func (m *Memory) IsValid() bool {
	now := time.Now()
	if m.ValidFrom != nil && now.Before(*m.ValidFrom) {
		return false
	}
	if m.ValidUntil != nil && now.After(*m.ValidUntil) {
		return false
	}
	return true
}

func (m *Memory) HasEmbedding() bool {
	return len(m.Embedding) > 0
}

type RetrievalResult struct {
	Memory          *Memory `json:"memory"`
	SemanticScore   float64 `json:"semantic_score"`
	RecencyScore    float64 `json:"recency_score"`
	CategoryScore   float64 `json:"category_score"`
	ConfidenceScore float64 `json:"confidence_score"`
	FinalScore      float64 `json:"final_score"`
}

type Query struct {
	Text           string     `json:"text"`
	Categories     []Category `json:"categories,omitempty"`
	Tags           []string   `json:"tags,omitempty"`
	Limit          int        `json:"limit"`
	MinConfidence  float64    `json:"min_confidence"`
	IncludeExpired bool       `json:"include_expired"`
}

func DefaultQuery(text string) Query {
	return Query{
		Text:          text,
		Limit:         10,
		MinConfidence: 0.3,
	}
}

type ListOptions struct {
	Categories     []Category
	Tags           []string
	MinConfidence  float64
	IncludeExpired bool
	Limit          int
	Offset         int
}

type StoreStats struct {
	Total      int              `json:"total"`
	Embedded   int              `json:"embedded"`
	ByCategory map[Category]int `json:"by_category"`
}
