package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

type fakeRefundProvider struct {
	refund        RazorpayRefund
	err           error
	calls         int
	fetchedRefund RazorpayRefund
	fetchErr      error
	fetchCalls    int
	listedRefunds []RazorpayRefund
	listErr       error
	listCalls     int
}

func (f *fakeRefundProvider) ListPaymentRefunds(
	ctx context.Context,
	paymentID string,
	count int,
	skip int,
) ([]RazorpayRefund, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	if skip >= len(f.listedRefunds) {
		return []RazorpayRefund{}, nil
	}

	end := skip + count
	if end > len(f.listedRefunds) {
		end = len(f.listedRefunds)
	}

	return f.listedRefunds[skip:end], nil
}

func (f *fakeRefundProvider) CreateRefund(
	ctx context.Context,
	paymentID string,
	amount int64,
	idempotencyKey string,
	internalRefundID string,
) (RazorpayRefund, error) {
	f.calls++
	return f.refund, f.err
}

func (f *fakeRefundProvider) FetchRefund(
	ctx context.Context,
	refundID string,
) (RazorpayRefund, error) {
	f.fetchCalls++
	if f.fetchErr != nil {
		return RazorpayRefund{}, f.fetchErr
	}
	return f.fetchedRefund, nil
}

func insertTestRefundJob(
	t *testing.T,
	pool *pgxpool.Pool,
	f webhookFixture,
) uuid.UUID {
	t.Helper()

	refundID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO payment_refunds (
			id,
			payment_id,
			provider,
			provider_payment_id,
			idempotency_key,
			amount_minor,
			currency,
			status
		)
		VALUES ($1, $2, 'mock', $3, $4, 5000, 'INR', 'pending')
	`,
		refundID,
		f.paymentID,
		f.providerPaymentID,
		"test-refund-"+refundID.String(),
	)
	require.NoError(t, err)

	return refundID
}

func markTestRefundJobProcessing(
	t *testing.T,
	pool *pgxpool.Pool,
	refundID uuid.UUID,
) {
	t.Helper()

	tag, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET status = 'processing',
		    attempt_count = attempt_count + 1,
		    claimed_at = NOW(),
		    updated_at = NOW()
		WHERE id = $1
		  AND status = 'pending'
	`, refundID)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected())
}

func TestClaimRefundJobEmptyQueue(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	// Verify that a specific, nonexistent refund job cannot be found.
	// ClaimRefundJob operates on the shared queue, so this test instead
	// verifies the empty-queue error using an isolated repository query
	// only if the queue is empty.
	var pendingCount int
	err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM payment_refunds
		WHERE status = 'pending'
	`).Scan(&pendingCount)
	require.NoError(t, err)

	if pendingCount != 0 {
		t.Skipf("test database has %d pending refund jobs; empty-queue test requires an empty queue", pendingCount)
	}

	_, err = repo.ClaimRefundJob(context.Background())
	require.ErrorIs(t, err, ErrRefundJobNotFound)
}

func TestClaimRefundJobConcurrentWorkers(t *testing.T) {
	pool := openTestPool(t)

	ctx := context.Background()
	repo := NewRepository(pool)

	f1 := createWebhookFixture(t, pool)
	f2 := createWebhookFixture(t, pool)

	id1 := insertTestRefundJob(t, pool, f1)
	id2 := insertTestRefundJob(t, pool, f2)

	_, err := pool.Exec(ctx, `
    UPDATE payment_refunds
    SET created_at = NOW() - INTERVAL '1 day'
    WHERE id IN ($1, $2)
`, id1, id2)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), `
        DELETE FROM payment_refunds
        WHERE id IN ($1, $2)
    `, id1, id2)
		require.NoError(t, err)
	})

	start := make(chan struct{})
	type result struct {
		job RefundJob
		err error
	}
	results := make(chan result, 2)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			job, err := repo.ClaimRefundJob(ctx)
			results <- result{job: job, err: err}
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	claimed := make(map[uuid.UUID]bool)
	for result := range results {
		require.NoError(t, result.err)
		claimed[result.job.ID] = true
		require.Equal(t, "processing", result.job.Status)
		require.Equal(t, 1, result.job.AttemptCount)
	}

	require.Len(t, claimed, 2)
	require.True(t, claimed[id1])
	require.True(t, claimed[id2])

	var processingCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM payment_refunds
		WHERE id IN ($1, $2)
		  AND status = 'processing'
	`, id1, id2).Scan(&processingCount)
	require.NoError(t, err)
	require.Equal(t, 2, processingCount)

}

func TestClaimRefundJobReturnsDatabaseErrors(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := repo.ClaimRefundJob(ctx)

	require.Error(t, err)
	require.False(t, errors.Is(err, ErrRefundJobNotFound),
		fmt.Sprintf("expected a database/context error, got %v", err))
}

func prepareRefundCompletionFixture(
	t *testing.T,
	pool *pgxpool.Pool,
	status string,
) (webhookFixture, uuid.UUID) {
	t.Helper()

	ctx := context.Background()
	f := createWebhookFixture(t, pool)
	refundID := insertTestRefundJob(t, pool, f)

	_, err := pool.Exec(ctx, `
		UPDATE payments
		SET status = 'succeeded',
		    fulfillment_status = 'refund_required'
		WHERE id = $1
	`, f.paymentID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		UPDATE payment_refunds
		SET status = $2
		WHERE id = $1
	`, refundID, status)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(),
			`DELETE FROM payment_refunds WHERE id = $1`, refundID)
		require.NoError(t, err)
	})

	return f, refundID
}

func TestMarkRefundSucceeded(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	f, refundID := prepareRefundCompletionFixture(t, pool, "processing")
	providerRefundID := "rfnd_" + uuid.NewString()

	err := repo.MarkRefundSucceeded(
		context.Background(),
		refundID,
		providerRefundID,
	)
	require.NoError(t, err)

	var refundStatus, storedProviderRefundID string
	err = pool.QueryRow(context.Background(), `
		SELECT status, provider_refund_id
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&refundStatus, &storedProviderRefundID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", refundStatus)
	require.Equal(t, providerRefundID, storedProviderRefundID)

	var paymentStatus, fulfillmentStatus string
	err = pool.QueryRow(context.Background(), `
		SELECT status, fulfillment_status
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&paymentStatus, &fulfillmentStatus)
	require.NoError(t, err)
	require.Equal(t, "succeeded", paymentStatus)
	require.Equal(t, "refunded", fulfillmentStatus)

	// Repeating completion with the same provider refund ID is safe.
	require.NoError(t, repo.MarkRefundSucceeded(
		context.Background(),
		refundID,
		providerRefundID,
	))
}

func TestMarkRefundSucceededRejectsConflictingProviderID(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	_, refundID := prepareRefundCompletionFixture(t, pool, "processing")

	require.NoError(t, repo.MarkRefundSucceeded(
		context.Background(),
		refundID,
		"rfnd_original",
	))

	err := repo.MarkRefundSucceeded(
		context.Background(),
		refundID,
		"rfnd_different",
	)
	require.ErrorContains(t, err, "conflicts")

	var storedID, status string
	err = pool.QueryRow(context.Background(), `
		SELECT provider_refund_id, status
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&storedID, &status)
	require.NoError(t, err)
	require.Equal(t, "rfnd_original", storedID)
	require.Equal(t, "succeeded", status)
}

func TestMarkRefundSucceededRejectsPendingJob(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	_, refundID := prepareRefundCompletionFixture(t, pool, "pending")

	err := repo.MarkRefundSucceeded(
		context.Background(),
		refundID,
		"rfnd_should_not_apply",
	)
	require.ErrorContains(t, err, "cannot complete refund job")

	var status string
	err = pool.QueryRow(context.Background(), `
		SELECT status
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&status)
	require.NoError(t, err)
	require.Equal(t, "pending", status)
}

func TestMarkRefundSucceededRollsBackOnPaymentUpdateFailure(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	f, refundID := prepareRefundCompletionFixture(t, pool, "processing")
	providerRefundID := "rfnd_" + uuid.NewString()

	triggerName := "test_reject_refund_payment_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "test_reject_refund_payment_fn_" + strings.ReplaceAll(uuid.NewString(), "-", "")

	_, err := pool.Exec(context.Background(), fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'intentional test failure';
		END;
		$$ LANGUAGE plpgsql
	`, functionName))
	require.NoError(t, err)

	_, err = pool.Exec(context.Background(), fmt.Sprintf(`
		CREATE TRIGGER %s
		BEFORE UPDATE OF fulfillment_status ON payments
		FOR EACH ROW
		WHEN (NEW.fulfillment_status = 'refunded')
		EXECUTE FUNCTION %s()
	`, triggerName, functionName))
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), fmt.Sprintf(
			`DROP TRIGGER IF EXISTS %s ON payments`, triggerName,
		))
		require.NoError(t, err)

		_, err = pool.Exec(context.Background(), fmt.Sprintf(
			`DROP FUNCTION IF EXISTS %s()`, functionName,
		))
		require.NoError(t, err)
	})

	err = repo.MarkRefundSucceeded(
		context.Background(),
		refundID,
		providerRefundID,
	)
	require.Error(t, err)

	var status, storedProviderRefundID string
	err = pool.QueryRow(context.Background(), `
		SELECT status, COALESCE(provider_refund_id, '')
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&status, &storedProviderRefundID)
	require.NoError(t, err)
	require.Equal(t, "processing", status)
	require.Empty(t, storedProviderRefundID)

	var fulfillmentStatus string
	err = pool.QueryRow(context.Background(), `
		SELECT fulfillment_status
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&fulfillmentStatus)
	require.NoError(t, err)
	require.Equal(t, "refund_required", fulfillmentStatus)
}

func TestMarkRefundPending(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	f := createWebhookFixture(t, pool)
	refundID := insertTestRefundJob(t, pool, f)

	// Claim the job first: MarkRefundPending only accepts processing jobs.
	markTestRefundJobProcessing(t, pool, refundID)

	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(),
			`DELETE FROM payment_refunds WHERE id = $1`, refundID)
		require.NoError(t, err)
	})

	providerRefundID := "rfnd_pending_" + uuid.NewString()

	err := repo.MarkRefundPending(
		context.Background(),
		refundID,
		providerRefundID,
	)
	require.NoError(t, err)

	var storedProviderRefundID, status, lastError string
	err = pool.QueryRow(context.Background(), `
		SELECT provider_refund_id, status, last_error
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&storedProviderRefundID, &status, &lastError)
	require.NoError(t, err)

	require.Equal(t, providerRefundID, storedProviderRefundID)
	require.Equal(t, "review_required", status)
	require.Contains(t, lastError, "pending")
}

func TestMarkRefundPendingRejectsConflictingProviderID(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	f := createWebhookFixture(t, pool)
	refundID := insertTestRefundJob(t, pool, f)

	markTestRefundJobProcessing(t, pool, refundID)

	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(),
			`DELETE FROM payment_refunds WHERE id = $1`, refundID)
		require.NoError(t, err)
	})

	err := repo.MarkRefundPending(
		context.Background(),
		refundID,
		"rfnd_first",
	)
	require.NoError(t, err)

	err = repo.MarkRefundPending(
		context.Background(),
		refundID,
		"rfnd_conflicting",
	)
	require.Error(t, err)
}

func TestMarkRefundForReview(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	f := createWebhookFixture(t, pool)
	refundID := insertTestRefundJob(t, pool, f)

	markTestRefundJobProcessing(t, pool, refundID)

	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(),
			`DELETE FROM payment_refunds WHERE id = $1`, refundID)
		require.NoError(t, err)
	})

	reason := "provider returned an uncertain outcome"
	err := repo.MarkRefundForReview(
		context.Background(),
		refundID,
		reason,
	)
	require.NoError(t, err)

	var status, lastError string
	err = pool.QueryRow(context.Background(), `
		SELECT status, last_error
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&status, &lastError)
	require.NoError(t, err)

	require.Equal(t, "review_required", status)
	require.Equal(t, reason, lastError)
}

func TestMarkRefundFailed(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	f, refundID := prepareRefundCompletionFixture(t, pool, "processing")

	reason := "provider reported refund failure"
	err := repo.MarkRefundFailed(
		context.Background(),
		refundID,
		reason,
	)
	require.NoError(t, err)

	var status, lastError string
	err = pool.QueryRow(context.Background(), `
		SELECT status, last_error
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&status, &lastError)
	require.NoError(t, err)

	require.Equal(t, "failed", status)
	require.Equal(t, reason, lastError)

	var paymentStatus, fulfillmentStatus string
	err = pool.QueryRow(context.Background(), `
		SELECT status, fulfillment_status
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&paymentStatus, &fulfillmentStatus)
	require.NoError(t, err)

	require.Equal(t, "succeeded", paymentStatus)
	require.Equal(t, "refund_required", fulfillmentStatus)
}

func TestRefundWorkerProcessesSuccessfulRefund(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	f, refundID := prepareRefundCompletionFixture(t, pool, "pending")

	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(),
			`DELETE FROM payment_refunds WHERE id = $1`, refundID)
		require.NoError(t, err)
	})

	provider := &fakeRefundProvider{
		refund: RazorpayRefund{
			ID:        "rfnd_worker_success_" + uuid.NewString(),
			PaymentID: f.providerPaymentID,
			Amount:    5000,
			Currency:  "INR",
			Status:    "processed",
		},
	}

	worker := NewRefundWorker(repo, provider)
	err := worker.ProcessRefund(context.Background(), refundID)
	require.NoError(t, err)
	require.Equal(t, 1, provider.calls)

	var refundStatus, storedRefundID string
	err = pool.QueryRow(context.Background(), `
		SELECT status, provider_refund_id
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&refundStatus, &storedRefundID)
	require.NoError(t, err)

	require.Equal(t, "succeeded", refundStatus)
	require.Equal(t, provider.refund.ID, storedRefundID)

	var fulfillmentStatus string
	err = pool.QueryRow(context.Background(), `
		SELECT fulfillment_status
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&fulfillmentStatus)
	require.NoError(t, err)
	require.Equal(t, "refunded", fulfillmentStatus)
}

func TestRefundWorkerHandlesPendingRefund(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	f, refundID := prepareRefundCompletionFixture(t, pool, "pending")

	provider := &fakeRefundProvider{
		refund: RazorpayRefund{
			ID:        "rfnd_pending_" + uuid.NewString(),
			PaymentID: f.providerPaymentID,
			Amount:    5000,
			Currency:  "INR",
			Status:    "pending",
		},
	}

	worker := NewRefundWorker(repo, provider)
	err := worker.ProcessRefund(context.Background(), refundID)
	require.NoError(t, err)
	require.Equal(t, 1, provider.calls)

	var refundStatus, storedProviderRefundID string
	err = pool.QueryRow(context.Background(), `
		SELECT status, provider_refund_id
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&refundStatus, &storedProviderRefundID)
	require.NoError(t, err)
	require.Equal(t, "review_required", refundStatus)
	require.Equal(t, provider.refund.ID, storedProviderRefundID)

	var fulfillmentStatus string
	err = pool.QueryRow(context.Background(), `
		SELECT fulfillment_status
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&fulfillmentStatus)
	require.NoError(t, err)
	require.Equal(t, "refund_required", fulfillmentStatus)
}

func TestRefundWorkerHandlesFailedRefund(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	f, refundID := prepareRefundCompletionFixture(t, pool, "pending")

	provider := &fakeRefundProvider{
		refund: RazorpayRefund{
			ID:        "rfnd_failed_" + uuid.NewString(),
			PaymentID: f.providerPaymentID,
			Amount:    5000,
			Currency:  "INR",
			Status:    "failed",
		},
	}

	worker := NewRefundWorker(repo, provider)
	err := worker.ProcessRefund(context.Background(), refundID)
	require.NoError(t, err)
	require.Equal(t, 1, provider.calls)

	var refundStatus, lastError string
	err = pool.QueryRow(context.Background(), `
		SELECT status, last_error
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&refundStatus, &lastError)
	require.NoError(t, err)
	require.Equal(t, "failed", refundStatus)
	require.Equal(t, "Razorpay reported refund failure", lastError)

	var fulfillmentStatus string
	err = pool.QueryRow(context.Background(), `
		SELECT fulfillment_status
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&fulfillmentStatus)
	require.NoError(t, err)
	require.Equal(t, "refund_required", fulfillmentStatus)
}

func TestRefundWorkerHandlesUnexpectedStatus(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	f, refundID := prepareRefundCompletionFixture(t, pool, "pending")

	provider := &fakeRefundProvider{
		refund: RazorpayRefund{
			ID:        "rfnd_unknown_" + uuid.NewString(),
			PaymentID: f.providerPaymentID,
			Amount:    5000,
			Currency:  "INR",
			Status:    "created",
		},
	}

	worker := NewRefundWorker(repo, provider)
	err := worker.ProcessRefund(context.Background(), refundID)
	require.ErrorContains(t, err, `unexpected provider refund status "created"`)
	require.Equal(t, 1, provider.calls)

	var refundStatus, lastError string
	err = pool.QueryRow(context.Background(), `
		SELECT status, last_error
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&refundStatus, &lastError)
	require.NoError(t, err)
	require.Equal(t, "review_required", refundStatus)
	require.Contains(t, lastError, `unexpected provider refund status "created"`)

	var fulfillmentStatus string
	err = pool.QueryRow(context.Background(), `
		SELECT fulfillment_status
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&fulfillmentStatus)
	require.NoError(t, err)
	require.Equal(t, "refund_required", fulfillmentStatus)
}

func TestRefundWorkerHandlesProviderError(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	f, refundID := prepareRefundCompletionFixture(t, pool, "pending")

	provider := &fakeRefundProvider{
		err: errors.New("simulated network timeout"),
	}

	worker := NewRefundWorker(repo, provider)
	err := worker.ProcessRefund(context.Background(), refundID)
	require.ErrorContains(t, err, "simulated network timeout")
	require.Equal(t, 1, provider.calls)

	var refundStatus, lastError string
	err = pool.QueryRow(context.Background(), `
		SELECT status, last_error
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&refundStatus, &lastError)
	require.NoError(t, err)
	require.Equal(t, "review_required", refundStatus)
	require.Contains(t, lastError, "refund provider outcome is uncertain")
	require.Contains(t, lastError, "simulated network timeout")

	var fulfillmentStatus string
	err = pool.QueryRow(context.Background(), `
		SELECT fulfillment_status
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&fulfillmentStatus)
	require.NoError(t, err)
	require.Equal(t, "refund_required", fulfillmentStatus)
}

func TestRefundWorkerReconcilesProcessedRefund(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	f, refundID := prepareRefundCompletionFixture(
		t,
		pool,
		"review_required",
	)

	providerRefundID := "rfnd_reconcile_" + uuid.NewString()

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET provider_refund_id = $2
		WHERE id = $1
	`, refundID, providerRefundID)
	require.NoError(t, err)

	provider := &fakeRefundProvider{
		fetchedRefund: RazorpayRefund{
			ID:        providerRefundID,
			PaymentID: f.providerPaymentID,
			Amount:    5000,
			Currency:  "INR",
			Status:    "processed",
		},
	}

	worker := NewRefundWorker(repo, provider)
	err = worker.ReconcileRefund(context.Background(), refundID)
	require.NoError(t, err)
	require.Equal(t, 1, provider.fetchCalls)

	var refundStatus, storedRefundID string
	err = pool.QueryRow(context.Background(), `
		SELECT status, provider_refund_id
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&refundStatus, &storedRefundID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", refundStatus)
	require.Equal(t, providerRefundID, storedRefundID)

	var fulfillmentStatus string
	err = pool.QueryRow(context.Background(), `
		SELECT fulfillment_status
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&fulfillmentStatus)
	require.NoError(t, err)
	require.Equal(t, "refunded", fulfillmentStatus)
}

func TestRefundWorkerRejectsReconciliationAmountMismatch(t *testing.T) {
	pool := openTestPool(t)
	repo := NewRepository(pool)

	f, refundID := prepareRefundCompletionFixture(
		t,
		pool,
		"review_required",
	)

	providerRefundID := "rfnd_reconcile_mismatch_" + uuid.NewString()

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET provider_refund_id = $2
		WHERE id = $1
	`, refundID, providerRefundID)
	require.NoError(t, err)

	provider := &fakeRefundProvider{
		fetchedRefund: RazorpayRefund{
			ID:        providerRefundID,
			PaymentID: f.providerPaymentID,
			Amount:    4999, // Expected amount is 5000.
			Currency:  "INR",
			Status:    "processed",
		},
	}

	worker := NewRefundWorker(repo, provider)
	err = worker.ReconcileRefund(context.Background(), refundID)

	require.ErrorContains(t, err, "amount does not match stored amount")
	require.Equal(t, 1, provider.fetchCalls)

	var refundStatus string
	err = pool.QueryRow(context.Background(), `
		SELECT status
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(&refundStatus)
	require.NoError(t, err)
	require.Equal(t, "review_required", refundStatus)

	var fulfillmentStatus string
	err = pool.QueryRow(context.Background(), `
		SELECT fulfillment_status
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&fulfillmentStatus)
	require.NoError(t, err)
	require.Equal(t, "refund_required", fulfillmentStatus)
}
