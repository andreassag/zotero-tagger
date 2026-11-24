package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/andreassag/zotero-tagger/internal/config"
	"github.com/andreassag/zotero-tagger/internal/tagging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func newTestClient(serverURL string, client *http.Client) (*Client, error) {
	genaiClient, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:     "test-api-key",
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: client,
		HTTPOptions: genai.HTTPOptions{
			BaseURL: serverURL,
		},
	})
	if err != nil {
		return nil, err
	}

	return NewClientForTest(genaiClient, client, serverURL, "gemini-3.5-flash-lite", 0.0, "gemini-decision-1"), nil
}

func assertBiofilmDecisionQuestion(t *testing.T, question interface{}) {
	t.Helper()
	assert.Equal(t, map[string]interface{}{
		"type":         "noul",
		"instructions": `Is "biofilm" a primary research subject, core scientific focus, or central methodology of this paper?`,
		"criteria": map[string]interface{}{
			"true_description":  `The paper directly investigates, measures, models, or centers on "biofilm" as a core scientific topic or primary methodology.`,
			"false_description": `The paper does not cover "biofilm", or only mentions it in passing as background context, related literature, or routine boilerplate.`,
		},
	}, question)
}

func TestCallSync_Success(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)

		resp := map[string]interface{}{
			"candidates": []map[string]interface{}{
				{
					"content": map[string]interface{}{
						"parts": []map[string]interface{}{
							{
								"text": `{"org_tags":["org:escherichia-coli"],"group_tags":[]}`,
							},
						},
						"role": "model",
					},
					"finishReason": "STOP",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)

	result, err := client.CallSync(context.Background(), "system prompt", "user prompt")
	assert.NoError(t, err)
	assert.Contains(t, result, "org:escherichia-coli")
}

func TestCallSync_EmptyCandidates(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"candidates": []interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)

	_, err = client.CallSync(context.Background(), "system", "user")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no response choices")
}

func TestCallDecisions_Success(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1beta/decisions", r.URL.Path)
		assert.Equal(t, "test-api-key", r.Header.Get("x-goog-api-key"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var reqBody map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)
		assert.Equal(t, "gemini-decision-1", reqBody["model"])
		assert.Equal(t, "Title: Biofilm Study", reqBody["input"])
		questionsMap, ok := reqBody["questions"].(map[string]interface{})
		require.True(t, ok)
		assertBiofilmDecisionQuestion(t, questionsMap["biofilm"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"biofilm":{"type":"noul","noul":0.91,"confidence":0.88}}}`))
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)

	questions := tagging.BuildTopicDecisionQuestions([]string{"biofilm"})
	rawResp, err := client.CallDecisions(context.Background(), "Title: Biofilm Study", questions)
	require.NoError(t, err)
	assert.Contains(t, rawResp, `"noul":0.91`)
}

func TestCallDecisionsWithPDF_Success(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	pdfContent := []byte("%PDF-1.4 dummy pdf bytes")
	tmpFile, err := os.CreateTemp("", "decisions-test-*.pdf")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	_, err = tmpFile.Write(pdfContent)
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	expectedB64 := base64.StdEncoding.EncodeToString(pdfContent)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1beta/decisions", r.URL.Path)
		assert.Equal(t, "test-api-key", r.Header.Get("x-goog-api-key"))

		var reqBody struct {
			Model     string                 `json:"model"`
			Input     []decisionInputPart    `json:"input"`
			Questions map[string]interface{} `json:"questions"`
		}
		decodeErr := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, decodeErr)
		assert.Equal(t, "gemini-decision-1", reqBody.Model)
		require.Len(t, reqBody.Input, 2)
		assert.Equal(t, "document", reqBody.Input[0].Type)
		assert.Equal(t, expectedB64, reqBody.Input[0].Data)
		assert.Equal(t, "application/pdf", reqBody.Input[0].MimeType)
		assert.Equal(t, "text", reqBody.Input[1].Type)
		assert.Equal(t, "Title: PDF Study", reqBody.Input[1].Text)
		assertBiofilmDecisionQuestion(t, reqBody.Questions["biofilm"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"biofilm":{"type":"noul","noul":0.85,"confidence":0.79}}}`))
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)

	questions := tagging.BuildTopicDecisionQuestions([]string{"biofilm"})
	rawResp, err := client.CallDecisionsWithPDF(context.Background(), "Title: PDF Study", tmpFile.Name(), questions)
	require.NoError(t, err)
	assert.Contains(t, rawResp, `"noul":0.85`)
}

func TestCallDecisions_HTTPError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid model"}}`))
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)

	questions := tagging.BuildTopicDecisionQuestions([]string{"biofilm"})
	_, err = client.CallDecisions(context.Background(), "Title: Study", questions)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestCallDecisions_Retries429And500(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		switch n {
		case 1:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
		case 2:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"transient error"}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"answers":{"biofilm":{"type":"noul","noul":0.88,"confidence":0.82}}}`))
		}
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)
	client.retries = config.RetryConfig{MaxRetries: 2, BackoffBase: 0.01}

	questions := tagging.BuildTopicDecisionQuestions([]string{"biofilm"})
	rawResp, err := client.CallDecisions(context.Background(), "Title: Retry Study", questions)
	require.NoError(t, err)
	assert.Equal(t, int32(3), calls.Load())
	assert.Contains(t, rawResp, `"noul":0.88`)
}

func TestCallDecisions_EmptyQuestionsDoesNotConsumeQuota(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"biofilm":{"type":"noul","noul":0.91,"confidence":0.88}}}`))
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)
	client.decisionRateLimiter = NewNamedRateLimiter("decisions", config.RateLimitConfig{RequestsPerDay: 1})

	_, err = client.CallDecisions(context.Background(), "Title: Study", map[string]tagging.DecisionQuestion{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no decision questions")

	questions := tagging.BuildTopicDecisionQuestions([]string{"biofilm"})
	_, err = client.CallDecisions(context.Background(), "Title: Study", questions)
	assert.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())
}

func TestCallDecisionsWithCache(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"biofilm":{"type":"noul","noul":0.93,"confidence":0.87}}}`))
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)

	questions := tagging.BuildTopicDecisionQuestions([]string{"biofilm"})
	resp1, err := client.CallDecisionsWithCache(context.Background(), "Title: Cached Study", questions, true)
	require.NoError(t, err)
	resp2, err := client.CallDecisionsWithCache(context.Background(), "Title: Cached Study", questions, true)
	require.NoError(t, err)
	assert.Equal(t, resp1, resp2)
	assert.Equal(t, int32(1), calls.Load())
}

func TestRateLimiter_DailyLimit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	statePath := getStateFilePath("default")
	_ = os.Remove(statePath)
	defer func() { _ = os.Remove(statePath) }()

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

func TestRateLimiter_SeparateDailyCounters(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	extractState := getStateFilePath("extract")
	decisionsState := getStateFilePath("decisions")
	_ = os.Remove(extractState)
	_ = os.Remove(decisionsState)
	defer func() {
		_ = os.Remove(extractState)
		_ = os.Remove(decisionsState)
	}()

	extractRL := NewNamedRateLimiter("extract", config.RateLimitConfig{RequestsPerDay: 1})
	decisionsRL := NewNamedRateLimiter("decisions", config.RateLimitConfig{RequestsPerDay: 2})

	assert.NoError(t, extractRL.Wait(context.Background(), 0))
	assert.Error(t, extractRL.Wait(context.Background(), 0))

	// Decisions limiter should still have its own independent daily budget of 2
	assert.NoError(t, decisionsRL.Wait(context.Background(), 0))
	assert.NoError(t, decisionsRL.Wait(context.Background(), 0))
	assert.Error(t, decisionsRL.Wait(context.Background(), 0))
}

func TestRateLimiter_Unlimited(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg := config.RateLimitConfig{
		RequestsPerMinute: 0,
		TokensPerMinute:   0,
		RequestsPerDay:    0,
	}
	rl := NewRateLimiter(cfg)

	err := rl.Wait(context.Background(), 100)
	assert.NoError(t, err)
}

func TestHashPDFPrompt(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test-*.pdf")
	assert.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	_, err = tmpFile.WriteString("%PDF-1.4 dummy content")
	assert.NoError(t, err)
	_ = tmpFile.Close()

	hash1, err := HashPDFPrompt("gemini-3.5-flash-lite", "system", "user", tmpFile.Name())
	assert.NoError(t, err)
	assert.NotEmpty(t, hash1)

	hash2, err := HashPDFPrompt("gemini-3.5-flash-lite", "system", "user", tmpFile.Name())
	assert.NoError(t, err)
	assert.Equal(t, hash1, hash2)
}

func TestCallDecisionsWithFileURI_Success(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	fileURI := "https://generativelanguage.googleapis.com/v1beta/files/abc123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1beta/decisions", r.URL.Path)

		var reqBody struct {
			Model     string                 `json:"model"`
			Input     []decisionInputPart    `json:"input"`
			Questions map[string]interface{} `json:"questions"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		assert.Equal(t, "gemini-decision-1", reqBody.Model)
		require.Len(t, reqBody.Input, 2)
		assert.Equal(t, "document", reqBody.Input[0].Type)
		assert.Equal(t, fileURI, reqBody.Input[0].URI)
		assert.Empty(t, reqBody.Input[0].Data)
		assert.Equal(t, "application/pdf", reqBody.Input[0].MimeType)
		assert.Equal(t, "text", reqBody.Input[1].Type)
		assert.Equal(t, "Title: URI Study", reqBody.Input[1].Text)
		assertBiofilmDecisionQuestion(t, reqBody.Questions["biofilm"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"biofilm":{"type":"noul","noul":0.91,"confidence":0.87}}}`))
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)

	questions := tagging.BuildTopicDecisionQuestions([]string{"biofilm"})
	rawResp, err := client.CallDecisionsWithFileURI(context.Background(), "Title: URI Study", fileURI, questions)
	require.NoError(t, err)
	assert.Contains(t, rawResp, `"noul":0.91`)
}

func TestCallSync_Retries429And500(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":429,"message":"rate limit","status":"RESOURCE_EXHAUSTED"}}`))
		case 2:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":500,"message":"internal error","status":"INTERNAL"}}`))
		default:
			resp := map[string]interface{}{
				"candidates": []map[string]interface{}{
					{
						"content": map[string]interface{}{
							"parts": []map[string]interface{}{
								{"text": `{"org_tags":["org:escherichia-coli"],"group_tags":[]}`},
							},
							"role": "model",
						},
						"finishReason": "STOP",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)
	client.retries = config.RetryConfig{MaxRetries: 2, BackoffBase: 0.01}

	result, err := client.CallSync(context.Background(), "system prompt", "user prompt")
	require.NoError(t, err)
	assert.Equal(t, int32(3), calls.Load())
	assert.Contains(t, result, "org:escherichia-coli")
}

func TestCallSync_DoesNotRetry400(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"bad request","status":"INVALID_ARGUMENT"}}`))
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)
	client.retries = config.RetryConfig{MaxRetries: 3, BackoffBase: 0.01}

	_, err = client.CallSync(context.Background(), "system prompt", "user prompt")
	require.Error(t, err)
	assert.Equal(t, int32(1), calls.Load())
}

func TestUploadPDF_RetriesAndCleanup(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	tmpFile, err := os.CreateTemp("", "upload-retry-*.pdf")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	_, err = tmpFile.WriteString("%PDF-1.4 dummy pdf content")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	var startCalls atomic.Int32
	var chunkCalls atomic.Int32
	var deleteCalls atomic.Int32

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/upload/v1beta/files" {
			cmd := r.Header.Get("X-Goog-Upload-Command")
			if cmd == "start" {
				if startCalls.Add(1) == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(`{"error":{"code":429,"message":"rate limited","status":"RESOURCE_EXHAUSTED"}}`))
					return
				}
				w.Header().Set("X-Goog-Upload-Url", serverURL+"/upload/v1beta/files?upload_id=u1")
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.URL.Query().Get("upload_id") == "u1" {
				if chunkCalls.Add(1) == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":{"code":500,"message":"transient upload failure","status":"INTERNAL"}}`))
					return
				}
				w.Header().Set("X-Goog-Upload-Status", "final")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"file":{"name":"files/pdf123","uri":"https://generativelanguage.googleapis.com/v1beta/files/pdf123","mimeType":"application/pdf"}}`))
				return
			}
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/v1beta/files/pdf123" {
			deleteCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	serverURL = server.URL

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)
	client.retries = config.RetryConfig{MaxRetries: 3, BackoffBase: 0.01}

	uploadedFile, cleanup, err := client.UploadPDF(context.Background(), tmpFile.Name())
	require.NoError(t, err)
	require.NotNil(t, uploadedFile)
	require.NotNil(t, cleanup)
	assert.Equal(t, "files/pdf123", uploadedFile.Name)
	assert.Equal(t, "https://generativelanguage.googleapis.com/v1beta/files/pdf123", uploadedFile.URI)
	assert.Equal(t, int32(2), startCalls.Load())
	assert.Equal(t, int32(2), chunkCalls.Load())

	cleanup()
	assert.Equal(t, int32(1), deleteCalls.Load())
}

func TestPDFTokenEstimationIncludesPromptsAndQuestions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	tmpFile, err := os.CreateTemp("", "tokens-test-*.pdf")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	_, err = tmpFile.WriteString("%PDF-1.4 content")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1beta/decisions" {
			_, _ = w.Write([]byte(`{"answers":{"biofilm":{"type":"noul","noul":0.9,"confidence":0.9}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"{\"org_tags\":[],\"group_tags\":[]}"}],"role":"model"},"finishReason":"STOP"}]}`))
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)

	sysPrompt := "Extract organism and group tags accurately."
	usrPrompt := "Title: Biofilm dynamics in Pseudomonas aeruginosa"
	questions := tagging.BuildTopicDecisionQuestions([]string{"biofilm", "quorum-sensing"})

	extractExtra := estimateExtractTokens(sysPrompt, usrPrompt)
	decisionExtra := estimateDecisionTokens(usrPrompt, questions)
	assert.Greater(t, extractExtra, 0)
	assert.Greater(t, decisionExtra, 0)

	// Burst == 2000 is insufficient because 2000 + prompt/question tokens > 2000.
	client.extractRateLimiter = NewNamedRateLimiter("extract-tight", config.RateLimitConfig{TokensPerMinute: 2000})
	_, err = client.CallSyncWithUploadedFile(context.Background(), sysPrompt, usrPrompt, &genai.File{
		Name:     "files/test1",
		URI:      "https://generativelanguage.googleapis.com/v1beta/files/test1",
		MIMEType: "application/pdf",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds limiter's burst")

	client.decisionRateLimiter = NewNamedRateLimiter("decisions-tight", config.RateLimitConfig{TokensPerMinute: 2000})
	_, err = client.CallDecisionsWithFileURI(context.Background(), usrPrompt, "https://generativelanguage.googleapis.com/v1beta/files/test1", questions)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds limiter's burst")

	// Burst == 2000 + estimated prompt/question tokens succeeds.
	client.extractRateLimiter = NewNamedRateLimiter("extract-exact", config.RateLimitConfig{TokensPerMinute: pdfBaseTokens + extractExtra})
	_, err = client.CallSyncWithUploadedFile(context.Background(), sysPrompt, usrPrompt, &genai.File{
		Name:     "files/test1",
		URI:      "https://generativelanguage.googleapis.com/v1beta/files/test1",
		MIMEType: "application/pdf",
	})
	require.NoError(t, err)

	client.decisionRateLimiter = NewNamedRateLimiter("decisions-exact", config.RateLimitConfig{TokensPerMinute: pdfBaseTokens + decisionExtra})
	_, err = client.CallDecisionsWithFileURI(context.Background(), usrPrompt, "https://generativelanguage.googleapis.com/v1beta/files/test1", questions)
	require.NoError(t, err)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type mockNetTimeoutError struct{}

func (mockNetTimeoutError) Error() string   { return "dial tcp: i/o timeout" }
func (mockNetTimeoutError) Timeout() bool   { return true }
func (mockNetTimeoutError) Temporary() bool { return true }

func TestIsRetryableGenAIError_EdgeCases(t *testing.T) {
	ctx := context.Background()

	assert.False(t, isRetryableGenAIError(ctx, nil))
	assert.False(t, isRetryableGenAIError(ctx, context.Canceled))
	assert.False(t, isRetryableGenAIError(ctx, context.DeadlineExceeded))
	assert.False(t, isRetryableGenAIError(ctx, genai.APIError{Code: 400, Message: "bad request"}))
	assert.False(t, isRetryableGenAIError(ctx, fmt.Errorf("Failed to create file. Ran into an error: Error 400, Message: bad")))

	assert.True(t, isRetryableGenAIError(ctx, genai.APIError{Code: 429, Message: "rate limit"}))
	assert.True(t, isRetryableGenAIError(ctx, genai.APIError{Code: 503, Message: "unavailable"}))
	assert.True(t, isRetryableGenAIError(ctx, fmt.Errorf("Failed to create file. Ran into an error: Error 503, Message: unavailable")))
	assert.True(t, isRetryableGenAIError(ctx, io.EOF))
	assert.True(t, isRetryableGenAIError(ctx, io.ErrUnexpectedEOF))
	assert.True(t, isRetryableGenAIError(ctx, mockNetTimeoutError{}))
	assert.True(t, isRetryableGenAIError(ctx, fmt.Errorf("Failed to create file. Ran into an error: Post \"https://example.com\": dial tcp 1.2.3.4:443: connect: connection timed out")))
	assert.True(t, isRetryableGenAIError(ctx, fmt.Errorf("Failed to create file. Ran into an error: http2: client connection lost")))

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.False(t, isRetryableGenAIError(canceledCtx, genai.APIError{Code: 503, Message: "unavailable"}))
}

func TestCallSync_RetriesTransientNetworkErrorsAndAbortsOnCancel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var calls atomic.Int32
	httpClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			n := calls.Add(1)
			switch n {
			case 1:
				return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
			case 2:
				return nil, io.EOF
			default:
				body := `{"candidates":[{"content":{"parts":[{"text":"{\"org_tags\":[\"org:escherichia-coli\"],\"group_tags\":[]}"}],"role":"model"},"finishReason":"STOP"}]}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(bytes.NewBufferString(body)),
				}, nil
			}
		}),
	}

	client, err := newTestClient("http://mock.local", httpClient)
	require.NoError(t, err)
	client.retries = config.RetryConfig{MaxRetries: 3, BackoffBase: 0.01}

	resp, err := client.CallSync(context.Background(), "system", "user")
	require.NoError(t, err)
	assert.Equal(t, int32(3), calls.Load())
	assert.Contains(t, resp, "org:escherichia-coli")

	// Context cancellation must abort immediately without retrying.
	calls.Store(0)
	canceledCtx, cancel := context.WithCancel(context.Background())
	httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		cancel()
		return nil, context.Canceled
	})
	_, err = client.CallSync(canceledCtx, "system", "user")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(1), calls.Load())
}

func TestUploadPDF_RetriesTransientNetworkErrorAndChunkFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	tmpFile, err := os.CreateTemp("", "upload-net-retry-*.pdf")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	_, err = tmpFile.WriteString("%PDF-1.4 dummy pdf content")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	var startCalls atomic.Int32
	var chunkCalls atomic.Int32
	httpClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPost && r.URL.Path == "/upload/v1beta/files" {
				if r.Header.Get("X-Goog-Upload-Command") == "start" {
					n := startCalls.Add(1)
					if n == 1 {
						// Simulates transient network failure on create-file request (wrapped via %s by SDK).
						return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection timed out")}
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header: http.Header{
							"X-Goog-Upload-Url": []string{"http://mock.local/upload/v1beta/files?upload_id=u1"},
						},
						Body: io.NopCloser(bytes.NewBuffer(nil)),
					}, nil
				}
				if r.URL.Query().Get("upload_id") == "u1" {
					c := chunkCalls.Add(1)
					if c == 1 {
						// X-Goog-Upload-Status: final with 500 causes SDK to return APIError immediately so withGenAIRetry retries UploadFromPath from start.
						return &http.Response{
							StatusCode: http.StatusInternalServerError,
							Header: http.Header{
								"Content-Type":         []string{"application/json"},
								"X-Goog-Upload-Status": []string{"final"},
							},
							Body: io.NopCloser(bytes.NewBufferString(`{"error":{"code":500,"message":"chunk failed","status":"INTERNAL"}}`)),
						}, nil
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header: http.Header{
							"Content-Type":         []string{"application/json"},
							"X-Goog-Upload-Status": []string{"final"},
						},
						Body: io.NopCloser(bytes.NewBufferString(`{"file":{"name":"files/net123","uri":"https://generativelanguage.googleapis.com/v1beta/files/net123","mimeType":"application/pdf"}}`)),
					}, nil
				}
			}
			if r.Method == http.MethodDelete && r.URL.Path == "/v1beta/files/net123" {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString(`{}`)),
				}, nil
			}
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewBuffer(nil))}, nil
		}),
	}

	client, err := newTestClient("http://mock.local", httpClient)
	require.NoError(t, err)
	client.retries = config.RetryConfig{MaxRetries: 3, BackoffBase: 0.01}

	uploadedFile, cleanup, err := client.UploadPDF(context.Background(), tmpFile.Name())
	require.NoError(t, err)
	require.NotNil(t, uploadedFile)
	defer cleanup()

	assert.Equal(t, "files/net123", uploadedFile.Name)
	assert.Equal(t, int32(3), startCalls.Load())
	assert.Equal(t, int32(2), chunkCalls.Load())
}

func TestCallDecisions_RetriesTransientNetworkErrorAndAbortsOnCancel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var calls atomic.Int32
	httpClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			n := calls.Add(1)
			if n == 1 {
				return nil, io.EOF
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewBufferString(`{"answers":{"biofilm":{"type":"noul","noul":0.91,"confidence":0.88}}}`)),
			}, nil
		}),
	}

	client, err := newTestClient("http://mock.local", httpClient)
	require.NoError(t, err)
	client.retries = config.RetryConfig{MaxRetries: 2, BackoffBase: 0.01}

	questions := tagging.BuildTopicDecisionQuestions([]string{"biofilm"})
	rawResp, err := client.CallDecisions(context.Background(), "Title: Network Retry", questions)
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load())
	assert.Contains(t, rawResp, `"noul":0.91`)

	// Context cancellation must abort immediately without retrying.
	calls.Store(0)
	canceledCtx, cancel := context.WithCancel(context.Background())
	httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		cancel()
		return nil, context.Canceled
	})
	_, err = client.CallDecisions(canceledCtx, "Title: Canceled", questions)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(1), calls.Load())
}

func TestCallSync_RetriesConsumeDailyQuota(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":500,"message":"boom","status":"INTERNAL"}}`))
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)
	client.retries = config.RetryConfig{MaxRetries: 3, BackoffBase: 0.001}
	client.extractRateLimiter = NewNamedRateLimiter("extract", config.RateLimitConfig{RequestsPerDay: 2})

	_, err = client.CallSync(context.Background(), "sys", "user")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "daily rate limit exceeded")
	assert.Equal(t, int32(2), calls.Load())
}

func TestCallSyncWithUploadedFile_RetriesConsumeDailyQuota(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":500,"message":"boom","status":"INTERNAL"}}`))
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)
	client.retries = config.RetryConfig{MaxRetries: 3, BackoffBase: 0.001}
	client.extractRateLimiter = NewNamedRateLimiter("extract", config.RateLimitConfig{RequestsPerDay: 2})

	_, err = client.CallSyncWithUploadedFile(context.Background(), "sys", "user", &genai.File{
		Name: "files/x", URI: "https://example.test/files/x", MIMEType: "application/pdf",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "daily rate limit exceeded")
	assert.Equal(t, int32(2), calls.Load())
}

func TestCallDecisions_RetriesConsumeDailyQuota(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)
	client.retries = config.RetryConfig{MaxRetries: 3, BackoffBase: 0.001}
	client.decisionRateLimiter = NewNamedRateLimiter("decisions", config.RateLimitConfig{RequestsPerDay: 2})

	questions := tagging.BuildTopicDecisionQuestions([]string{"biofilm"})
	_, err = client.CallDecisions(context.Background(), "Title: Study", questions)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "daily rate limit exceeded")
	assert.Equal(t, int32(2), calls.Load())
}

func TestUploadPDF_DoesNotConsumeModelQuota(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"{}"}],"role":"model"},"finishReason":"STOP"}]}`))
	}))
	defer server.Close()

	client, err := newTestClient(server.URL, server.Client())
	require.NoError(t, err)
	client.extractRateLimiter = NewNamedRateLimiter("extract", config.RateLimitConfig{RequestsPerDay: 1})

	// withGenAIRetry with a nil gate (as used by files.upload) must not touch the limiter.
	calls := 0
	_, err = withGenAIRetry(context.Background(), client, "files.upload", nil, func() (int, error) {
		calls++
		return calls, nil
	})
	require.NoError(t, err)

	_, err = client.CallSync(context.Background(), "sys", "user")
	require.NoError(t, err)
}
