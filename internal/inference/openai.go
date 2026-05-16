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

type openAIProvider struct {
	cfg    Config
	client *http.Client
	log    *zap.Logger
	name   string
}

func newOpenAIProvider(cfg Config, log *zap.Logger) Provider {
	name := cfg.Provider
	if name == "" {
		name = "openai"
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &openAIProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
		log:    log,
		name:   name,
	}
}

func (p *openAIProvider) Name() string    { return p.name }
func (p *openAIProvider) ModelID() string { return p.cfg.Model }

func (p *openAIProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.cfg.MaxTokens
	}
	temperature := req.Temperature
	if temperature == 0 {
		temperature = p.cfg.Temperature
	}

	type chatMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var messages []chatMsg
	if req.SystemPrompt != "" {
		messages = append(messages, chatMsg{Role: "system", Content: req.SystemPrompt})
	}
	messages = append(messages, chatMsg{Role: "user", Content: req.UserMessage})

	body := struct {
		Model       string    `json:"model"`
		Messages    []chatMsg `json:"messages"`
		MaxTokens   int       `json:"max_tokens,omitempty"`
		Temperature float64   `json:"temperature,omitempty"`
	}{
		Model:       p.cfg.Model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}

	b, err := json.Marshal(body)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("%s: encode request: %w", p.name, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		joinURL(p.cfg.BaseURL, "/chat/completions"), bytes.NewReader(b))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("%s: build request: %w", p.name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("%s: http: %w", p.name, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return CompletionResponse{}, fmt.Errorf("%s: decode: %w", p.name, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if result.Error != nil {
			msg = result.Error.Message
		}
		return CompletionResponse{}, fmt.Errorf("%s: %s", p.name, msg)
	}
	if len(result.Choices) == 0 {
		return CompletionResponse{}, fmt.Errorf("%s: empty choices", p.name)
	}

	return CompletionResponse{
		Content:      result.Choices[0].Message.Content,
		InputTokens:  result.Usage.PromptTokens,
		OutputTokens: result.Usage.CompletionTokens,
		Model:        result.Model,
		Provider:     p.name,
	}, nil
}
