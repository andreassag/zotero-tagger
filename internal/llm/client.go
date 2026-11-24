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
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/andreassag/zotero-tagger/internal/config"
	"github.com/andreassag/zotero-tagger/internal/tagging"
	"github.com/rs/zerolog"
	"google.golang.org/genai"
)

const (
	defaultGeminiBaseURL = "https://generativelanguage.googleapis.com"
)

type Client struct {
	client              *genai.Client
	httpClient          *http.Client
	baseURL             string
	apiKey              string
	extractModel        string
	temperature         float64
	extractRateLimiter  *RateLimiter
	decisionModel       string
	decisionRateLimiter *RateLimiter
	retries             config.RetryConfig
	logger              zerolog.Logger
	cache               *DiskCache
}

type decisionRequest struct {
	Model     string                              `json:"model"`
	Input     any                                 `json:"input"`
	Questions map[string]tagging.DecisionQuestion `json:"questions"`
}

type decisionInputPart struct {
	Type     string `json:"type"`
	Data     string `json:"data,omitempty"`
	URI      string `json:"uri,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Text     string `json:"text,omitempty"`
}

func NewClient(ctx context.Context, cfg config.LLMConfig, logger zerolog.Logger) (*Client, error) {
	apiKey := cfg.APIKey
	if apiKey == "" {
		if k := os.Getenv("GEMINI_API_KEY"); k != "" {
			apiKey = k
		} else if k := os.Getenv("GOOGLE_API_KEY"); k != "" {
			apiKey = k
		}
	}

	clientConfig := &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	}

	client, err := genai.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Google GenAI client: %w", err)
	}

	retries := cfg.Retries
	if retries.MaxRetries == 0 && retries.BackoffBase == 0 {
		retries.MaxRetries = 3
		retries.BackoffBase = 1.0
	}

	return &Client{
		client:              client,
		httpClient:          &http.Client{Timeout: 2 * time.Minute},
		baseURL:             defaultGeminiBaseURL,
		apiKey:              apiKey,
		extractModel:        cfg.Extract.ModelName,
		temperature:         cfg.Extract.Temperature,
		extractRateLimiter:  NewNamedRateLimiter("extract", cfg.Extract.RateLimits),
		decisionModel:       cfg.Decisions.ModelName,
		decisionRateLimiter: NewNamedRateLimiter("decisions", cfg.Decisions.RateLimits),
		retries:             retries,
		logger:              logger,
		cache:               NewDiskCache(),
	}, nil
}

func NewClientForTest(client *genai.Client, httpClient *http.Client, baseURL, extractModel string, temperature float64, decisionModel string) *Client {
	rateCfg := config.RateLimitConfig{
		RequestsPerMinute: 0,
		TokensPerMinute:   0,
		RequestsPerDay:    0,
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = defaultGeminiBaseURL
	}

	return &Client{
		client:              client,
		httpClient:          httpClient,
		baseURL:             baseURL,
		apiKey:              "test-api-key",
		extractModel:        extractModel,
		temperature:         temperature,
		extractRateLimiter:  NewNamedRateLimiter("extract", rateCfg),
		decisionModel:       decisionModel,
		decisionRateLimiter: NewNamedRateLimiter("decisions", rateCfg),
		retries:             config.RetryConfig{MaxRetries: 0, BackoffBase: 0},
		logger:              zerolog.Nop(),
		cache:               NewDiskCache(),
	}
}

func (c *Client) extractResponseText(resp *genai.GenerateContentResponse) (string, error) {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no response choices returned from LLM")
	}

	result := resp.Text()
	if result == "" {
		var sb strings.Builder
		for _, part := range resp.Candidates[0].Content.Parts {
			if part.Text != "" {
				sb.WriteString(part.Text)
			}
		}
		result = sb.String()
	}

	if result == "" {
		return "", fmt.Errorf("empty text response returned from LLM")
	}

	return result, nil
}

var genaiAPIErrorCodeRe = regexp.MustCompile(`Error (\d{3}),`)

func isRetryableGenAIError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code == http.StatusTooManyRequests || apiErr.Code >= 500
	}
	var apiErrPtr *genai.APIError
	if errors.As(err, &apiErrPtr) && apiErrPtr != nil {
		return apiErrPtr.Code == http.StatusTooManyRequests || apiErrPtr.Code >= 500
	}

	// Files.Upload formats initial create-file errors with %s instead of %w.
	if matches := genaiAPIErrorCodeRe.FindStringSubmatch(err.Error()); len(matches) == 2 {
		if code, convErr := strconv.Atoi(matches[1]); convErr == nil {
			return code == http.StatusTooManyRequests || code >= 500
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	msg := strings.ToLower(err.Error())
	transientPatterns := []string{
		"connection reset",
		"connection refused",
		"connection timed out",
		"broken pipe",
		"unexpected eof",
		"i/o timeout",
		"tls handshake timeout",
		"timeout awaiting response headers",
		"closed network connection",
		"client connection lost",
		"server closed idle connection",
		"temporary failure in name resolution",
		": eof",
	}
	for _, p := range transientPatterns {
		if strings.Contains(msg, p) {
			return true
		}
	}

	return false
}

// attemptGate runs before every attempt (including the first). Errors are terminal.
type attemptGate func(ctx context.Context) error

func limiterGate(rl *RateLimiter, tokens int) attemptGate {
	return func(ctx context.Context) error {
		if err := rl.Wait(ctx, tokens); err != nil {
			return fmt.Errorf("rate limiter wait failed: %w", err)
		}
		return nil
	}
}

func withGenAIRetry[T any](ctx context.Context, c *Client, op string, gate attemptGate, fn func() (T, error)) (T, error) {
	var zero T
	maxRetries := c.retries.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	backoffBase := c.retries.BackoffBase
	if backoffBase <= 0 {
		backoffBase = 1.0
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(backoffBase * float64(attempt) * float64(time.Second))
			c.logger.Warn().
				Err(lastErr).
				Str("operation", op).
				Int("attempt", attempt).
				Int("maxRetries", maxRetries).
				Dur("backoff", backoff).
				Msg("Retrying Google GenAI request")
			if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
				return zero, sleepErr
			}
		}

		if gate != nil {
			if gateErr := gate(ctx); gateErr != nil {
				return zero, gateErr
			}
		}

		res, err := fn()
		if err == nil {
			return res, nil
		}
		lastErr = err

		if !isRetryableGenAIError(ctx, err) {
			return zero, err
		}
	}

	return zero, lastErr
}

func estimateExtractTokens(systemPrompt, userPrompt string) int {
	return int(float64(len(strings.Fields(systemPrompt))+len(strings.Fields(userPrompt))) * 1.3)
}

func (c *Client) CallSync(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	estimatedTokens := estimateExtractTokens(systemPrompt, userPrompt)

	temp := float32(c.temperature)
	genConfig := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{
				{Text: systemPrompt},
			},
		},
		Temperature:      &temp,
		ResponseMIMEType: "application/json",
	}

	resp, err := withGenAIRetry(ctx, c, "models.generateContent", limiterGate(c.extractRateLimiter, estimatedTokens), func() (*genai.GenerateContentResponse, error) {
		return c.client.Models.GenerateContent(ctx, c.extractModel, genai.Text(userPrompt), genConfig)
	})
	if err != nil {
		return "", fmt.Errorf("gemini generate content failed: %w", err)
	}

	return c.extractResponseText(resp)
}

func (c *Client) CallSyncWithCache(ctx context.Context, systemPrompt, userPrompt string, useCache bool) (string, error) {
	hash := HashPrompt(c.extractModel, systemPrompt, userPrompt)
	if useCache && c.cache != nil {
		if cached, ok := c.cache.Get(hash); ok {
			c.logger.Info().Str("hash", hash[:8]).Msg("[CACHE HIT] Using cached LLM response")
			return cached, nil
		}
	}

	resp, err := c.CallSync(ctx, systemPrompt, userPrompt)
	if err != nil {
		return "", err
	}

	if useCache && c.cache != nil {
		_ = c.cache.Set(hash, resp)
	}

	return resp, nil
}

func (c *Client) UploadPDF(ctx context.Context, pdfPath string) (*genai.File, func(), error) {
	c.logger.Info().Str("path", pdfPath).Msg("Uploading PDF to Google GenAI...")
	uploadedFile, err := withGenAIRetry(ctx, c, "files.upload", nil, func() (*genai.File, error) {
		return c.client.Files.UploadFromPath(ctx, pdfPath, &genai.UploadFileConfig{
			MIMEType: "application/pdf",
		})
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to upload PDF to Google GenAI: %w", err)
	}

	cleanup := func() {
		if uploadedFile == nil || uploadedFile.Name == "" {
			return
		}
		delCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, delErr := c.client.Files.Delete(delCtx, uploadedFile.Name, nil); delErr != nil {
			c.logger.Warn().Err(delErr).Str("fileName", uploadedFile.Name).Msg("Failed to delete uploaded PDF from Google GenAI")
		}
	}

	if uploadedFile == nil || strings.TrimSpace(uploadedFile.URI) == "" {
		cleanup()
		return nil, nil, fmt.Errorf("failed to upload PDF to Google GenAI: uploaded file missing URI")
	}

	return uploadedFile, cleanup, nil
}

func (c *Client) CallSyncWithUploadedFile(ctx context.Context, systemPrompt, userPrompt string, uploadedFile *genai.File) (string, error) {
	return c.callSyncWithUploadedFile(ctx, systemPrompt, userPrompt, uploadedFile, pdfFallbackTokens)
}

func (c *Client) callSyncWithUploadedFile(ctx context.Context, systemPrompt, userPrompt string, uploadedFile *genai.File, docTokens int) (string, error) {
	if uploadedFile == nil || strings.TrimSpace(uploadedFile.URI) == "" {
		return "", fmt.Errorf("uploaded PDF file is missing URI")
	}

	estimatedTokens := docTokens + estimateExtractTokens(systemPrompt, userPrompt)

	temp := float32(c.temperature)
	genConfig := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{
				{Text: systemPrompt},
			},
		},
		Temperature:      &temp,
		ResponseMIMEType: "application/json",
	}

	filePart := *uploadedFile
	if filePart.MIMEType == "" {
		filePart.MIMEType = "application/pdf"
	}

	contents := []*genai.Content{
		{
			Parts: []*genai.Part{
				genai.NewPartFromFile(filePart),
				genai.NewPartFromText(userPrompt),
			},
		},
	}

	resp, err := withGenAIRetry(ctx, c, "models.generateContent", limiterGate(c.extractRateLimiter, estimatedTokens), func() (*genai.GenerateContentResponse, error) {
		r, genErr := c.client.Models.GenerateContent(ctx, c.extractModel, contents, genConfig)
		if genErr == nil && r != nil && r.UsageMetadata != nil {
			actual := int(r.UsageMetadata.PromptTokenCount) + int(r.UsageMetadata.CandidatesTokenCount)
			c.extractRateLimiter.Reconcile(estimatedTokens, actual)
		}
		return r, genErr
	})
	if err != nil {
		return "", fmt.Errorf("gemini generate content failed: %w", err)
	}

	return c.extractResponseText(resp)
}

func (c *Client) CallSyncWithPDF(ctx context.Context, systemPrompt, userPrompt, pdfPath string) (string, error) {
	uploadedFile, cleanup, err := c.UploadPDF(ctx, pdfPath)
	if err != nil {
		return "", err
	}
	defer cleanup()

	return c.CallSyncWithUploadedFile(ctx, systemPrompt, userPrompt, uploadedFile)
}

func (c *Client) CallSyncWithPDFProviderAndCache(ctx context.Context, systemPrompt, userPrompt, pdfPath string, fileProvider func() (*genai.File, error), useCache bool) (string, error) {
	docTokens := pdfFallbackTokens
	var hash string
	info, infoErr := InspectPDF(pdfPath)
	if infoErr == nil {
		docTokens = info.EstimatedTokens()
	}
	if useCache && c.cache != nil {
		if infoErr == nil {
			hash = hashPromptWithDigest(c.extractModel, systemPrompt, userPrompt, info.SHA256)
			if cached, ok := c.cache.Get(hash); ok {
				c.logger.Info().Str("hash", hash[:8]).Msg("[CACHE HIT] Using cached LLM response")
				return cached, nil
			}
		}
	}

	if fileProvider == nil {
		return "", fmt.Errorf("PDF file provider is required")
	}
	uploadedFile, err := fileProvider()
	if err != nil {
		return "", err
	}

	resp, err := c.callSyncWithUploadedFile(ctx, systemPrompt, userPrompt, uploadedFile, docTokens)
	if err != nil {
		return "", err
	}

	if hash != "" && useCache && c.cache != nil {
		_ = c.cache.Set(hash, resp)
	}

	return resp, nil
}

func (c *Client) CallSyncWithPDFAndCache(ctx context.Context, systemPrompt, userPrompt, pdfPath string, useCache bool) (string, error) {
	var cleanupFn func()
	defer func() {
		if cleanupFn != nil {
			cleanupFn()
		}
	}()

	return c.CallSyncWithPDFProviderAndCache(ctx, systemPrompt, userPrompt, pdfPath, func() (*genai.File, error) {
		f, cleanup, err := c.UploadPDF(ctx, pdfPath)
		if err != nil {
			return nil, err
		}
		cleanupFn = cleanup
		return f, nil
	}, useCache)
}

func estimateDecisionTokens(userPrompt string, questions map[string]tagging.DecisionQuestion) int {
	words := len(strings.Fields(userPrompt))
	for topic, q := range questions {
		words += len(strings.Fields(topic)) +
			len(strings.Fields(q.Instructions))
		if q.Criteria != nil {
			words += len(strings.Fields(q.Criteria.TrueDescription)) +
				len(strings.Fields(q.Criteria.FalseDescription))
		}
	}
	return int(float64(words) * 1.3)
}

func serializeQuestionsForHash(questions map[string]tagging.DecisionQuestion) (string, error) {
	b, err := json.Marshal(questions)
	if err != nil {
		return "", fmt.Errorf("failed to serialize decision questions: %w", err)
	}
	return string(b), nil
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) doDecisionRequest(ctx context.Context, input any, questions map[string]tagging.DecisionQuestion, estimatedTokens int) (string, error) {
	if len(questions) == 0 {
		return "", fmt.Errorf("no decision questions provided")
	}

	payload := decisionRequest{
		Model:     c.decisionModel,
		Input:     input,
		Questions: questions,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal decisions request: %w", err)
	}

	endpoint := strings.TrimRight(c.baseURL, "/") + "/v1beta/decisions"
	maxRetries := c.retries.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	backoffBase := c.retries.BackoffBase
	if backoffBase <= 0 {
		backoffBase = 1.0
	}

	var lastErr error
	skipNextBackoff := false
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 && !skipNextBackoff {
			backoff := time.Duration(backoffBase * float64(attempt) * float64(time.Second))
			c.logger.Warn().
				Err(lastErr).
				Str("operation", "decisions").
				Int("attempt", attempt).
				Int("maxRetries", maxRetries).
				Dur("backoff", backoff).
				Msg("Retrying Gemini Decisions request")
			if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
				return "", sleepErr
			}
		}
		skipNextBackoff = false

		// Every HTTP attempt (including retries) consumes RPM/TPM/daily quota.
		if waitErr := c.decisionRateLimiter.Wait(ctx, estimatedTokens); waitErr != nil {
			return "", fmt.Errorf("rate limiter wait failed: %w", waitErr)
		}

		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if reqErr != nil {
			return "", fmt.Errorf("failed to build decisions request: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("x-goog-api-key", c.apiKey)
		}

		resp, doErr := c.httpClient.Do(req)
		if doErr != nil {
			reqErr := fmt.Errorf("gemini decisions request failed: %w", doErr)
			if !isRetryableGenAIError(ctx, doErr) {
				return "", reqErr
			}
			lastErr = reqErr
			continue
		}

		respBytes, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			bodyErr := fmt.Errorf("failed to read decisions response body: %w", readErr)
			if !isRetryableGenAIError(ctx, readErr) {
				return "", bodyErr
			}
			lastErr = bodyErr
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("gemini decisions API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
			if attempt < maxRetries {
				retryAfterSec := 5
				if ra := resp.Header.Get("Retry-After"); ra != "" {
					if parsed, pErr := strconv.Atoi(ra); pErr == nil && parsed >= 0 {
						retryAfterSec = parsed
					}
				}
				if sleepErr := sleepWithContext(ctx, time.Duration(retryAfterSec)*time.Second); sleepErr != nil {
					return "", sleepErr
				}
				skipNextBackoff = true
			}
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("gemini decisions API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("gemini decisions API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
		}

		trimmed := strings.TrimSpace(string(respBytes))
		if trimmed == "" || !json.Valid(respBytes) {
			return "", fmt.Errorf("gemini decisions API returned invalid JSON response")
		}

		return trimmed, nil
	}

	return "", fmt.Errorf("gemini decisions API failed after retries: %w", lastErr)
}

func (c *Client) CallDecisions(ctx context.Context, userPrompt string, questions map[string]tagging.DecisionQuestion) (string, error) {
	if len(questions) == 0 {
		return "", fmt.Errorf("no decision questions provided")
	}

	return c.doDecisionRequest(ctx, userPrompt, questions, estimateDecisionTokens(userPrompt, questions))
}

func (c *Client) CallDecisionsWithCache(ctx context.Context, userPrompt string, questions map[string]tagging.DecisionQuestion, useCache bool) (string, error) {
	if len(questions) == 0 {
		return "", fmt.Errorf("no decision questions provided")
	}

	questionsKey, err := serializeQuestionsForHash(questions)
	if err != nil {
		return "", err
	}

	hash := HashPrompt(c.decisionModel, questionsKey, userPrompt)
	if useCache && c.cache != nil {
		if cached, ok := c.cache.Get(hash); ok {
			c.logger.Info().Str("hash", hash[:8]).Msg("[CACHE HIT] Using cached decisions response")
			return cached, nil
		}
	}

	resp, err := c.CallDecisions(ctx, userPrompt, questions)
	if err != nil {
		return "", err
	}

	if useCache && c.cache != nil {
		_ = c.cache.Set(hash, resp)
	}

	return resp, nil
}

func (c *Client) CallDecisionsWithFileURI(ctx context.Context, userPrompt, fileURI string, questions map[string]tagging.DecisionQuestion) (string, error) {
	return c.callDecisionsWithFileURI(ctx, userPrompt, fileURI, questions, pdfFallbackTokens)
}

func (c *Client) callDecisionsWithFileURI(ctx context.Context, userPrompt, fileURI string, questions map[string]tagging.DecisionQuestion, docTokens int) (string, error) {
	if len(questions) == 0 {
		return "", fmt.Errorf("no decision questions provided")
	}
	if strings.TrimSpace(fileURI) == "" {
		return "", fmt.Errorf("uploaded PDF file URI is required")
	}

	estimatedTokens := docTokens + estimateDecisionTokens(userPrompt, questions)

	inputBlocks := []decisionInputPart{
		{
			Type:     "document",
			URI:      fileURI,
			MimeType: "application/pdf",
		},
		{
			Type: "text",
			Text: userPrompt,
		},
	}

	return c.doDecisionRequest(ctx, inputBlocks, questions, estimatedTokens)
}

func (c *Client) CallDecisionsWithPDFProviderAndCache(ctx context.Context, userPrompt, pdfPath string, questions map[string]tagging.DecisionQuestion, fileProvider func() (*genai.File, error), useCache bool) (string, error) {
	if len(questions) == 0 {
		return "", fmt.Errorf("no decision questions provided")
	}

	docTokens := pdfFallbackTokens
	var hash string
	info, infoErr := InspectPDF(pdfPath)
	if infoErr == nil {
		docTokens = info.EstimatedTokens()
	}
	if useCache && c.cache != nil {
		questionsKey, err := serializeQuestionsForHash(questions)
		if err != nil {
			return "", err
		}

		if infoErr == nil {
			hash = hashPromptWithDigest(c.decisionModel, questionsKey, userPrompt, info.SHA256)
			if cached, ok := c.cache.Get(hash); ok {
				c.logger.Info().Str("hash", hash[:8]).Msg("[CACHE HIT] Using cached decisions response")
				return cached, nil
			}
		}
	}

	if fileProvider == nil {
		return "", fmt.Errorf("PDF file provider is required")
	}
	uploadedFile, err := fileProvider()
	if err != nil {
		return "", err
	}
	if uploadedFile == nil || strings.TrimSpace(uploadedFile.URI) == "" {
		return "", fmt.Errorf("uploaded PDF file is missing URI")
	}

	resp, err := c.callDecisionsWithFileURI(ctx, userPrompt, uploadedFile.URI, questions, docTokens)
	if err != nil {
		return "", err
	}

	if hash != "" && useCache && c.cache != nil {
		_ = c.cache.Set(hash, resp)
	}

	return resp, nil
}

func (c *Client) CallDecisionsWithPDF(ctx context.Context, userPrompt, pdfPath string, questions map[string]tagging.DecisionQuestion) (string, error) {
	if len(questions) == 0 {
		return "", fmt.Errorf("no decision questions provided")
	}

	pdfBytes, err := os.ReadFile(filepath.Clean(pdfPath))
	if err != nil {
		return "", fmt.Errorf("failed to read PDF file for decisions API: %w", err)
	}

	estimatedTokens := PDFInfo{Size: int64(len(pdfBytes))}.EstimatedTokens() + estimateDecisionTokens(userPrompt, questions)

	inputBlocks := []decisionInputPart{
		{
			Type:     "document",
			Data:     base64.StdEncoding.EncodeToString(pdfBytes),
			MimeType: "application/pdf",
		},
		{
			Type: "text",
			Text: userPrompt,
		},
	}

	return c.doDecisionRequest(ctx, inputBlocks, questions, estimatedTokens)
}

func (c *Client) CallDecisionsWithPDFAndCache(ctx context.Context, userPrompt, pdfPath string, questions map[string]tagging.DecisionQuestion, useCache bool) (string, error) {
	if len(questions) == 0 {
		return "", fmt.Errorf("no decision questions provided")
	}

	var hash string
	if useCache && c.cache != nil {
		questionsKey, err := serializeQuestionsForHash(questions)
		if err != nil {
			return "", err
		}

		hash, err = HashPDFPrompt(c.decisionModel, questionsKey, userPrompt, pdfPath)
		if err == nil {
			if cached, ok := c.cache.Get(hash); ok {
				c.logger.Info().Str("hash", hash[:8]).Msg("[CACHE HIT] Using cached decisions response")
				return cached, nil
			}
		}
	}

	resp, err := c.CallDecisionsWithPDF(ctx, userPrompt, pdfPath, questions)
	if err != nil {
		return "", err
	}

	if hash != "" && useCache && c.cache != nil {
		_ = c.cache.Set(hash, resp)
	}

	return resp, nil
}
