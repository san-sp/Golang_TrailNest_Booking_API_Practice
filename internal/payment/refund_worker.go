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
