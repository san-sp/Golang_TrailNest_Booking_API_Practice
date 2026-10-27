package payment

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// RunRefundWorker processes pending refunds independently of HTTP requests.
// It drains available jobs, then waits before checking the queue again.
// It exits when ctx is cancelled.
func RunRefundWorker(
	ctx context.Context,
	worker *RefundWorker,
	interval time.Duration,
) {
	if worker == nil {
		slog.Error("refund worker is not configured")
		return
	}
	if interval <= 0 {
		slog.Error("invalid refund worker interval", "interval", interval)
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		// Process available jobs without waiting between successful jobs.
		for {
			if err := ctx.Err(); err != nil {
				return
			}

			err := worker.ProcessNext(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}

				if errors.Is(err, ErrRefundJobNotFound) {
					break
				}

				slog.Error("process refund job", "error", err)
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

// RunRefundRecoveryWorker periodically scans for stale refund jobs.
// It does not create new refunds; it only attempts to locate existing
// provider refunds and moves verified matches to review_required.

func RunRefundRecoveryWorker(
	ctx context.Context,
	worker *RefundWorker,
	interval time.Duration,
	olderThan time.Duration,
	limit int,
) {
	if worker == nil {
		slog.Error("refund recovery worker is not configured")
		return
	}
	if interval <= 0 {
		slog.Error("invalid refund recovery interval", "interval", interval)
		return
	}
	if olderThan <= 0 {
		slog.Error("invalid refund stale threshold", "older_than", olderThan)
		return
	}
	if limit < 1 || limit > 100 {
		slog.Error("invalid refund recovery batch limit", "limit", limit)
		return
	}

	runRefundRecoveryLoop(ctx, interval, func(ctx context.Context) error {
		return worker.RecoverStaleRefunds(ctx, olderThan, limit)
	})
}

// runRefundRecoveryLoop runs a recovery pass immediately, then waits between
// passes. The injected function lets tests verify cancellation deterministically.
func runRefundRecoveryLoop(
	ctx context.Context,
	interval time.Duration,
	recover func(context.Context) error,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		if err := recover(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("recover stale refunds", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
