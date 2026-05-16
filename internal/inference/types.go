package inference

import (
	"context"
	"fmt"
	"time"
)

type Provider interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
	Name() string
	ModelID() string
}

type CompletionRequest struct {
	SystemPrompt string
	UserMessage  string
	MaxTokens    int
	Temperature  float64
}

type CompletionResponse struct {
	Content      string
	InputTokens  int
	OutputTokens int
	Model        string
	Provider     string
}

type Config struct {
	Provider    string
	Model       string
	BaseURL     string
	APIKey      string
	MaxTokens   int
	Temperature float64
	Timeout     time.Duration
}

type ErrUnknownProvider struct {
	Name string
}

func (e ErrUnknownProvider) Error() string {
	return fmt.Sprintf(
		"inference: unknown provider %q — valid options: anthropic, openai, gemini, ollama, lmstudio",
		e.Name,
	)
}
