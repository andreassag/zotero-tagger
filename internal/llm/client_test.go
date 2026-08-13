package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/exterex/zotero-tagger/internal/config"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func makeTestLLMConfig(serverURL string) config.LLMConfig {
	return config.LLMConfig{
		APIKey:         "test-key",
		ModelName:      "test-model",
		BaseURL:        serverURL + "/v1",
		Temperature:    0.0,
		MaxInputTokens: 1000,
		RateLimits: config.RateLimitConfig{
			RequestsPerMinute: 0, // unlimited for tests
			TokensPerMinute:   0,
			RequestsPerDay:    0,
		},
		Retries: config.RetryConfig{
			MaxRetries:  1,
			BackoffBase: 0.01,
		},
	}
}

func TestCallSync_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)

		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]string{
						"content": `{"org_tags":["org:escherichia-coli"],"group_tags":[],"topic_tags":["topic:pcr"]}`,
						"role":    "assistant",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := zerolog.Nop()
	client := NewClient(makeTestLLMConfig(server.URL), logger)

	result, err := client.CallSync(context.Background(), "system prompt", "user prompt")
	assert.NoError(t, err)
	assert.Contains(t, result, "org:escherichia-coli")
}

func TestCallSync_EmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := zerolog.Nop()
	client := NewClient(makeTestLLMConfig(server.URL), logger)

	_, err := client.CallSync(context.Background(), "system", "user")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no response choices")
}

func TestRateLimiter_DailyLimit(t *testing.T) {
	statePath := getStateFilePath()
	_ = os.Remove(statePath)
	defer os.Remove(statePath)

	cfg := config.RateLimitConfig{
		RequestsPerMinute: 0,
		TokensPerMinute:   0,
		RequestsPerDay:    2,
	}
	rl := NewRateLimiter(cfg)

	err1 := rl.Wait(context.Background(), 0)
	assert.NoError(t, err1)

	err2 := rl.Wait(context.Background(), 0)
	assert.NoError(t, err2)

	// Third request should be blocked (daily limit reached)
	err3 := rl.Wait(context.Background(), 0)
	assert.Error(t, err3)
}
