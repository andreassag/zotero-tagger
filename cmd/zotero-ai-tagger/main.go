package main

import (
	"context"
	"fmt"
	"os"

	"github.com/exterex/zotero-tagger/internal/config"
	"github.com/exterex/zotero-tagger/internal/pipeline"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var (
	opts pipeline.Options
)

var rootCmd = &cobra.Command{
	Use:   "zotero-ai-tagger",
	Short: "Automatically tag academic papers in Zotero libraries using LLM taxonomy extraction",
}

var tagCmd = &cobra.Command{
	Use:   "tag",
	Short: "Run the fetching, processing, tagging, and sync pipeline",
	RunE: func(cmd *cobra.Command, args []string) error {
		logLevel := zerolog.InfoLevel
		if opts.Verbose {
			logLevel = zerolog.DebugLevel
		}
		logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).
			Level(logLevel).
			With().
			Timestamp().
			Logger()

		cfg, err := config.LoadConfig(opts.ConfigPath)
		if err != nil {
			return fmt.Errorf("failed to load configuration: %w", err)
		}

		runner := pipeline.NewRunner(cfg, logger)
		return runner.Run(context.Background(), opts)
	},
}

func init() {
	tagCmd.Flags().StringVar(&opts.ConfigPath, "config", "config/config.toml", "Path to config file")
	tagCmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Preview tags without updating Zotero")
	tagCmd.Flags().IntVar(&opts.Limit, "limit", 0, "Max items to process (0 = all)")
	tagCmd.Flags().StringVar(&opts.CollectionKey, "collection", "", "Zotero collection key")
	tagCmd.Flags().StringVar(&opts.GroupID, "group", "", "Zotero group ID")
	tagCmd.Flags().StringVar(&opts.ItemKey, "item", "", "Single Zotero item key to process")
	tagCmd.Flags().BoolVar(&opts.Batch, "batch", false, "Use LLM batch API mode")
	tagCmd.Flags().IntVar(&opts.Concurrent, "concurrent", 1, "Number of concurrent workers")
	tagCmd.Flags().BoolVar(&opts.Reprocess, "reprocess", false, "Reprocess items even if sentinel tag exists")
	tagCmd.Flags().BoolVar(&opts.Verbose, "verbose", false, "Enable DEBUG logging")

	rootCmd.AddCommand(tagCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
