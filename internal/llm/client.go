package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/exterex/zotero-tagger/internal/config"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/rs/zerolog"
)

type Client struct {
	client      *openai.Client
	model       string
	temperature float64
	rateLimiter *RateLimiter
	retryConfig config.RetryConfig
	logger      zerolog.Logger
}

func NewClient(cfg config.LLMConfig, logger zerolog.Logger) *Client {
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}

	client := openai.NewClient(opts...)

	return &Client{
		client:      &client,
		model:       cfg.ModelName,
		temperature: cfg.Temperature,
		rateLimiter: NewRateLimiter(cfg.RateLimits),
		retryConfig: cfg.Retries,
		logger:      logger,
	}
}

func (c *Client) CallSync(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	// Word-count proxy (1 word ≈ 1.3 tokens) — consistent with TruncateToTokenBudget
	estimatedTokens := int(float64(len(strings.Fields(systemPrompt))+len(strings.Fields(userPrompt))) * 1.3)
	if err := c.rateLimiter.Wait(ctx, estimatedTokens); err != nil {
		return "", fmt.Errorf("rate limiter wait failed: %w", err)
	}

	params := openai.ChatCompletionNewParams{
		Model: openai.ChatModel(c.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userPrompt),
		},
		Temperature: openai.Float(c.temperature),
	}

	resp, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("chat completion request failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response choices returned from LLM")
	}

	return resp.Choices[0].Message.Content, nil
}
