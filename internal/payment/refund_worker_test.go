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
