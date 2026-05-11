package memory

import "time"

type Archetype struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Keywords      []string  `json:"keywords"`
	StablePriors  []string  `json:"stable_priors"`
	DynamicPriors []string  `json:"dynamic_priors"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type PersonaStatus string

const (
	PersonaActive    PersonaStatus = "active"
	PersonaDormant   PersonaStatus = "dormant"
	PersonaEmerging  PersonaStatus = "emerging"
	PersonaColdStart PersonaStatus = "cold_start"
)

type Persona struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	ArchetypeID     string         `json:"archetype_id,omitempty"`
	Centroid        []float32      `json:"centroid,omitempty"`
	ActivationScore float64        `json:"activation_score"`
	SessionCount    int            `json:"session_count"`
	LastActive      *time.Time     `json:"last_active,omitempty"`
	Status          PersonaStatus  `json:"status"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type Session struct {
	ID        string         `json:"id"`
	PersonaID string         `json:"persona_id,omitempty"`
	Summary   string         `json:"summary"`
	Tool      string         `json:"tool,omitempty"`
	RawPath   string         `json:"raw_path,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type Project struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Path       string         `json:"path"`
	Lang       string         `json:"lang,omitempty"`
	Frameworks []string       `json:"frameworks,omitempty"`
	Active     bool           `json:"active"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}
