package inference

import (
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

func New(cfg Config, log *zap.Logger) (Provider, error) {
	cfg = applyDefaults(cfg)

	switch cfg.Provider {
	case "anthropic":
		return newAnthropicProvider(cfg, log), nil

	case "openai":
		return newOpenAIProvider(cfg, log), nil

	case "gemini":
		return newGeminiProvider(cfg, log), nil

	case "ollama":
		ollamaCfg := cfg
		if ollamaCfg.BaseURL == "" {
			ollamaCfg.BaseURL = "http://localhost:11434"
		} else {
			ollamaCfg.BaseURL = strings.TrimRight(ollamaCfg.BaseURL, "/")
		}
		ollamaCfg.BaseURL = ensureV1Base(ollamaCfg.BaseURL)
		ollamaCfg.APIKey = "ollama"
		return newOpenAIProvider(ollamaCfg, log), nil

	case "lmstudio":
		lmCfg := cfg
		if lmCfg.BaseURL == "" {
			lmCfg.BaseURL = "http://localhost:1234"
		}
		lmCfg.BaseURL = ensureV1Base(lmCfg.BaseURL)
		lmCfg.APIKey = "lmstudio"
		return newOpenAIProvider(lmCfg, log), nil

	default:
		return nil, ErrUnknownProvider{Name: cfg.Provider}
	}
}

func Validate(cfg Config) error {
	cfg = applyDefaults(cfg)
	switch cfg.Provider {
	case "anthropic", "openai", "gemini":
		if cfg.APIKey == "" {
			return fmt.Errorf("inference: %s requires NEXUS_INFERENCE_API_KEY", cfg.Provider)
		}
	case "ollama", "lmstudio":
	default:
		return ErrUnknownProvider{Name: cfg.Provider}
	}
	return nil
}

func applyDefaults(cfg Config) Config {
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.Provider == "" {
		cfg.Provider = "ollama"
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.2
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 2048
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.Model == "" {
		switch cfg.Provider {
		case "anthropic":
			cfg.Model = "claude-haiku-4-5-20251001"
		case "openai":
			cfg.Model = "gpt-4o-mini"
		case "gemini":
			cfg.Model = "gemini-1.5-flash"
		case "ollama", "lmstudio":
			cfg.Model = "llama3.2"
		}
	}
	if cfg.BaseURL == "" {
		switch cfg.Provider {
		case "anthropic":
			cfg.BaseURL = "https://api.anthropic.com"
		case "openai":
			cfg.BaseURL = "https://api.openai.com/v1"
		case "gemini":
			cfg.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
		}
	}
	return cfg
}

func ensureV1Base(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL
	}
	return baseURL + "/v1"
}

func joinURL(baseURL, path string) string {
	return strings.TrimRight(baseURL, "/") + path
}

func anthropicMessagesURL(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/messages"
	}
	return baseURL + "/v1/messages"
}
