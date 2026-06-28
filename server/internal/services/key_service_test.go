package services

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"tavily-proxy/server/internal/db"
)

func TestKeyService_Candidates_ShuffleIsNotDeterministic(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	keys := NewKeyService(database, nil)

	for i := 0; i < 5; i++ {
		_, err := keys.Create(context.Background(),
			"tvly-test-"+string(rune('a'+i)),
			"alias-"+string(rune('a'+i)),
			1000)
		if err != nil {
			t.Fatalf("create key %d: %v", i, err)
		}
	}

	const N = 50
	var mu sync.Mutex
	firstSeen := make(map[uint]int)

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cands, err := keys.Candidates(context.Background())
			if err != nil {
				t.Errorf("candidates: %v", err)
				return
			}
			if len(cands) == 0 {
				t.Error("no candidates")
				return
			}
			mu.Lock()
			firstSeen[cands[0].ID]++
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(firstSeen) < 3 {
		t.Errorf("shuffle not random: only %d distinct first-keys across %d concurrent calls (want >= 3)", len(firstSeen), N)
	}
}
