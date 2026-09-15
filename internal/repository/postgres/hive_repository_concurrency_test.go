//go:build integration

package postgres_test

// Verifies CreateWithLimit's advisory-lock guard actually serializes
// concurrent creates against real PostgreSQL - see
// apiary_repository_concurrency_test.go in beebase-apiary-service for the
// same property on apiaries. The lock/count here are scoped to user_id,
// not apiary_id: FreeMaxHives is a per-user, account-wide quota (see
// application/hive.Service.Create), so two concurrent creates into
// different apiaries for the same user must still be serialized against
// the same shared limit - not each independently allowed up to 5.

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
		_, _ = pool.Exec(ctx, `DELETE FROM hives WHERE user_id = $1`, userID)
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

	count, err := repo.CountByUser(ctx, userID)
	if err != nil {
		t.Fatalf("CountByUser: %v", err)
	}
	if count != limit {
		t.Fatalf("final hive count for user = %d, want %d", count, limit)
	}
}

// TestHiveRepository_CreateWithLimit_ConcurrentCreatesAcrossApiaries_DoNotExceedAccountLimit
// is the explicit cross-apiary regression case: two batches of concurrent
// creates, targeting two different apiaries but the same user, must
// together still be capped at the account's 5-hive quota - proving the
// advisory lock is keyed by user_id and therefore serializes across
// apiary boundaries, not just within one. Before this correction, the
// lock/count were incorrectly scoped to apiary_id, which would have let
// this test's two apiaries independently reach 5 each (10 total).
func TestHiveRepository_CreateWithLimit_ConcurrentCreatesAcrossApiaries_DoNotExceedAccountLimit(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	userID := uuid.New()
	apiaryA := uuid.New()
	apiaryB := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hives WHERE user_id = $1`, userID)
	})

	repo := repopostgres.NewHiveRepository(pool)
	const perApiary = 6
	const limit = 5

	var wg sync.WaitGroup
	results := make(chan error, perApiary*2)
	for _, apiaryID := range []uuid.UUID{apiaryA, apiaryB} {
		for i := 0; i < perApiary; i++ {
			wg.Add(1)
			go func(apiaryID uuid.UUID, i int) {
				defer wg.Done()
				h := hive.New(userID, apiaryID, fmt.Sprintf("%s-%d", apiaryID, i), "")
				results <- repo.CreateWithLimit(ctx, h, limit)
			}(apiaryID, i)
		}
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, hive.ErrLimitReached):
			// expected once the shared account limit is reached
		default:
			t.Fatalf("unexpected error from a concurrent create: %v", err)
		}
	}
	if successes != limit {
		t.Fatalf("successful concurrent creates across 2 apiaries for one user = %d, want exactly %d (the account limit, not %d per apiary)", successes, limit, limit)
	}

	count, err := repo.CountByUser(ctx, userID)
	if err != nil {
		t.Fatalf("CountByUser: %v", err)
	}
	if count != limit {
		t.Fatalf("final hive count for user = %d, want %d", count, limit)
	}
}
