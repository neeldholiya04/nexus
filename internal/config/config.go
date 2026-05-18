package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

var (
	defaultAppEnvironment = "development"
	defaultAppDryRun      = "true"
)

type Config struct {
	App AppConfig `mapstructure:"app"`

	Storage StorageConfig `mapstructure:"storage"`

	Ollama OllamaConfig `mapstructure:"ollama"`

	MCP MCPConfig `mapstructure:"mcp"`

	Retrieval RetrievalConfig `mapstructure:"retrieval"`

	Log LogConfig `mapstructure:"log"`

	Inference InferenceConfig `mapstructure:"inference"`
}

type AppConfig struct {
	Name        string `mapstructure:"name"`
	Version     string `mapstructure:"version"`
	Environment string `mapstructure:"environment"`
	DryRun      bool   `mapstructure:"dry_run"`
}

type StorageConfig struct {
	DataDir       string `mapstructure:"data_dir"`
	DBPath        string `mapstructure:"db_path"`
	MigrationsDir string `mapstructure:"migrations_dir"`
	MaxOpenConns  int    `mapstructure:"max_open_conns"`
	BusyTimeoutMs int    `mapstructure:"busy_timeout_ms"`
}

type OllamaConfig struct {
	BaseURL             string        `mapstructure:"base_url"`
	EmbeddingModel      string        `mapstructure:"embedding_model"`
	EmbeddingDimensions int           `mapstructure:"embedding_dimensions"`
	Timeout             time.Duration `mapstructure:"timeout"`
}

type InferenceConfig struct {
	Provider    string        `mapstructure:"provider"`
	Model       string        `mapstructure:"model"`
	BaseURL     string        `mapstructure:"base_url"`
	APIKey      string        `mapstructure:"api_key"`
	MaxTokens   int           `mapstructure:"max_tokens"`
	Temperature float64       `mapstructure:"temperature"`
	Timeout     time.Duration `mapstructure:"timeout"`
}

type MCPConfig struct {
	Transport     string `mapstructure:"transport"`
	SSEAddr       string `mapstructure:"sse_addr"`
	ServerName    string `mapstructure:"server_name"`
	ServerVersion string `mapstructure:"server_version"`
}

type RetrievalConfig struct {
	SemanticWeight         float64 `mapstructure:"semantic_weight"`
	RecencyWeight          float64 `mapstructure:"recency_weight"`
	CategoryWeight         float64 `mapstructure:"category_weight"`
	ConfidenceWeight       float64 `mapstructure:"confidence_weight"`
	RecencyHalfLifeDays    float64 `mapstructure:"recency_half_life_days"`
	DefaultLimit           int     `mapstructure:"default_limit"`
	MinConfidenceThreshold float64 `mapstructure:"min_confidence_threshold"`
}

type LogConfig struct {
	Level      string `mapstructure:"level"`
	Format     string `mapstructure:"format"`
	OutputPath string `mapstructure:"output_path"`
}

func Load() (*Config, error) {
	v := viper.New()
	setDefaults(v)

	v.SetEnvPrefix("NEXUS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	bindEnvs(v)

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal failed: %w", err)
	}

	if cfg.Storage.DBPath == "" {
		cfg.Storage.DBPath = cfg.Storage.DataDir + "/nexus.db"
	}

	if cfg.Log.Format == "" {
		if cfg.App.Environment == "production" {
			cfg.Log.Format = "json"
		} else {
			cfg.Log.Format = "console"
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: validation failed: %w", err)
	}

	return cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("app.name", "nexus")
	v.SetDefault("app.version", "0.1.0")
	v.SetDefault("app.environment", defaultAppEnvironment)
	v.SetDefault("app.dry_run", parseDefaultBool(defaultAppDryRun, true))

	// Storage
	v.SetDefault("storage.data_dir", "${HOME}/.nexus")
	v.SetDefault("storage.db_path", "")
	v.SetDefault("storage.max_open_conns", 1)
	v.SetDefault("storage.busy_timeout_ms", 5000)

	// Ollama
	v.SetDefault("ollama.base_url", "http://localhost:11434")
	v.SetDefault("ollama.embedding_model", "nomic-embed-text")
	v.SetDefault("ollama.embedding_dimensions", 768)
	v.SetDefault("ollama.timeout", 30*time.Second)

	// MCP
	v.SetDefault("mcp.transport", "stdio")
	v.SetDefault("mcp.sse_addr", "127.0.0.1:7798")
	v.SetDefault("mcp.server_name", "nexus")
	v.SetDefault("mcp.server_version", "0.1.0")

	// Retrieval weights — must sum to 1.0
	v.SetDefault("retrieval.semantic_weight", 0.45)
	v.SetDefault("retrieval.recency_weight", 0.25)
	v.SetDefault("retrieval.category_weight", 0.20)
	v.SetDefault("retrieval.confidence_weight", 0.10)
	v.SetDefault("retrieval.recency_half_life_days", 30.0)
	v.SetDefault("retrieval.default_limit", 10)
	v.SetDefault("retrieval.min_confidence_threshold", 0.5)

	// Log
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "")
	v.SetDefault("log.output_path", "stderr")

	//inference
	v.SetDefault("inference.provider", "ollama")
	v.SetDefault("inference.max_tokens", 2048)
	v.SetDefault("inference.temperature", 0.2)
	v.SetDefault("inference.timeout", 60*time.Second)
}

func parseDefaultBool(value string, fallback bool) bool {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func bindEnvs(v *viper.Viper) {
	_ = v.BindEnv("app.environment", "NEXUS_APP_ENVIRONMENT")
	_ = v.BindEnv("app.dry_run", "NEXUS_APP_DRY_RUN")
	_ = v.BindEnv("storage.data_dir", "NEXUS_STORAGE_DATA_DIR")
	_ = v.BindEnv("storage.db_path", "NEXUS_STORAGE_DB_PATH")
	_ = v.BindEnv("ollama.base_url", "NEXUS_OLLAMA_BASE_URL")
	_ = v.BindEnv("ollama.embedding_model", "NEXUS_OLLAMA_EMBEDDING_MODEL")
	_ = v.BindEnv("mcp.transport", "NEXUS_MCP_TRANSPORT")
	_ = v.BindEnv("mcp.sse_addr", "NEXUS_MCP_SSE_ADDR")
	_ = v.BindEnv("log.level", "NEXUS_LOG_LEVEL")
	_ = v.BindEnv("log.output_path", "NEXUS_LOG_OUTPUT_PATH")
	_ = v.BindEnv("inference.provider", "NEXUS_INFERENCE_PROVIDER")
	_ = v.BindEnv("inference.model", "NEXUS_INFERENCE_MODEL")
	_ = v.BindEnv("inference.api_key", "NEXUS_INFERENCE_API_KEY")
	_ = v.BindEnv("inference.base_url", "NEXUS_INFERENCE_BASE_URL")
	_ = v.BindEnv("inference.max_tokens", "NEXUS_INFERENCE_MAX_TOKENS")
	_ = v.BindEnv("inference.temperature", "NEXUS_INFERENCE_TEMPERATURE")
	_ = v.BindEnv("inference.timeout", "NEXUS_INFERENCE_TIMEOUT")
}

func (c *Config) validate() error {
	provider := strings.ToLower(strings.TrimSpace(c.Inference.Provider))
	if provider == "" {
		provider = "ollama"
	}
	validProviders := map[string]bool{
		"anthropic": true,
		"openai":    true,
		"gemini":    true,
		"ollama":    true,
		"lmstudio":  true,
	}
	if !validProviders[provider] {
		return fmt.Errorf("inference.provider must be one of anthropic|openai|gemini|ollama|lmstudio, got %q", c.Inference.Provider)
	}
	if !c.App.DryRun {
		switch provider {
		case "anthropic", "openai", "gemini":
			if strings.TrimSpace(c.Inference.APIKey) == "" {
				return fmt.Errorf("NEXUS_INFERENCE_API_KEY is required when app.dry_run=false and inference.provider=%s", provider)
			}
		}
	}

	weightSum := c.Retrieval.SemanticWeight +
		c.Retrieval.RecencyWeight +
		c.Retrieval.CategoryWeight +
		c.Retrieval.ConfidenceWeight

	const epsilon = 0.001
	if weightSum < 1.0-epsilon || weightSum > 1.0+epsilon {
		return fmt.Errorf(
			"retrieval weights must sum to 1.0, got %.4f (semantic=%.2f recency=%.2f category=%.2f confidence=%.2f)",
			weightSum,
			c.Retrieval.SemanticWeight,
			c.Retrieval.RecencyWeight,
			c.Retrieval.CategoryWeight,
			c.Retrieval.ConfidenceWeight,
		)
	}

	if c.MCP.Transport != "stdio" && c.MCP.Transport != "sse" {
		return fmt.Errorf("mcp.transport must be 'stdio' or 'sse', got %q", c.MCP.Transport)
	}

	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Log.Level] {
		return fmt.Errorf("log.level must be one of debug|info|warn|error, got %q", c.Log.Level)
	}

	return nil
}

func (c *Config) IsDevelopment() bool {
	return c.App.Environment == "development"
}

func (c *Config) IsProduction() bool {
	return c.App.Environment == "production"
}
