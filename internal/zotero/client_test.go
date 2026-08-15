package zotero

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andreassag/zotero-tagger/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestClient_FetchItems(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "3", r.Header.Get("Zotero-API-Version"))
		assert.Equal(t, "test-key", r.Header.Get("Zotero-API-Key"))

		items := []Item{
			{
				Key:     "ITEM1",
				Version: 1,
				Data: ItemData{
					ItemType: "journalArticle",
					Title:    "Test Paper 1",
				},
			},
			{
				Key:     "ITEM2",
				Version: 1,
				Data: ItemData{
					ItemType: "book",
					Title:    "Test Book",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	}))
	defer mockServer.Close()

	cfg := config.ZoteroConfig{
		UserID:      "12345",
		APIKey:      "test-key",
		LibraryType: "user",
	}

	client := NewClient(cfg)
	client.baseURL = mockServer.URL

	items, err := client.FetchItems(context.Background(), FetchOptions{
		ItemTypes: []string{"journalArticle", "book"},
	})

	assert.NoError(t, err)
	assert.Len(t, items, 2)
	assert.Equal(t, "ITEM1", items[0].Key)
	assert.Equal(t, "ITEM2", items[1].Key)
}

func TestClient_UpdateTags(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PATCH", r.Method)
		assert.Equal(t, "1", r.Header.Get("If-Unmodified-Since-Version"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer mockServer.Close()

	cfg := config.ZoteroConfig{
		UserID:      "12345",
		APIKey:      "test-key",
		LibraryType: "user",
	}

	client := NewClient(cfg)
	client.baseURL = mockServer.URL

	tags := []Tag{{Tag: "org:escherichia-coli"}, {Tag: "_ai-tagged"}}
	err := client.UpdateTags(context.Background(), "ITEM1", 1, tags, "")

	assert.NoError(t, err)
}
