package ollama

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

type Client struct {
	httpClient *http.Client
	baseURL    string
	model      string
	log        *zap.Logger
}

type Config struct {
	BaseURL string
	Model   string
	Timeout time.Duration
}

func New(cfg Config, log *zap.Logger) *Client {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    cfg.BaseURL,
		model:      cfg.Model,
		log:        log,
	}
}

type embedRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

type embedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("ollama: Embed: text cannot be empty")
	}

	results, err := c.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("ollama: Embed: no embeddings returned for input")
	}

	return results[0], nil
}

func (c *Client) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("ollama: EmbedBatch: texts cannot be empty")
	}

	var input any
	if len(texts) == 1 {
		input = texts[0]
	} else {
		input = texts
	}

	reqBody := embedRequest{
		Model: c.model,
		Input: input,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ollama: EmbedBatch: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/embed",
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("ollama: EmbedBatch: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	c.log.Debug("ollama: embedding request",
		zap.String("model", c.model),
		zap.Int("count", len(texts)),
	)

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: EmbedBatch: http: %w", err)
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ollama: EmbedBatch: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var embedResp embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("ollama: EmbedBatch: decode response: %w", err)
	}

	if len(embedResp.Embeddings) != len(texts) {
		return nil, fmt.Errorf(
			"ollama: EmbedBatch: expected %d embeddings, got %d",
			len(texts), len(embedResp.Embeddings),
		)
	}

	c.log.Debug("ollama: embedding complete",
		zap.Int("count", len(texts)),
		zap.Duration("latency", latency),
		zap.Int("dimensions", len(embedResp.Embeddings[0])),
	)

	return embedResp.Embeddings, nil
}

func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/tags", nil,
	)
	if err != nil {
		return fmt.Errorf("ollama: Ping: create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: Ping: server unreachable at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama: Ping: unexpected status %d", resp.StatusCode)
	}

	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return fmt.Errorf("ollama: Ping: decode tags response: %w", err)
	}

	for _, m := range tagsResp.Models {
		if m.Name == c.model || m.Name == c.model+":latest" {
			c.log.Info("ollama: model available", zap.String("model", c.model))
			return nil
		}
	}

	available := make([]string, 0, len(tagsResp.Models))
	for _, m := range tagsResp.Models {
		available = append(available, m.Name)
	}

	return fmt.Errorf(
		"ollama: model %q not found. Pull it with: ollama pull %s (available: %v)",
		c.model, c.model, available,
	)
}
