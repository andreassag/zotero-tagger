package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/andreassag/zotero-tagger/internal/config"
	"github.com/andreassag/zotero-tagger/internal/display"
	"github.com/andreassag/zotero-tagger/internal/llm"
	"github.com/andreassag/zotero-tagger/internal/tagging"
	"github.com/andreassag/zotero-tagger/internal/zotero"
	"github.com/rs/zerolog"
	"google.golang.org/genai"
)

type Options struct {
	ConfigPath    string
	DryRun        bool
	Limit         int
	CollectionKey string
	GroupID       string
	ItemKey       string
	Concurrent    int
	Reprocess     bool
	Verbose       bool
	JSONLog       bool
	UseCache      bool
	SkipLLM       bool
}

type Runner struct {
	cfg          *config.Config
	zoteroClient *zotero.Client
	llmClient    *llm.Client
	display      *display.Output
	logger       zerolog.Logger
}

func NewRunner(ctx context.Context, cfg *config.Config, logger zerolog.Logger) (*Runner, error) {
	llmClient, err := llm.NewClient(ctx, cfg.LLM, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize LLM client: %w", err)
	}

	return &Runner{
		cfg:          cfg,
		zoteroClient: zotero.NewClient(cfg.Zotero),
		llmClient:    llmClient,
		display:      display.NewOutput(),
		logger:       logger,
	}, nil
}

var ErrNoTextAvailable = errors.New("no text or abstract available")

func (r *Runner) Run(ctx context.Context, opts Options) error {
	fetchOpts := zotero.FetchOptions{
		CollectionKey: opts.CollectionKey,
		GroupID:       opts.GroupID,
		ItemKey:       opts.ItemKey,
		Limit:         opts.Limit,
		ExcludeTag:    r.cfg.Tagging.SentinelTag,
		ItemTypes:     r.cfg.Zotero.ItemTypes.Allowed,
		Reprocess:     opts.Reprocess,
	}

	items, err := r.zoteroClient.FetchItems(ctx, fetchOpts)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			r.logger.Warn().Msg("Fetch aborted due to cancellation")
			return nil
		}
		return fmt.Errorf("failed to fetch items from Zotero: %w", err)
	}

	if len(items) == 0 {
		r.logger.Info().Msg("No items found to process.")
		return nil
	}

	r.logger.Info().Int("count", len(items)).Msg("Starting item tagging pipeline...")

	total := len(items)
	var processed, skipped, errCount int
	var mu sync.Mutex

	if opts.Concurrent > 1 {
		sem := make(chan struct{}, opts.Concurrent)
		var wg sync.WaitGroup

		for _, item := range items {
			if ctx.Err() != nil {
				r.logger.Warn().Msg("Execution canceled by user; stopping worker dispatch")
				break
			}

			wg.Add(1)
			sem <- struct{}{}

			go func(it zotero.Item) {
				defer wg.Done()
				defer func() { <-sem }()

				if ctx.Err() != nil {
					return
				}

				err := r.processSingleItem(ctx, it, opts)
				mu.Lock()
				if err != nil {
					if errors.Is(err, ErrNoTextAvailable) {
						r.logger.Warn().Str("itemKey", it.Key).Msg("Skipping item: no text or abstract available")
						skipped++
					} else if errors.Is(err, context.Canceled) {
						r.logger.Warn().Str("itemKey", it.Key).Msg("Processing aborted due to cancellation")
					} else {
						r.logger.Error().Err(err).Str("itemKey", it.Key).Msg("Failed to process item")
						errCount++
					}
				} else {
					processed++
				}
				mu.Unlock()
			}(item)
		}

		wg.Wait()
	} else {
		for _, item := range items {
			if ctx.Err() != nil {
				r.logger.Warn().Msg("Execution canceled by user; stopping pipeline")
				break
			}

			err := r.processSingleItem(ctx, item, opts)
			if err != nil {
				if errors.Is(err, ErrNoTextAvailable) {
					r.logger.Warn().Str("itemKey", item.Key).Msg("Skipping item: no text or abstract available")
					skipped++
				} else if errors.Is(err, context.Canceled) {
					r.logger.Warn().Str("itemKey", item.Key).Msg("Processing aborted due to cancellation")
					break
				} else {
					r.logger.Error().Err(err).Str("itemKey", item.Key).Msg("Failed to process item")
					errCount++
				}
			} else {
				processed++
			}
		}
	}

	r.display.RenderSummary(total, processed, skipped, errCount)
	return nil
}

func (r *Runner) processSingleItem(ctx context.Context, item zotero.Item, opts Options) error {
	r.display.RenderItemHeader(item.Data.Title, item.Key)

	existingTags := make([]string, len(item.Data.Tags))
	for i, t := range item.Data.Tags {
		existingTags[i] = t.Tag
	}

	sysPrompt := tagging.BuildSystemPrompt()
	questions := tagging.BuildTopicDecisionQuestions(r.cfg.Tagging.ControlledTopics.Topics)

	var extractRawResp string
	var decisionsRawResp string
	pdfPath, err := r.zoteroClient.DownloadPDF(ctx, item.Key, opts.GroupID)
	if err == nil && pdfPath != "" {
		defer func() { _ = os.Remove(pdfPath) }()
		fi, statErr := os.Stat(pdfPath)
		var sizeInfo string
		if statErr == nil && fi.Size() > 0 {
			sizeInfo = fmt.Sprintf("%.2f MB", float64(fi.Size())/(1024*1024))
		}
		r.display.RenderItemSource("PDF Document", sizeInfo)

		if opts.SkipLLM {
			r.logger.Info().Str("itemKey", item.Key).Msg("[SKIP-LLM] Skipping LLM call and Zotero update")
			return nil
		}

		usrPrompt := tagging.BuildUserPrompt(item.Data.Title, "", existingTags)

		var cleanupFn func()
		getUploadedFile := sync.OnceValues(func() (*genai.File, error) {
			uploadedFile, cleanup, uploadErr := r.llmClient.UploadPDF(ctx, pdfPath)
			if uploadErr != nil {
				return nil, uploadErr
			}
			cleanupFn = cleanup
			return uploadedFile, nil
		})

		var extractErr, decisionsErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			extractRawResp, extractErr = r.llmClient.CallSyncWithPDFProviderAndCache(ctx, sysPrompt, usrPrompt, pdfPath, getUploadedFile, opts.UseCache)
		}()
		go func() {
			defer wg.Done()
			decisionsRawResp, decisionsErr = r.llmClient.CallDecisionsWithPDFProviderAndCache(ctx, usrPrompt, pdfPath, questions, getUploadedFile, opts.UseCache)
		}()
		wg.Wait()
		if cleanupFn != nil {
			cleanupFn()
		}

		if extractErr != nil && decisionsErr != nil {
			return fmt.Errorf("LLM extract and decisions API calls with PDF failed: %w", errors.Join(extractErr, decisionsErr))
		}
		if extractErr != nil {
			return fmt.Errorf("LLM extract API call with PDF failed: %w", extractErr)
		}
		if decisionsErr != nil {
			return fmt.Errorf("LLM decisions API call with PDF failed: %w", decisionsErr)
		}
	} else if strings.TrimSpace(item.Data.AbstractNote) != "" {
		r.logger.Info().Str("itemKey", item.Key).Msg("No PDF attachment found; using abstract note")
		r.display.RenderItemSource("Abstract Note", "")

		if opts.SkipLLM {
			r.logger.Info().Str("itemKey", item.Key).Msg("[SKIP-LLM] Skipping LLM call and Zotero update")
			return nil
		}

		usrPrompt := tagging.BuildUserPrompt(item.Data.Title, item.Data.AbstractNote, existingTags)
		var extractErr, decisionsErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			extractRawResp, extractErr = r.llmClient.CallSyncWithCache(ctx, sysPrompt, usrPrompt, opts.UseCache)
		}()
		go func() {
			defer wg.Done()
			decisionsRawResp, decisionsErr = r.llmClient.CallDecisionsWithCache(ctx, usrPrompt, questions, opts.UseCache)
		}()
		wg.Wait()

		if extractErr != nil && decisionsErr != nil {
			return fmt.Errorf("LLM extract and decisions API calls failed: %w", errors.Join(extractErr, decisionsErr))
		}
		if extractErr != nil {
			return fmt.Errorf("LLM extract API call failed: %w", extractErr)
		}
		if decisionsErr != nil {
			return fmt.Errorf("LLM decisions API call failed: %w", decisionsErr)
		}
	} else {
		return fmt.Errorf("%w for item %s", ErrNoTextAvailable, item.Key)
	}

	tagResult, err := tagging.ParseResponse(extractRawResp)
	if err != nil {
		return fmt.Errorf("failed to parse LLM extract output: %w", err)
	}

	topicMatches, err := tagging.SelectTopicsFromDecisions(
		decisionsRawResp,
		r.cfg.Tagging.ControlledTopics.Topics,
		r.cfg.LLM.Decisions.ProbabilityThreshold,
		r.cfg.LLM.Decisions.ConfidenceThreshold,
		r.cfg.LLM.Decisions.MaxTopics,
	)
	if err != nil {
		return fmt.Errorf("failed to parse LLM decisions output: %w", err)
	}

	topicTags := make([]string, 0, len(topicMatches))
	for _, m := range topicMatches {
		r.logger.Debug().
			Str("itemKey", item.Key).
			Str("topic", m.Tag).
			Float64("noul", m.Noul).
			Float64("confidence", m.Confidence).
			Msg("Selected topic from decision model")
		topicTags = append(topicTags, m.Tag)
	}

	combinedResult := &tagging.TagResult{
		OrgTags:   tagResult.OrgTags,
		GroupTags: tagResult.GroupTags,
		TopicTags: topicTags,
	}
	formattedTags := tagging.BuildTagList(combinedResult, r.cfg.Tagging.SentinelTag)

	r.display.RenderTags(formattedTags)

	if opts.DryRun {
		r.logger.Info().Str("itemKey", item.Key).Msg("[DRY-RUN] Would update tags in Zotero")
		return nil
	}

	err = r.zoteroClient.UpdateTags(ctx, item.Key, item.Version, formattedTags, opts.GroupID)
	if err != nil {
		if errors.Is(err, zotero.ErrPreconditionFailed) {
			r.logger.Warn().Str("itemKey", item.Key).Msg("Item modified externally since fetch; skipping tag update (412)")
			return nil
		}
		return fmt.Errorf("failed to update tags in Zotero: %w", err)
	}

	r.logger.Info().Str("itemKey", item.Key).Msg("Successfully updated Zotero tags")
	return nil
}
