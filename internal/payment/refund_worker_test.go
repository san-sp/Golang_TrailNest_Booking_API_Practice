package payment

import (
	"context"
	"testing"
	"time"
)

func TestRunRefundWorkerStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	go func() {
		defer close(done)
		RunRefundWorker(ctx, nil, time.Second)
	}()

	cancel()

	select {
	case <-done:
		// The worker exited as expected.
	case <-time.After(time.Second):
		t.Fatal("refund worker did not stop after cancellation")
	}
}

func TestRunRefundRecoveryWorkerStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	worker := NewRefundWorker(nil, nil)

	done := make(chan struct{})

	go func() {
		defer close(done)

		RunRefundRecoveryWorker(
			ctx,
			worker,
			time.Second,
			30*time.Minute,
			20,
		)
	}()

	select {
	case <-done:
		// The recovery worker exited as expected.
	case <-time.After(time.Second):
		t.Fatal("refund recovery worker did not stop after cancellation")
	}
}

func TestRunRefundRecoveryWorkerRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		worker    *RefundWorker
		interval  time.Duration
		olderThan time.Duration
		limit     int
	}{
		{
			name:      "nil worker",
			interval:  time.Second,
			olderThan: 30 * time.Minute,
			limit:     20,
		},
		{
			name:      "non-positive interval",
			worker:    NewRefundWorker(nil, nil),
			interval:  0,
			olderThan: 30 * time.Minute,
			limit:     20,
		},
		{
			name:      "non-positive stale threshold",
			worker:    NewRefundWorker(nil, nil),
			interval:  time.Second,
			olderThan: 0,
			limit:     20,
		},
		{
			name:      "invalid batch limit",
			worker:    NewRefundWorker(nil, nil),
			interval:  time.Second,
			olderThan: 30 * time.Minute,
			limit:     101,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RunRefundRecoveryWorker(
				context.Background(),
				tt.worker,
				tt.interval,
				tt.olderThan,
				tt.limit,
			)
		})
	}
}

func TestRunRefundRecoveryWorkerStopsWhileWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	recoveryStarted := make(chan struct{})
	recoveryFinished := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)

		runRefundRecoveryLoop(ctx, time.Hour, func(ctx context.Context) error {
			close(recoveryStarted)
			defer close(recoveryFinished)

			// Simulate a recovery operation that respects cancellation.
			<-ctx.Done()
			return ctx.Err()
		})
	}()

	select {
	case <-recoveryStarted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("recovery pass did not start")
	}

	cancel()

	select {
	case <-recoveryFinished:
	case <-time.After(time.Second):
		t.Fatal("recovery pass did not finish after cancellation")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recovery worker did not stop after cancellation")
	}
}
