package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validBaseConfig() Config {
	return Config{
		LLM: LLMConfig{
			Extract: ExtractConfig{
				ModelName: "gemini-3.5-flash-lite",
			},
			Decisions: DecisionsConfig{
				ModelName:            "gemini-decision-1",
				ProbabilityThreshold: 0.70,
				ConfidenceThreshold:  0.50,
				MaxTopics:            5,
			},
		},
		Zotero: ZoteroConfig{
			LibraryType: "user",
		},
		Tagging: TaggingConfig{
			ControlledTopics: ControlledTopics{
				Topics: []string{"biofilm", "pcr"},
			},
		},
	}
}

func TestConfig_Validate_Success(t *testing.T) {
	cfg := validBaseConfig()
	assert.NoError(t, cfg.Validate())
}

func TestConfig_Validate_MissingExtractModelName(t *testing.T) {
	cfg := validBaseConfig()
	cfg.LLM.Extract.ModelName = ""

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "llm.extract.model_name is required")
}

func TestConfig_Validate_MissingDecisionsModelName(t *testing.T) {
	cfg := validBaseConfig()
	cfg.LLM.Decisions.ModelName = "   "

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "llm.decisions.model_name is required")
}

func TestConfig_Validate_InvalidThresholdsAndMaxTopics(t *testing.T) {
	t.Run("probability_threshold below 0", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.LLM.Decisions.ProbabilityThreshold = -0.01
		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "llm.decisions.probability_threshold")
	})

	t.Run("probability_threshold above 1", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.LLM.Decisions.ProbabilityThreshold = 1.01
		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "llm.decisions.probability_threshold")
	})

	t.Run("confidence_threshold below 0", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.LLM.Decisions.ConfidenceThreshold = -0.1
		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "llm.decisions.confidence_threshold")
	})

	t.Run("confidence_threshold above 1", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.LLM.Decisions.ConfidenceThreshold = 1.5
		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "llm.decisions.confidence_threshold")
	})

	t.Run("max_topics negative", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.LLM.Decisions.MaxTopics = -1
		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "llm.decisions.max_topics")
	})

	t.Run("max_topics zero allowed", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.LLM.Decisions.MaxTopics = 0
		assert.NoError(t, cfg.Validate())
	})
}

func TestConfig_Validate_InvalidLibraryType(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Zotero.LibraryType = "invalid_type"

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "zotero.library_type must be either 'user' or 'group'")
}

func TestConfig_Validate_EmptyTopics(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Zotero.LibraryType = "group"
	cfg.Tagging.ControlledTopics.Topics = []string{}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must contain at least one controlled topic")

	cfg.Tagging.ControlledTopics.Topics = []string{"", "   "}
	err = cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must contain at least one controlled topic")
}

func TestLoadConfig_Success(t *testing.T) {
	tomlContent := `
[llm.extract]
model_name = "gemini-3.5-flash-lite"
temperature = 0.2

[llm.extract.rate_limits]
requests_per_minute = 10
tokens_per_minute = 200000
requests_per_day = 400

[llm.decisions]
model_name = "gemini-decision-1"
probability_threshold = 0.80
confidence_threshold = 0.60
max_topics = 3

[llm.decisions.rate_limits]
requests_per_minute = 200
tokens_per_minute = 50000
requests_per_day = 10000

[llm.retries]
max_retries = 4
backoff_base = 1.5

[zotero]
library_type = "user"
user_id = "test_user"

[tagging.controlled_topics]
topics = ["biofilm", "oral microbiology"]
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	err := os.WriteFile(configPath, []byte(tomlContent), 0600)
	require.NoError(t, err)

	t.Setenv("ZOTERO_USER_ID", "")
	t.Setenv("ZOTERO_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	assert.Equal(t, "gemini-3.5-flash-lite", cfg.LLM.Extract.ModelName)
	assert.Equal(t, 0.2, cfg.LLM.Extract.Temperature)
	assert.Equal(t, 10, cfg.LLM.Extract.RateLimits.RequestsPerMinute)
	assert.Equal(t, 200000, cfg.LLM.Extract.RateLimits.TokensPerMinute)
	assert.Equal(t, 400, cfg.LLM.Extract.RateLimits.RequestsPerDay)

	assert.Equal(t, "gemini-decision-1", cfg.LLM.Decisions.ModelName)
	assert.Equal(t, 0.80, cfg.LLM.Decisions.ProbabilityThreshold)
	assert.Equal(t, 0.60, cfg.LLM.Decisions.ConfidenceThreshold)
	assert.Equal(t, 3, cfg.LLM.Decisions.MaxTopics)
	assert.Equal(t, 200, cfg.LLM.Decisions.RateLimits.RequestsPerMinute)
	assert.Equal(t, 50000, cfg.LLM.Decisions.RateLimits.TokensPerMinute)
	assert.Equal(t, 10000, cfg.LLM.Decisions.RateLimits.RequestsPerDay)

	assert.Equal(t, 4, cfg.LLM.Retries.MaxRetries)
	assert.Equal(t, 1.5, cfg.LLM.Retries.BackoffBase)

	assert.Equal(t, "user", cfg.Zotero.LibraryType)
	assert.Equal(t, "test_user", cfg.Zotero.UserID)
	assert.Equal(t, []string{"biofilm", "oral microbiology"}, cfg.Tagging.ControlledTopics.Topics)
}

func TestLoadConfig_Defaults(t *testing.T) {
	tomlContent := `
[llm.extract]
model_name = "gemini-3.5-flash-lite"

[llm.decisions]
model_name = "gemini-decision-1"

[zotero]
library_type = "user"

[tagging.controlled_topics]
topics = ["biofilm"]
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	err := os.WriteFile(configPath, []byte(tomlContent), 0600)
	require.NoError(t, err)

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	assert.InDelta(t, 0.70, cfg.LLM.Decisions.ProbabilityThreshold, 1e-9)
	assert.InDelta(t, 0.50, cfg.LLM.Decisions.ConfidenceThreshold, 1e-9)
	assert.Equal(t, 5, cfg.LLM.Decisions.MaxTopics)
}

func TestLoadConfig_ExplicitZeroMaxTopics(t *testing.T) {
	tomlContent := `
[llm.extract]
model_name = "gemini-3.5-flash-lite"

[llm.decisions]
model_name = "gemini-decision-1"
max_topics = 0

[zotero]
library_type = "user"

[tagging.controlled_topics]
topics = ["biofilm"]
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	err := os.WriteFile(configPath, []byte(tomlContent), 0600)
	require.NoError(t, err)

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	assert.Equal(t, 0, cfg.LLM.Decisions.MaxTopics)
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	tomlContent := `
[llm.extract]
model_name = "gemini-3.5-flash-lite"

[llm.decisions]
model_name = "gemini-decision-1"

[zotero]
library_type = "user"

[tagging.controlled_topics]
topics = ["biofilm"]
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	err := os.WriteFile(configPath, []byte(tomlContent), 0600)
	require.NoError(t, err)

	t.Setenv("GEMINI_API_KEY", "env_gemini_key")
	t.Setenv("ZOTERO_USER_ID", "env_user_id")
	t.Setenv("ZOTERO_API_KEY", "env_zotero_key")

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	assert.Equal(t, "env_gemini_key", cfg.LLM.APIKey)
	assert.Equal(t, "env_user_id", cfg.Zotero.UserID)
	assert.Equal(t, "env_zotero_key", cfg.Zotero.APIKey)
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("nonexistent_path_to_config.toml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read config file")
}
