package services

import (
	"context"
	"path/filepath"
	"testing"

	"tavily-proxy/server/internal/db"
)

func TestKeyService_Update_RevivesInvalidKeyViaIsInvalidField(t *testing.T) {
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

	svc := NewKeyService(database, nil)

	created, err := svc.Create(context.Background(), "tvly-revive-test", "revive", 1000)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 模拟上游 401 标记 key invalid
	if err := svc.MarkInvalid(context.Background(), created.ID); err != nil {
		t.Fatalf("mark invalid: %v", err)
	}

	// 候选池应排除它
	cands, err := svc.Candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	for _, c := range cands {
		if c.ID == created.ID {
			t.Fatalf("invalid key still in candidate pool")
		}
	}

	// 通过 Update 设 IsInvalid=false 和 IsActive=true 来复活
	invalidFalse := false
	activeTrue := true
	updated, err := svc.Update(context.Background(), created.ID, KeyUpdate{
		IsInvalid: &invalidFalse,
		IsActive:  &activeTrue,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if updated.IsInvalid {
		t.Errorf("IsInvalid still true after revive; want false")
	}
	if !updated.IsActive {
		t.Errorf("IsActive still false after revive; want true")
	}

	// 候选池应重新包含它
	cands, err = svc.Candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates after revive: %v", err)
	}
	found := false
	for _, c := range cands {
		if c.ID == created.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("revived key not in candidate pool")
	}
}

func TestKeyService_Update_AllowsIsActiveToggleOnValidKey(t *testing.T) {
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

	svc := NewKeyService(database, nil)
	created, err := svc.Create(context.Background(), "tvly-toggle-test", "toggle", 1000)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	activeFalse := false
	updated, err := svc.Update(context.Background(), created.ID, KeyUpdate{IsActive: &activeFalse})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.IsActive {
		t.Errorf("IsActive should be false after disabling")
	}
}