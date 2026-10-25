package reservation

import (
	"context"
	"log/slog"
	"time"
)

// RunExpiryWorker immediately processes overdue reservations, then repeats
// at the configured interval. It exits when ctx is cancelled.
func RunExpiryWorker(
	ctx context.Context,
	repo *Repository,
	interval time.Duration,
	batchSize int,
) {
	if interval <= 0 {
		slog.Error("reservation expiry worker requires a positive interval")
		return
	}
	if batchSize < 1 {
		slog.Error("reservation expiry worker requires a positive batch size")
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		// Drain batches so a backlog doesn't have to wait for another tick.
		for {
			if err := ctx.Err(); err != nil {
				return
			}

			count, err := repo.ExpireOverdue(ctx, batchSize)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Error("expire overdue reservations", "error", err)
				break
			}

			if count > 0 {
				slog.Info("expired overdue reservations", "count", count)
			}

			if count < int64(batchSize) {
				break
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
