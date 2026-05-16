package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

type anthropicProvider struct {
	cfg    Config
	client *http.Client
	log    *zap.Logger
}

func newAnthropicProvider(cfg Config, log *zap.Logger) Provider {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &anthropicProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
		log:    log,
	}
}

func (p *anthropicProvider) Name() string    { return "anthropic" }
func (p *anthropicProvider) ModelID() string { return p.cfg.Model }

func (p *anthropicProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.cfg.MaxTokens
	}
	temperature := req.Temperature
	if temperature == 0 {
		temperature = p.cfg.Temperature
	}

	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	body := struct {
		Model       string  `json:"model"`
		MaxTokens   int     `json:"max_tokens"`
		Temperature float64 `json:"temperature,omitempty"`
		System      string  `json:"system,omitempty"`
		Messages    []msg   `json:"messages"`
	}{
		Model:       p.cfg.Model,
		MaxTokens:   maxTokens,
		Temperature: temperature,
		System:      req.SystemPrompt,
		Messages:    []msg{{Role: "user", Content: req.UserMessage}},
	}

	b, err := json.Marshal(body)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("anthropic: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		anthropicMessagesURL(p.cfg.BaseURL), bytes.NewReader(b))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("anthropic: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("anthropic: http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return CompletionResponse{}, fmt.Errorf("anthropic: decode: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if result.Error != nil {
			msg = result.Error.Message
		}
		return CompletionResponse{}, fmt.Errorf("anthropic: %s", msg)
	}
	if len(result.Content) == 0 {
		return CompletionResponse{}, fmt.Errorf("anthropic: empty content in response")
	}

	return CompletionResponse{
		Content:      result.Content[0].Text,
		InputTokens:  result.Usage.InputTokens,
		OutputTokens: result.Usage.OutputTokens,
		Model:        result.Model,
		Provider:     "anthropic",
	}, nil
}
