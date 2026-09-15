//go:build integration

package postgres_test

// Verifies CreateWithLimit's advisory-lock guard actually serializes
// concurrent creates into the same apiary against real PostgreSQL - see
// apiary_repository_concurrency_test.go in beebase-apiary-service for the
// same property on apiaries. The lock/count here are scoped to apiary_id
// (not user_id), matching the finalized product model: the 5-hive limit
// only ever applies to the one apiary a Free user may currently create
// into.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
	repopostgres "github.com/sbezhuk/beebase-hive-service/internal/repository/postgres"
)

func TestHiveRepository_CreateWithLimit_ConcurrentCreatesDoNotExceedLimit(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	userID := uuid.New()
	apiaryID := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hives WHERE apiary_id = $1`, apiaryID)
	})

	repo := repopostgres.NewHiveRepository(pool)

	const attempts = 12
	const limit = 5

	var wg sync.WaitGroup
	results := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h := hive.New(userID, apiaryID, fmt.Sprintf("Concurrent %d", i), "")
			results <- repo.CreateWithLimit(ctx, h, limit)
		}(i)
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, hive.ErrLimitReached):
			// expected for every attempt beyond the limit
		default:
			t.Fatalf("unexpected error from a concurrent create: %v", err)
		}
	}
	if successes != limit {
		t.Fatalf("successful concurrent creates = %d, want exactly %d - a count-then-insert race would let this exceed the limit", successes, limit)
	}

	count, err := repo.CountByApiary(ctx, apiaryID)
	if err != nil {
		t.Fatalf("CountByApiary: %v", err)
	}
	if count != limit {
		t.Fatalf("final hive count in apiary = %d, want %d", count, limit)
	}
}

// TestHiveRepository_CreateWithLimit_ConcurrentCreatesInDifferentApiaries_DoNotInterfere
// proves the lock is scoped to apiary_id, not user_id: concurrent creates
// into two different apiaries for the same user must each independently
// reach their own limit, not contend with or block each other.
func TestHiveRepository_CreateWithLimit_ConcurrentCreatesInDifferentApiaries_DoNotInterfere(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	userID := uuid.New()
	apiaryA := uuid.New()
	apiaryB := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hives WHERE apiary_id = ANY($1)`, []uuid.UUID{apiaryA, apiaryB})
	})

	repo := repopostgres.NewHiveRepository(pool)
	const perApiary = 6
	const limit = 5

	var wg sync.WaitGroup
	for _, apiaryID := range []uuid.UUID{apiaryA, apiaryB} {
		for i := 0; i < perApiary; i++ {
			wg.Add(1)
			go func(apiaryID uuid.UUID, i int) {
				defer wg.Done()
				h := hive.New(userID, apiaryID, fmt.Sprintf("%s-%d", apiaryID, i), "")
				_ = repo.CreateWithLimit(ctx, h, limit)
			}(apiaryID, i)
		}
	}
	wg.Wait()

	for _, apiaryID := range []uuid.UUID{apiaryA, apiaryB} {
		count, err := repo.CountByApiary(ctx, apiaryID)
		if err != nil {
			t.Fatalf("CountByApiary(%s): %v", apiaryID, err)
		}
		if count != limit {
			t.Errorf("apiary %s count = %d, want %d", apiaryID, count, limit)
		}
	}
}
