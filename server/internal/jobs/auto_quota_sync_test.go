package jobs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"tavily-proxy/server/internal/db"
	"tavily-proxy/server/internal/services"
)

func TestAutoQuotaSync_ReadsConcurrencyFromSettings(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	settings := services.NewSettingsService(database)
	keySvc := services.NewKeyService(database, nil)

	if _, err := keySvc.Create(context.Background(), "tvly-stub", "stub", 1000); err != nil {
		t.Fatalf("create key: %v", err)
	}

	if err := settings.SetBool(context.Background(), services.SettingAutoSyncEnabled, true); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	if err := settings.SetInt(context.Background(), services.SettingAutoSyncIntervalMinutes, 1); err != nil {
		t.Fatalf("set interval: %v", err)
	}
	if err := settings.SetInt(context.Background(), services.SettingAutoSyncRequestIntervalSeconds, 0); err != nil {
		t.Fatalf("set request interval: %v", err)
	}
	if err := settings.SetInt(context.Background(), services.SettingAutoSyncConcurrency, 8); err != nil {
		t.Fatalf("set concurrency: %v", err)
	}
	if err := settings.SetTime(context.Background(), services.SettingAutoSyncLastRunAt, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatalf("backdate last run: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proxy := services.NewTavilyProxy("http://127.0.0.1:1", 100*time.Millisecond, keySvc, nil, nil, nil)
	syncSvc := services.NewQuotaSyncService(keySvc, proxy, nil)

	go StartAutoQuotaSync(ctx, settings, syncSvc, nil)
	t.Cleanup(func() {
		cancel()
		WaitForAutoSync()
	})

	// Ticker is 30s in production; wait up to 35s for the first tick to fire.
	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		if last, _ := settings.GetTime(context.Background(), services.SettingAutoSyncLastSuccessAt); last != nil {
			got := syncSvc.LastSyncConcurrency()
			if got != 8 {
				t.Errorf("auto-sync concurrency = %d, want 8 (set via SettingAutoSyncConcurrency)", got)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("auto-sync loop did not run within 35s; setting reads may be broken")
}