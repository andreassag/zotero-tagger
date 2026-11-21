package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andreassag/zotero-tagger/internal/config"
	"github.com/andreassag/zotero-tagger/internal/display"
	"github.com/andreassag/zotero-tagger/internal/llm"
	"github.com/andreassag/zotero-tagger/internal/zotero"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func newTestRunner(zoteroURL, geminiURL string, httpClient *http.Client) (*Runner, error) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Extract: config.ExtractConfig{
				ModelName:   "gemini-3.5-flash-lite",
				Temperature: 0.0,
				RateLimits: config.RateLimitConfig{
					RequestsPerMinute: 0,
					TokensPerMinute:   0,
					RequestsPerDay:    0,
				},
			},
			Decisions: config.DecisionsConfig{
				ModelName:            "gemini-decision-1",
				ProbabilityThreshold: 0.70,
				ConfidenceThreshold:  0.50,
				MaxTopics:            5,
				RateLimits: config.RateLimitConfig{
					RequestsPerMinute: 0,
					TokensPerMinute:   0,
					RequestsPerDay:    0,
				},
			},
		},
		Zotero: config.ZoteroConfig{
			UserID:      "12345",
			APIKey:      "test-key",
			LibraryType: "user",
			ItemTypes: config.ItemTypes{
				Allowed: []string{"journalArticle"},
			},
		},
		Tagging: config.TaggingConfig{
			SentinelTag: "_ai-tagged",
			ControlledTopics: config.ControlledTopics{
				Topics: []string{"biofilm", "pcr"},
			},
		},
	}

	zClient := zotero.NewClient(cfg.Zotero)
	zClient.SetBaseURLForTest(zoteroURL)

	genaiClient, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:     "test-api-key",
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: httpClient,
		HTTPOptions: genai.HTTPOptions{
			BaseURL: geminiURL,
		},
	})
	if err != nil {
		return nil, err
	}

	llmClient := llm.NewClientForTest(
		genaiClient,
		httpClient,
		geminiURL,
		cfg.LLM.Extract.ModelName,
		cfg.LLM.Extract.Temperature,
		cfg.LLM.Decisions.ModelName,
	)

	return &Runner{
		cfg:          cfg,
		zoteroClient: zClient,
		llmClient:    llmClient,
		display:      display.NewOutput(),
		logger:       zerolog.Nop(),
	}, nil
}

func TestRunner_Run_NoItems(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	zoteroServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]zotero.Item{})
	}))
	defer zoteroServer.Close()

	runner, err := newTestRunner(zoteroServer.URL, "http://localhost", http.DefaultClient)
	require.NoError(t, err)

	err = runner.Run(context.Background(), Options{DryRun: true})
	assert.NoError(t, err)
}

func TestRunner_Run_SkipLLM(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	zoteroServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/12345/items/top" {
			items := []zotero.Item{
				{
					Key:     "ITEM1",
					Version: 1,
					Data: zotero.ItemData{
						ItemType:     "journalArticle",
						Title:        "Biofilm Study",
						AbstractNote: "This is an abstract about biofilm.",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)
			return
		}
		if r.URL.Path == "/users/12345/items/ITEM1/children" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]zotero.Item{})
			return
		}
	}))
	defer zoteroServer.Close()

	runner, err := newTestRunner(zoteroServer.URL, "http://localhost", http.DefaultClient)
	require.NoError(t, err)

	err = runner.Run(context.Background(), Options{
		SkipLLM: true,
	})
	assert.NoError(t, err)
}

func TestRunner_Run_SuccessDryRun(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	zoteroServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/12345/items/top" {
			items := []zotero.Item{
				{
					Key:     "ITEM1",
					Version: 1,
					Data: zotero.ItemData{
						ItemType:     "journalArticle",
						Title:        "Biofilm Study",
						AbstractNote: "This is an abstract about biofilm.",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)
			return
		}
		if r.URL.Path == "/users/12345/items/ITEM1/children" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]zotero.Item{})
			return
		}
	}))
	defer zoteroServer.Close()

	var extractCalled, decisionsCalled atomic.Bool
	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1beta/decisions" {
			decisionsCalled.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"answers": map[string]interface{}{
					"biofilm": map[string]interface{}{"type": "noul", "noul": 0.94, "confidence": 0.88},
					"pcr":     map[string]interface{}{"type": "noul", "noul": 0.12, "confidence": 0.91},
				},
			})
			return
		}
		if r.URL.Path == "/v1beta/models/gemini-3.5-flash-lite:generateContent" {
			extractCalled.Store(true)
			resp := map[string]interface{}{
				"candidates": []map[string]interface{}{
					{
						"content": map[string]interface{}{
							"parts": []map[string]interface{}{
								{
									"text": `{"org_tags":["org:pseudomonas-aeruginosa"],"group_tags":["group:gram-negative"]}`,
								},
							},
							"role": "model",
						},
						"finishReason": "STOP",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer geminiServer.Close()

	runner, err := newTestRunner(zoteroServer.URL, geminiServer.URL, geminiServer.Client())
	require.NoError(t, err)

	err = runner.Run(context.Background(), Options{
		DryRun: true,
	})
	assert.NoError(t, err)
	assert.True(t, extractCalled.Load(), "expected generateContent endpoint to be called")
	assert.True(t, decisionsCalled.Load(), "expected /v1beta/decisions endpoint to be called")
}

func TestRunner_Run_AppliesCombinedTags(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var patchedTags []zotero.Tag
	zoteroServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/12345/items/top" {
			items := []zotero.Item{
				{
					Key:     "ITEM1",
					Version: 3,
					Data: zotero.ItemData{
						ItemType:     "journalArticle",
						Title:        "Biofilm Study",
						AbstractNote: "This is an abstract about Pseudomonas aeruginosa biofilm.",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)
			return
		}
		if r.URL.Path == "/users/12345/items/ITEM1/children" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]zotero.Item{})
			return
		}
		if r.URL.Path == "/users/12345/items/ITEM1" && r.Method == http.MethodPatch {
			var body struct {
				Tags []zotero.Tag `json:"tags"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			patchedTags = body.Tags
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer zoteroServer.Close()

	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1beta/decisions" {
			var request struct {
				Questions map[string]struct {
					Instructions string `json:"instructions"`
				} `json:"questions"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			require.Contains(t, request.Questions, "_topic_2")
			assert.Contains(t, request.Questions["_topic_2"].Instructions, `"oral microbiology"`)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"answers": map[string]interface{}{
					"biofilm":  map[string]interface{}{"type": "noul", "noul": 0.90, "confidence": 0.85},
					"pcr":      map[string]interface{}{"type": "noul", "noul": 0.40, "confidence": 0.90},
					"_topic_2": map[string]interface{}{"type": "noul", "noul": 0.80, "confidence": 0.90},
				},
			})
			return
		}
		if r.URL.Path == "/v1beta/models/gemini-3.5-flash-lite:generateContent" {
			resp := map[string]interface{}{
				"candidates": []map[string]interface{}{
					{
						"content": map[string]interface{}{
							"parts": []map[string]interface{}{
								{
									"text": `{"org_tags":["org:pseudomonas-aeruginosa"],"group_tags":["group:gram-negative"]}`,
								},
							},
							"role": "model",
						},
						"finishReason": "STOP",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer geminiServer.Close()

	runner, err := newTestRunner(zoteroServer.URL, geminiServer.URL, geminiServer.Client())
	require.NoError(t, err)
	runner.cfg.Tagging.ControlledTopics.Topics = append(runner.cfg.Tagging.ControlledTopics.Topics, "oral microbiology")

	err = runner.Run(context.Background(), Options{DryRun: false})
	require.NoError(t, err)

	var tagStrings []string
	for _, tg := range patchedTags {
		tagStrings = append(tagStrings, tg.Tag)
	}
	assert.Equal(t, []string{
		"org:pseudomonas-aeruginosa",
		"group:gram-negative",
		"topic:biofilm",
		"topic:oral-microbiology",
		"_ai-tagged",
	}, tagStrings)
}

func TestRunner_Run_DecisionsErrorAtomic(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var patchCalled atomic.Bool
	zoteroServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/12345/items/top" {
			items := []zotero.Item{
				{
					Key:     "ITEM1",
					Version: 1,
					Data: zotero.ItemData{
						ItemType:     "journalArticle",
						Title:        "Biofilm Study",
						AbstractNote: "This is an abstract about biofilm.",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)
			return
		}
		if r.URL.Path == "/users/12345/items/ITEM1/children" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]zotero.Item{})
			return
		}
		if r.URL.Path == "/users/12345/items/ITEM1" && r.Method == http.MethodPatch {
			patchCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}))
	defer zoteroServer.Close()

	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1beta/decisions" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal server error"}`))
			return
		}
		if r.URL.Path == "/v1beta/models/gemini-3.5-flash-lite:generateContent" {
			resp := map[string]interface{}{
				"candidates": []map[string]interface{}{
					{
						"content": map[string]interface{}{
							"parts": []map[string]interface{}{
								{
									"text": `{"org_tags":["org:pseudomonas-aeruginosa"],"group_tags":["group:gram-negative"]}`,
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
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer geminiServer.Close()

	runner, err := newTestRunner(zoteroServer.URL, geminiServer.URL, geminiServer.Client())
	require.NoError(t, err)

	err = runner.Run(context.Background(), Options{DryRun: false})
	assert.NoError(t, err)
	assert.False(t, patchCalled.Load(), "Zotero tags must not be updated when decisions call fails")
}

func TestRunner_Run_DecisionsInvalidJSONAtomic(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var patchCalled atomic.Bool
	zoteroServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/12345/items/top" {
			items := []zotero.Item{
				{
					Key:     "ITEM1",
					Version: 1,
					Data: zotero.ItemData{
						ItemType:     "journalArticle",
						Title:        "Biofilm Study",
						AbstractNote: "This is an abstract about biofilm.",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)
			return
		}
		if r.URL.Path == "/users/12345/items/ITEM1/children" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]zotero.Item{})
			return
		}
		if r.URL.Path == "/users/12345/items/ITEM1" && r.Method == http.MethodPatch {
			patchCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}))
	defer zoteroServer.Close()

	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1beta/decisions" {
			_, _ = w.Write([]byte(`{"unexpected":"schema"}`))
			return
		}
		if r.URL.Path == "/v1beta/models/gemini-3.5-flash-lite:generateContent" {
			resp := map[string]interface{}{
				"candidates": []map[string]interface{}{
					{
						"content": map[string]interface{}{
							"parts": []map[string]interface{}{
								{
									"text": `{"org_tags":["org:pseudomonas-aeruginosa"],"group_tags":["group:gram-negative"]}`,
								},
							},
							"role": "model",
						},
						"finishReason": "STOP",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer geminiServer.Close()

	runner, err := newTestRunner(zoteroServer.URL, geminiServer.URL, geminiServer.Client())
	require.NoError(t, err)

	err = runner.Run(context.Background(), Options{DryRun: false})
	assert.NoError(t, err)
	assert.False(t, patchCalled.Load(), "Zotero tags must not be updated when decisions response lacks answers")
}

func TestRunner_Run_Cancellation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	zoteroServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := []zotero.Item{
			{
				Key:     "ITEM1",
				Version: 1,
				Data: zotero.ItemData{
					ItemType:     "journalArticle",
					Title:        "Biofilm Study",
					AbstractNote: "Abstract text",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	}))
	defer zoteroServer.Close()

	runner, err := newTestRunner(zoteroServer.URL, "http://localhost", http.DefaultClient)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err = runner.Run(ctx, Options{
		DryRun: true,
	})
	assert.NoError(t, err)
}

func newPDFZoteroServer(t *testing.T, patchedTags *[]zotero.Tag, patchCalled *atomic.Bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/users/12345/items/top":
			items := []zotero.Item{
				{
					Key:     "ITEM1",
					Version: 7,
					Data: zotero.ItemData{
						ItemType: "journalArticle",
						Title:    "Shared PDF Study",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)
		case r.URL.Path == "/users/12345/items/ITEM1/children":
			children := []zotero.Item{
				{
					Key: "ATT1",
					Data: zotero.ItemData{
						ItemType:    "attachment",
						Filename:    "paper.pdf",
						ContentType: "application/pdf",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(children)
		case r.URL.Path == "/users/12345/items/ATT1/file":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.4 mock pdf content"))
		case r.URL.Path == "/users/12345/items/ITEM1" && r.Method == http.MethodPatch:
			if patchCalled != nil {
				patchCalled.Store(true)
			}
			if patchedTags != nil {
				var body struct {
					Tags []zotero.Tag `json:"tags"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				*patchedTags = body.Tags
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestRunner_Run_PDFSharedUploadAndConcurrentExecution(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	var patchedTags []zotero.Tag
	var patchCalled atomic.Bool
	zoteroServer := newPDFZoteroServer(t, &patchedTags, &patchCalled)
	defer zoteroServer.Close()

	var uploadStartCalls atomic.Int32
	var uploadFinalCalls atomic.Int32
	var extractCalls atomic.Int32
	var decisionsCalls atomic.Int32
	var deleteCalls atomic.Int32
	var deletedBeforeBothDone atomic.Bool

	// Rendezvous barrier to prove extract and decisions are in-flight simultaneously.
	var inflightWG sync.WaitGroup
	inflightWG.Add(2)
	bothEntered := make(chan struct{})
	go func() {
		inflightWG.Wait()
		close(bothEntered)
	}()

	var mu sync.Mutex
	var extractDone, decisionsDone bool

	const sharedFileURI = "https://generativelanguage.googleapis.com/v1beta/files/shared123"
	var geminiURL string
	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/upload/v1beta/files":
			if r.Header.Get("X-Goog-Upload-Command") == "start" {
				uploadStartCalls.Add(1)
				w.Header().Set("X-Goog-Upload-Url", geminiURL+"/upload/v1beta/files?upload_id=u1")
				w.WriteHeader(http.StatusOK)
				return
			}
			uploadFinalCalls.Add(1)
			w.Header().Set("X-Goog-Upload-Status", "final")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"file":{"name":"files/shared123","uri":"` + sharedFileURI + `","mimeType":"application/pdf"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1beta/models/gemini-3.5-flash-lite:generateContent":
			extractCalls.Add(1)
			var reqBody map[string]interface{}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
			contents := reqBody["contents"].([]interface{})
			parts := contents[0].(map[string]interface{})["parts"].([]interface{})
			fileData := parts[0].(map[string]interface{})["fileData"].(map[string]interface{})
			assert.Equal(t, sharedFileURI, fileData["fileUri"])

			inflightWG.Done()
			select {
			case <-bothEntered:
			case <-time.After(2 * time.Second):
				t.Error("expected extract and decisions to execute concurrently")
			}

			mu.Lock()
			extractDone = true
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"candidates": []map[string]interface{}{
					{
						"content": map[string]interface{}{
							"parts": []map[string]interface{}{
								{"text": `{"org_tags":["org:streptococcus-mutans"],"group_tags":["group:streptococcaceae"]}`},
							},
							"role": "model",
						},
						"finishReason": "STOP",
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1beta/decisions":
			decisionsCalls.Add(1)
			var reqBody struct {
				Model string `json:"model"`
				Input []struct {
					Type     string `json:"type"`
					Data     string `json:"data"`
					URI      string `json:"uri"`
					MimeType string `json:"mime_type"`
					Text     string `json:"text"`
				} `json:"input"`
				Questions map[string]struct {
					Instructions string `json:"instructions"`
				} `json:"questions"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
			require.Len(t, reqBody.Input, 2)
			assert.Equal(t, "document", reqBody.Input[0].Type)
			assert.Equal(t, sharedFileURI, reqBody.Input[0].URI)
			assert.Empty(t, reqBody.Input[0].Data)
			assert.Equal(t, "application/pdf", reqBody.Input[0].MimeType)
			require.Contains(t, reqBody.Questions, "_topic_2")
			assert.Contains(t, reqBody.Questions["_topic_2"].Instructions, `"whole genome sequencing"`)

			inflightWG.Done()
			select {
			case <-bothEntered:
			case <-time.After(2 * time.Second):
				t.Error("expected extract and decisions to execute concurrently")
			}

			mu.Lock()
			decisionsDone = true
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"answers": map[string]interface{}{
					"biofilm":  map[string]interface{}{"type": "noul", "noul": 0.92, "confidence": 0.89},
					"pcr":      map[string]interface{}{"type": "noul", "noul": 0.25, "confidence": 0.90},
					"_topic_2": map[string]interface{}{"type": "noul", "noul": 0.80, "confidence": 0.90},
				},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1beta/files/shared123":
			mu.Lock()
			if !extractDone || !decisionsDone {
				deletedBeforeBothDone.Store(true)
			}
			mu.Unlock()
			deleteCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer geminiServer.Close()
	geminiURL = geminiServer.URL

	runner, err := newTestRunner(zoteroServer.URL, geminiServer.URL, geminiServer.Client())
	require.NoError(t, err)
	runner.cfg.Tagging.ControlledTopics.Topics = append(runner.cfg.Tagging.ControlledTopics.Topics, "whole genome sequencing")

	err = runner.Run(context.Background(), Options{DryRun: false})
	require.NoError(t, err)

	assert.Equal(t, int32(1), uploadStartCalls.Load())
	assert.Equal(t, int32(1), uploadFinalCalls.Load())
	assert.Equal(t, int32(1), extractCalls.Load())
	assert.Equal(t, int32(1), decisionsCalls.Load())
	assert.Equal(t, int32(1), deleteCalls.Load())
	assert.False(t, deletedBeforeBothDone.Load(), "uploaded PDF must not be deleted until both concurrent calls finish")
	assert.True(t, patchCalled.Load())

	var tagStrings []string
	for _, tg := range patchedTags {
		tagStrings = append(tagStrings, tg.Tag)
	}
	assert.Equal(t, []string{
		"org:streptococcus-mutans",
		"group:streptococcaceae",
		"topic:biofilm",
		"topic:whole-genome-sequencing",
		"_ai-tagged",
	}, tagStrings)
}

func TestRunner_Run_PDFCacheHitSkipsUploadAndPartialCacheUploadsOnce(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	zoteroServer := newPDFZoteroServer(t, nil, nil)
	defer zoteroServer.Close()

	var uploadCalls atomic.Int32
	var extractCalls atomic.Int32
	var decisionsCalls atomic.Int32
	var deleteCalls atomic.Int32

	var geminiURL string
	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/upload/v1beta/files":
			if r.Header.Get("X-Goog-Upload-Command") == "start" {
				uploadCalls.Add(1)
				w.Header().Set("X-Goog-Upload-Url", geminiURL+"/upload/v1beta/files?upload_id=u1")
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("X-Goog-Upload-Status", "final")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"file":{"name":"files/cached123","uri":"https://generativelanguage.googleapis.com/v1beta/files/cached123","mimeType":"application/pdf"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1beta/models/gemini-3.5-flash-lite:generateContent":
			extractCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
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
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1beta/decisions":
			decisionsCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"answers": map[string]interface{}{
					"biofilm": map[string]interface{}{"type": "noul", "noul": 0.85, "confidence": 0.85},
				},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1beta/files/cached123":
			deleteCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer geminiServer.Close()
	geminiURL = geminiServer.URL

	runner, err := newTestRunner(zoteroServer.URL, geminiServer.URL, geminiServer.Client())
	require.NoError(t, err)

	// Run 1: misses both caches -> 1 upload, 1 extract, 1 decisions, 1 delete.
	require.NoError(t, runner.Run(context.Background(), Options{DryRun: true, UseCache: true}))
	assert.Equal(t, int32(1), uploadCalls.Load())
	assert.Equal(t, int32(1), extractCalls.Load())
	assert.Equal(t, int32(1), decisionsCalls.Load())
	assert.Equal(t, int32(1), deleteCalls.Load())

	// Run 2: hits both caches -> 0 additional uploads, 0 additional model calls, 0 additional deletes.
	require.NoError(t, runner.Run(context.Background(), Options{DryRun: true, UseCache: true}))
	assert.Equal(t, int32(1), uploadCalls.Load())
	assert.Equal(t, int32(1), extractCalls.Load())
	assert.Equal(t, int32(1), decisionsCalls.Load())
	assert.Equal(t, int32(1), deleteCalls.Load())

	// Run 3: change controlled topics so decisions misses cache while extract still hits -> 1 new upload, 0 new extract, 1 new decisions, 1 new delete.
	runner.cfg.Tagging.ControlledTopics.Topics = []string{"biofilm", "pcr", "genomics"}
	require.NoError(t, runner.Run(context.Background(), Options{DryRun: true, UseCache: true}))
	assert.Equal(t, int32(2), uploadCalls.Load())
	assert.Equal(t, int32(1), extractCalls.Load())
	assert.Equal(t, int32(2), decisionsCalls.Load())
	assert.Equal(t, int32(2), deleteCalls.Load())
}

func TestRunner_Run_PDFDecisionsErrorAtomicAndDeletesFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	var patchCalled atomic.Bool
	zoteroServer := newPDFZoteroServer(t, nil, &patchCalled)
	defer zoteroServer.Close()

	var deleteCalls atomic.Int32
	var geminiURL string
	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/upload/v1beta/files":
			if r.Header.Get("X-Goog-Upload-Command") == "start" {
				w.Header().Set("X-Goog-Upload-Url", geminiURL+"/upload/v1beta/files?upload_id=u1")
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("X-Goog-Upload-Status", "final")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"file":{"name":"files/fail123","uri":"https://generativelanguage.googleapis.com/v1beta/files/fail123","mimeType":"application/pdf"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1beta/models/gemini-3.5-flash-lite:generateContent":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
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
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1beta/decisions":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal error"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1beta/files/fail123":
			deleteCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer geminiServer.Close()
	geminiURL = geminiServer.URL

	runner, err := newTestRunner(zoteroServer.URL, geminiServer.URL, geminiServer.Client())
	require.NoError(t, err)

	err = runner.Run(context.Background(), Options{DryRun: false})
	require.NoError(t, err)
	assert.False(t, patchCalled.Load(), "Zotero tags must not be updated when decisions call fails")
	assert.Equal(t, int32(1), deleteCalls.Load(), "uploaded PDF must still be deleted on failure")
}

func TestRunner_Run_AbstractConcurrentExecution(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	zoteroServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/12345/items/top" {
			items := []zotero.Item{
				{
					Key:     "ITEM1",
					Version: 1,
					Data: zotero.ItemData{
						ItemType:     "journalArticle",
						Title:        "Abstract Concurrency Study",
						AbstractNote: "Study on Streptococcus mutans biofilm formation.",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)
			return
		}
		if r.URL.Path == "/users/12345/items/ITEM1/children" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]zotero.Item{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer zoteroServer.Close()

	var inflightWG sync.WaitGroup
	inflightWG.Add(2)
	bothEntered := make(chan struct{})
	go func() {
		inflightWG.Wait()
		close(bothEntered)
	}()

	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1beta/models/gemini-3.5-flash-lite:generateContent":
			inflightWG.Done()
			select {
			case <-bothEntered:
			case <-time.After(2 * time.Second):
				t.Error("expected abstract extract and decisions to execute concurrently")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"candidates": []map[string]interface{}{
					{
						"content": map[string]interface{}{
							"parts": []map[string]interface{}{
								{"text": `{"org_tags":["org:streptococcus-mutans"],"group_tags":[]}`},
							},
							"role": "model",
						},
						"finishReason": "STOP",
					},
				},
			})
		case "/v1beta/decisions":
			inflightWG.Done()
			select {
			case <-bothEntered:
			case <-time.After(2 * time.Second):
				t.Error("expected abstract extract and decisions to execute concurrently")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"answers": map[string]interface{}{
					"biofilm": map[string]interface{}{"type": "noul", "noul": 0.90, "confidence": 0.85},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer geminiServer.Close()

	runner, err := newTestRunner(zoteroServer.URL, geminiServer.URL, geminiServer.Client())
	require.NoError(t, err)

	require.NoError(t, runner.Run(context.Background(), Options{DryRun: true}))
}

func TestRunner_Run_PDFExtractErrorAtomicAndDeletesFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	var patchCalled atomic.Bool
	zoteroServer := newPDFZoteroServer(t, nil, &patchCalled)
	defer zoteroServer.Close()

	var deleteCalls atomic.Int32
	var geminiURL string
	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/upload/v1beta/files":
			if r.Header.Get("X-Goog-Upload-Command") == "start" {
				w.Header().Set("X-Goog-Upload-Url", geminiURL+"/upload/v1beta/files?upload_id=u1")
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("X-Goog-Upload-Status", "final")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"file":{"name":"files/extfail123","uri":"https://generativelanguage.googleapis.com/v1beta/files/extfail123","mimeType":"application/pdf"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1beta/models/gemini-3.5-flash-lite:generateContent":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"bad request","status":"INVALID_ARGUMENT"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1beta/decisions":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"answers": map[string]interface{}{
					"biofilm": map[string]interface{}{"type": "noul", "noul": 0.90, "confidence": 0.85},
				},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1beta/files/extfail123":
			deleteCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer geminiServer.Close()
	geminiURL = geminiServer.URL

	runner, err := newTestRunner(zoteroServer.URL, geminiServer.URL, geminiServer.Client())
	require.NoError(t, err)

	err = runner.Run(context.Background(), Options{DryRun: false})
	require.NoError(t, err)
	assert.False(t, patchCalled.Load(), "Zotero tags must not be updated when extract call fails")
	assert.Equal(t, int32(1), deleteCalls.Load(), "uploaded PDF must still be deleted when extract fails")
}
