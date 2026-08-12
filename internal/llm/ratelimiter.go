package llm

import (
	"context"
	"sync"
	"time"

	"github.com/exterex/zotero-tagger/internal/config"
	"golang.org/x/time/rate"
)

type RateLimiter struct {
	rpm         *rate.Limiter
	tpm         *rate.Limiter
	mu          sync.Mutex
	dailyCount  int
	dailyLimit  int
	dailyReset  time.Time
}

func NewRateLimiter(cfg config.RateLimitConfig) *RateLimiter {
	var rpmLimiter *rate.Limiter
	if cfg.RequestsPerMinute > 0 {
		limit := rate.Every(time.Minute / time.Duration(cfg.RequestsPerMinute))
		rpmLimiter = rate.NewLimiter(limit, 1)
	} else {
		rpmLimiter = rate.NewLimiter(rate.Inf, 0)
	}

	var tpmLimiter *rate.Limiter
	if cfg.TokensPerMinute > 0 {
		limit := rate.Limit(float64(cfg.TokensPerMinute) / 60.0)
		tpmLimiter = rate.NewLimiter(limit, cfg.TokensPerMinute/10)
	} else {
		tpmLimiter = rate.NewLimiter(rate.Inf, 0)
	}

	return &RateLimiter{
		rpm:        rpmLimiter,
		tpm:        tpmLimiter,
		dailyLimit: cfg.RequestsPerDay,
		dailyReset: time.Now().Add(24 * time.Hour),
	}
}

func (rl *RateLimiter) Wait(ctx context.Context, estimatedTokens int) error {
	rl.mu.Lock()
	now := time.Now()
	if now.After(rl.dailyReset) {
		rl.dailyCount = 0
		rl.dailyReset = now.Add(24 * time.Hour)
	}
	if rl.dailyLimit > 0 && rl.dailyCount >= rl.dailyLimit {
		rl.mu.Unlock()
		return context.DeadlineExceeded
	}
	rl.dailyCount++
	rl.mu.Unlock()

	if err := rl.rpm.Wait(ctx); err != nil {
		return err
	}

	if estimatedTokens > 0 {
		if err := rl.tpm.WaitN(ctx, estimatedTokens); err != nil {
			return err
		}
	}

	return nil
}
