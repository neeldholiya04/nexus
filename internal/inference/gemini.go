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

type geminiProvider struct {
	cfg    Config
	client *http.Client
	log    *zap.Logger
}

func newGeminiProvider(cfg Config, log *zap.Logger) Provider {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &geminiProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
		log:    log,
	}
}

func (p *geminiProvider) Name() string    { return "gemini" }
func (p *geminiProvider) ModelID() string { return p.cfg.Model }

func (p *geminiProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.cfg.MaxTokens
	}
	temperature := req.Temperature
	if temperature == 0 {
		temperature = p.cfg.Temperature
	}

	type part struct {
		Text string `json:"text"`
	}
	type content struct {
		Role  string `json:"role"`
		Parts []part `json:"parts"`
	}
	body := struct {
		SystemInstruction *struct {
			Parts []part `json:"parts"`
		} `json:"systemInstruction,omitempty"`
		Contents         []content `json:"contents"`
		GenerationConfig struct {
			MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
			Temperature     float64 `json:"temperature,omitempty"`
		} `json:"generationConfig"`
	}{
		Contents: []content{
			{Role: "user", Parts: []part{{Text: req.UserMessage}}},
		},
	}
	body.GenerationConfig.MaxOutputTokens = maxTokens
	body.GenerationConfig.Temperature = temperature
	if req.SystemPrompt != "" {
		body.SystemInstruction = &struct {
			Parts []part `json:"parts"`
		}{Parts: []part{{Text: req.SystemPrompt}}}
	}

	b, err := json.Marshal(body)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("gemini: encode request: %w", err)
	}
	url := joinURL(p.cfg.BaseURL, fmt.Sprintf("/models/%s:generateContent?key=%s", p.cfg.Model, p.cfg.APIKey))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("gemini: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("gemini: http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []part `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return CompletionResponse{}, fmt.Errorf("gemini: decode: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if result.Error != nil {
			msg = result.Error.Message
		}
		return CompletionResponse{}, fmt.Errorf("gemini: %s", msg)
	}
	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return CompletionResponse{}, fmt.Errorf("gemini: empty response")
	}

	return CompletionResponse{
		Content:      result.Candidates[0].Content.Parts[0].Text,
		InputTokens:  result.UsageMetadata.PromptTokenCount,
		OutputTokens: result.UsageMetadata.CandidatesTokenCount,
		Model:        p.cfg.Model,
		Provider:     "gemini",
	}, nil
}
