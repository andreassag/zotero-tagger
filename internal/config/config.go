package config

import (
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

type Config struct {
	LLM     LLMConfig     `mapstructure:"llm"`
	Zotero  ZoteroConfig  `mapstructure:"zotero"`
	Tagging TaggingConfig `mapstructure:"tagging"`
}

type RetryConfig struct {
	MaxRetries  int     `mapstructure:"max_retries"`
	BackoffBase float64 `mapstructure:"backoff_base"`
}

type RateLimitConfig struct {
	RequestsPerMinute int `mapstructure:"requests_per_minute"`
	TokensPerMinute   int `mapstructure:"tokens_per_minute"`
	RequestsPerDay    int `mapstructure:"requests_per_day"`
}

type ExtractConfig struct {
	ModelName   string          `mapstructure:"model_name"`
	Temperature float64         `mapstructure:"temperature"`
	RateLimits  RateLimitConfig `mapstructure:"rate_limits"`
}

type DecisionsConfig struct {
	ModelName            string          `mapstructure:"model_name"`
	ProbabilityThreshold float64         `mapstructure:"probability_threshold"`
	ConfidenceThreshold  float64         `mapstructure:"confidence_threshold"`
	MaxTopics            int             `mapstructure:"max_topics"`
	RateLimits           RateLimitConfig `mapstructure:"rate_limits"`
}

type LLMConfig struct {
	APIKey    string          `mapstructure:"api_key"`
	Extract   ExtractConfig   `mapstructure:"extract"`
	Decisions DecisionsConfig `mapstructure:"decisions"`
	Retries   RetryConfig     `mapstructure:"retries"`
}

type ZoteroConfig struct {
	UserID      string      `mapstructure:"user_id"`
	APIKey      string      `mapstructure:"api_key"`
	LibraryType string      `mapstructure:"library_type"`
	GroupID     string      `mapstructure:"group_id"`
	Retries     RetryConfig `mapstructure:"retries"`
	ItemTypes   ItemTypes   `mapstructure:"item_types"`
}

type ItemTypes struct {
	Allowed []string `mapstructure:"allowed"`
}

type ControlledTopics struct {
	Topics []string `mapstructure:"topics"`
}

type TaggingConfig struct {
	SentinelTag      string           `mapstructure:"sentinel_tag"`
	ControlledTopics ControlledTopics `mapstructure:"controlled_topics"`
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.LLM.Extract.ModelName) == "" {
		return fmt.Errorf("llm.extract.model_name is required in configuration")
	}

	if strings.TrimSpace(c.LLM.Decisions.ModelName) == "" {
		return fmt.Errorf("llm.decisions.model_name is required in configuration")
	}

	if math.IsNaN(c.LLM.Decisions.ProbabilityThreshold) || c.LLM.Decisions.ProbabilityThreshold < 0.0 || c.LLM.Decisions.ProbabilityThreshold > 1.0 {
		return fmt.Errorf("llm.decisions.probability_threshold must be between 0.0 and 1.0, got %v", c.LLM.Decisions.ProbabilityThreshold)
	}

	if math.IsNaN(c.LLM.Decisions.ConfidenceThreshold) || c.LLM.Decisions.ConfidenceThreshold < 0.0 || c.LLM.Decisions.ConfidenceThreshold > 1.0 {
		return fmt.Errorf("llm.decisions.confidence_threshold must be between 0.0 and 1.0, got %v", c.LLM.Decisions.ConfidenceThreshold)
	}

	if c.LLM.Decisions.MaxTopics < 0 {
		return fmt.Errorf("llm.decisions.max_topics must be >= 0, got %d", c.LLM.Decisions.MaxTopics)
	}

	if c.Zotero.LibraryType != "" && c.Zotero.LibraryType != "user" && c.Zotero.LibraryType != "group" {
		return fmt.Errorf("zotero.library_type must be either 'user' or 'group', got %q", c.Zotero.LibraryType)
	}

	hasNonEmptyTopic := false
	for _, topic := range c.Tagging.ControlledTopics.Topics {
		if strings.TrimSpace(topic) != "" {
			hasNonEmptyTopic = true
			break
		}
	}
	if !hasNonEmptyTopic {
		return fmt.Errorf("tagging.controlled_topics.topics must contain at least one controlled topic")
	}

	return nil
}

func LoadConfig(configPath string) (*Config, error) {
	// Try loading .env from current or parent directories
	envPaths := []string{".env", "../.env", "../../.env", "../../../.env"}
	for _, p := range envPaths {
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			break
		}
	}

	v := viper.New()
	v.SetDefault("llm.decisions.probability_threshold", 0.70)
	v.SetDefault("llm.decisions.confidence_threshold", 0.50)
	v.SetDefault("llm.decisions.max_topics", 5)

	v.SetConfigFile(configPath)
	v.SetConfigType("toml")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if envKey, ok := os.LookupEnv("GEMINI_API_KEY"); ok && envKey != "" {
		cfg.LLM.APIKey = envKey
	}
	if envUserID, ok := os.LookupEnv("ZOTERO_USER_ID"); ok && envUserID != "" {
		cfg.Zotero.UserID = envUserID
	}
	if envZoteroKey, ok := os.LookupEnv("ZOTERO_API_KEY"); ok && envZoteroKey != "" {
		cfg.Zotero.APIKey = envZoteroKey
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation error: %w", err)
	}

	return &cfg, nil
}
