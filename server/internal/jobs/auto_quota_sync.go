package jobs

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"tavily-proxy/server/internal/services"
)

var autoSyncWG sync.WaitGroup

// WaitForAutoSync blocks until all background auto-sync goroutines have exited.
// Test-only helper.
func WaitForAutoSync() {
	autoSyncWG.Wait()
}

func StartAutoQuotaSync(ctx context.Context, settings *services.SettingsService, sync *services.QuotaSyncService, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	var running atomic.Bool

	autoSyncWG.Add(1)
	go func() {
		defer autoSyncWG.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if running.Load() {
					continue
				}

				enabled, err := settings.GetBool(ctx, services.SettingAutoSyncEnabled, false)
				if err != nil {
					logger.Error("auto-sync: failed to read enabled setting", "err", err)
					continue
				}
				if !enabled {
					continue
				}

				intervalMinutes, err := settings.GetInt(ctx, services.SettingAutoSyncIntervalMinutes, 60)
				if err != nil {
					logger.Error("auto-sync: failed to read interval setting", "err", err)
					continue
				}
				if intervalMinutes < 1 {
					intervalMinutes = 1
				}

				concurrency, err := settings.GetInt(ctx, services.SettingAutoSyncConcurrency, services.DefaultAutoSyncConcurrency)
				if err != nil {
					logger.Error("auto-sync: failed to read concurrency setting", "err", err)
					continue
				}
				if concurrency < 1 {
					concurrency = 1
				}
				if concurrency > services.MaxQuotaSyncConcurrency {
					concurrency = services.MaxQuotaSyncConcurrency
				}

				requestIntervalSeconds, err := settings.GetInt(ctx, services.SettingAutoSyncRequestIntervalSeconds, 0)
				if err != nil {
					logger.Error("auto-sync: failed to read request interval setting", "err", err)
					continue
				}
				if requestIntervalSeconds < 0 {
					requestIntervalSeconds = 0
				}
				if requestIntervalSeconds > 60 {
					requestIntervalSeconds = 60
				}

				interval := time.Duration(intervalMinutes) * time.Minute
				lastRunAt, _ := settings.GetTime(ctx, services.SettingAutoSyncLastRunAt)
				if lastRunAt != nil && time.Since(*lastRunAt) < interval {
					continue
				}

				if !running.CompareAndSwap(false, true) {
					continue
				}

				autoSyncWG.Add(1)
				go func() {
					defer autoSyncWG.Done()
					defer func() {
						running.Store(false)
						if r := recover(); r != nil {
							logger.Error("auto-sync: panic in worker", "err", r)
						}
					}()

					now := time.Now()
					_ = settings.SetTime(context.Background(), services.SettingAutoSyncLastRunAt, now)

					result, err := sync.SyncAllWithConcurrencyAndInterval(
						ctx,
						concurrency,
						time.Duration(requestIntervalSeconds)*time.Second,
					)
					if err != nil {
						_ = settings.Set(context.Background(), services.SettingAutoSyncLastError, err.Error())
						logger.Error("auto-sync: sync failed", "err", err)
						return
					}

					_ = settings.SetTime(context.Background(), services.SettingAutoSyncLastSuccessAt, time.Now())
					_ = settings.Set(context.Background(), services.SettingAutoSyncLastError, "")
					logger.Info(
						"auto-sync: completed",
						"total",
						result.Total,
						"failed",
						result.Failed,
						"interval_seconds",
						requestIntervalSeconds,
					)
				}()
			}
		}
	}()
}
