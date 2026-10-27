package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrRefundJobNotFound = errors.New("refund job not found")

type RefundProvider interface {
	CreateRefund(
		ctx context.Context,
		paymentID string,
		amount int64,
		idempotencyKey string,
		internalRefundID string,
	) (RazorpayRefund, error)
}

type RefundLookupProvider interface {
	FetchRefund(
		ctx context.Context,
		refundID string,
	) (RazorpayRefund, error)
}

type RefundWorker struct {
	repository *Repository
	provider   RefundProvider
}

func NewRefundWorker(
	repository *Repository,
	provider RefundProvider,
) *RefundWorker {
	return &RefundWorker{
		repository: repository,
		provider:   provider,
	}
}

type RefundJob struct {
	ID                uuid.UUID
	PaymentID         uuid.UUID
	Provider          string
	ProviderPaymentID string
	ProviderRefundID  string
	IdempotencyKey    string
	AmountMinor       int64
	Currency          string
	Status            string
	AttemptCount      int
	ClaimedAt         *time.Time
	LastError         string
}

func (r *Repository) GetRefundJobForReconciliation(
	ctx context.Context,
	refundJobID uuid.UUID,
) (RefundJob, error) {
	var job RefundJob

	err := r.pool.QueryRow(ctx, `
		SELECT
			id,
			payment_id,
			provider,
			provider_payment_id,
			COALESCE(provider_refund_id, ''),
			idempotency_key,
			amount_minor,
			currency,
			status,
			attempt_count,
			claimed_at,
			COALESCE(last_error, '')
		FROM payment_refunds
		WHERE id = $1
	`, refundJobID).Scan(
		&job.ID,
		&job.PaymentID,
		&job.Provider,
		&job.ProviderPaymentID,
		&job.ProviderRefundID,
		&job.IdempotencyKey,
		&job.AmountMinor,
		&job.Currency,
		&job.Status,
		&job.AttemptCount,
		&job.ClaimedAt,
		&job.LastError,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefundJob{}, ErrRefundJobNotFound
	}
	if err != nil {
		return RefundJob{}, fmt.Errorf(
			"load refund job for reconciliation: %w",
			err,
		)
	}

	if job.Status != "review_required" {
		return RefundJob{}, fmt.Errorf(
			"refund job is not awaiting reconciliation: status %q",
			job.Status,
		)
	}

	if job.ProviderRefundID == "" {
		return RefundJob{}, errors.New(
			"refund job has no provider refund ID to reconcile",
		)
	}

	return job, nil
}

func (r *Repository) ClaimRefundJob(ctx context.Context) (RefundJob, error) {
	return r.claimRefundJob(ctx, nil)
}

// claimRefundJob claims a specific refund when refundJobID is provided.
// A nil ID claims the oldest available pending refund.
func (r *Repository) claimRefundJob(
	ctx context.Context,
	refundJobID *uuid.UUID,
) (RefundJob, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RefundJob{}, fmt.Errorf("begin refund claim: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var job RefundJob

	err = tx.QueryRow(ctx, `
		SELECT
			id,
			payment_id,
			provider,
			provider_payment_id,
			COALESCE(provider_refund_id, ''),
			idempotency_key,
			amount_minor,
			currency,
			status,
			attempt_count,
			claimed_at,
			COALESCE(last_error, '')
		FROM payment_refunds
		WHERE status = 'pending'
		  AND ($1::uuid IS NULL OR id = $1)
		ORDER BY created_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, refundJobID).Scan(
		&job.ID,
		&job.PaymentID,
		&job.Provider,
		&job.ProviderPaymentID,
		&job.ProviderRefundID,
		&job.IdempotencyKey,
		&job.AmountMinor,
		&job.Currency,
		&job.Status,
		&job.AttemptCount,
		&job.ClaimedAt,
		&job.LastError,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefundJob{}, ErrRefundJobNotFound
	}
	if err != nil {
		return RefundJob{}, fmt.Errorf("select pending refund job: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE payment_refunds
		SET status = 'processing',
		    attempt_count = attempt_count + 1,
		    claimed_at = NOW(),
		    updated_at = NOW(),
		    last_error = NULL
		WHERE id = $1
		  AND status = 'pending'
	`, job.ID)
	if err != nil {
		return RefundJob{}, fmt.Errorf("mark refund job processing: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return RefundJob{}, fmt.Errorf("commit refund claim: %w", err)
	}

	job.Status = "processing"
	job.AttemptCount++
	return job, nil
}

// MarkRefundSucceeded atomically records a successful provider refund and
// marks the associated payment's fulfillment as refunded.
func (r *Repository) MarkRefundSucceeded(
	ctx context.Context,
	refundJobID uuid.UUID,
	providerRefundID string,
) error {
	providerRefundID = strings.TrimSpace(providerRefundID)
	if providerRefundID == "" {
		return errors.New("provider refund ID is required")
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin refund completion: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var paymentID uuid.UUID
	var status string
	var existingProviderRefundID string

	err = tx.QueryRow(ctx, `
		SELECT payment_id, status, COALESCE(provider_refund_id, '')
		FROM payment_refunds
		WHERE id = $1
		FOR UPDATE
	`, refundJobID).Scan(
		&paymentID,
		&status,
		&existingProviderRefundID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRefundJobNotFound
	}
	if err != nil {
		return fmt.Errorf("load refund job for completion: %w", err)
	}

	if status != "processing" && status != "succeeded" {
		return fmt.Errorf("cannot complete refund job with status %q", status)
	}

	if existingProviderRefundID != "" &&
		existingProviderRefundID != providerRefundID {
		return errors.New("provider refund ID conflicts with stored refund")
	}

	if status == "processing" {
		_, err = tx.Exec(ctx, `
			UPDATE payment_refunds
			SET provider_refund_id = $2,
			    status = 'succeeded',
			    updated_at = NOW(),
			    completed_at = COALESCE(completed_at, NOW()),
			    last_error = NULL
			WHERE id = $1
			  AND status = 'processing'
		`, refundJobID, providerRefundID)
		if err != nil {
			return fmt.Errorf("mark refund job succeeded: %w", err)
		}
	}

	tag, err := tx.Exec(ctx, `
		UPDATE payments
		SET fulfillment_status = 'refunded',
		    updated_at = NOW()
		WHERE id = $1
		  AND fulfillment_status IN ('refund_required', 'refunded')
	`, paymentID)
	if err != nil {
		return fmt.Errorf("mark payment fulfillment refunded: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New(
			"payment fulfillment is not eligible to be marked refunded",
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit refund completion: %w", err)
	}

	return nil
}

func (r *Repository) MarkReconciledRefundSucceeded(
	ctx context.Context,
	refundJobID uuid.UUID,
	providerRefundID string,
) error {
	providerRefundID = strings.TrimSpace(providerRefundID)
	if providerRefundID == "" {
		return errors.New("provider refund ID is required")
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin reconciled refund completion: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var paymentID uuid.UUID
	var status string
	var existingProviderRefundID string

	err = tx.QueryRow(ctx, `
		SELECT payment_id, status, COALESCE(provider_refund_id, '')
		FROM payment_refunds
		WHERE id = $1
		FOR UPDATE
	`, refundJobID).Scan(
		&paymentID,
		&status,
		&existingProviderRefundID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRefundJobNotFound
	}
	if err != nil {
		return fmt.Errorf("load refund job for reconciliation: %w", err)
	}

	if status != "review_required" && status != "succeeded" {
		return fmt.Errorf(
			"cannot reconcile refund job with status %q",
			status,
		)
	}

	if existingProviderRefundID != providerRefundID {
		return errors.New("provider refund ID conflicts with stored refund")
	}

	if status == "review_required" {
		_, err = tx.Exec(ctx, `
			UPDATE payment_refunds
			SET status = 'succeeded',
			    updated_at = NOW(),
			    completed_at = COALESCE(completed_at, NOW()),
			    last_error = NULL
			WHERE id = $1
			  AND status = 'review_required'
		`, refundJobID)
		if err != nil {
			return fmt.Errorf("mark reconciled refund succeeded: %w", err)
		}
	}

	tag, err := tx.Exec(ctx, `
		UPDATE payments
		SET fulfillment_status = 'refunded',
		    updated_at = NOW()
		WHERE id = $1
		  AND fulfillment_status IN ('refund_required', 'refunded')
	`, paymentID)
	if err != nil {
		return fmt.Errorf("mark reconciled payment refunded: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New(
			"payment fulfillment is not eligible to be marked refunded",
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reconciled refund completion: %w", err)
	}

	return nil
}

func (r *Repository) MarkRefundForReview(
	ctx context.Context,
	refundJobID uuid.UUID,
	reason string,
) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "refund outcome requires reconciliation"
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE payment_refunds
		SET status = 'review_required',
		    last_error = $2,
		    updated_at = NOW()
		WHERE id = $1
		  AND status = 'processing'
	`, refundJobID, reason)
	if err != nil {
		return fmt.Errorf("mark refund for review: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("refund job is not processing")
	}

	return nil
}

func (r *Repository) MarkRefundPending(
	ctx context.Context,
	refundJobID uuid.UUID,
	providerRefundID string,
) error {
	providerRefundID = strings.TrimSpace(providerRefundID)
	if providerRefundID == "" {
		return errors.New("provider refund ID is required")
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE payment_refunds
		SET provider_refund_id = $2,
		    status = 'review_required',
		    last_error = 'provider refund is pending; reconciliation required',
		    updated_at = NOW()
		WHERE id = $1
		  AND status = 'processing'
		  AND (
		      provider_refund_id IS NULL
		      OR provider_refund_id = $2
		  )
	`, refundJobID, providerRefundID)
	if err != nil {
		return fmt.Errorf("record pending provider refund: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("refund job is not processing or provider refund ID conflicts")
	}

	return nil
}

func (r *Repository) MarkRefundFailed(
	ctx context.Context,
	refundJobID uuid.UUID,
	reason string,
) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "provider reported refund failure"
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE payment_refunds
		SET status = 'failed',
		    last_error = $2,
		    updated_at = NOW()
		WHERE id = $1
		  AND status = 'processing'
	`, refundJobID, reason)
	if err != nil {
		return fmt.Errorf("mark refund failed: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("refund job is not processing")
	}

	return nil
}

func (w *RefundWorker) ProcessNext(ctx context.Context) error {
	if w == nil || w.repository == nil || w.provider == nil {
		return errors.New("refund worker is not properly configured")
	}

	job, err := w.repository.ClaimRefundJob(ctx)
	if err != nil {
		return err
	}

	return w.processClaimedRefund(ctx, job)
}

func (w *RefundWorker) ProcessRefund(
	ctx context.Context,
	refundJobID uuid.UUID,
) error {
	if w == nil || w.repository == nil || w.provider == nil {
		return errors.New("refund worker is not properly configured")
	}

	job, err := w.repository.claimRefundJob(ctx, &refundJobID)
	if err != nil {
		return err
	}

	return w.processClaimedRefund(ctx, job)
}

func (w *RefundWorker) processClaimedRefund(
	ctx context.Context,
	job RefundJob,
) error {
	refund, err := w.provider.CreateRefund(
		ctx,
		job.ProviderPaymentID,
		job.AmountMinor,
		job.IdempotencyKey,
		job.ID.String(),
	)
	if err != nil {
		reason := "refund provider outcome is uncertain: " + err.Error()
		if reviewErr := w.repository.MarkRefundForReview(ctx, job.ID, reason); reviewErr != nil {
			return fmt.Errorf(
				"refund provider error: %v; mark for review: %w",
				err,
				reviewErr,
			)
		}
		return fmt.Errorf("create provider refund: %w", err)
	}

	switch refund.Status {
	case "processed":
		return w.repository.MarkRefundSucceeded(ctx, job.ID, refund.ID)

	case "pending":
		return w.repository.MarkRefundPending(ctx, job.ID, refund.ID)

	case "failed":
		return w.repository.MarkRefundFailed(
			ctx,
			job.ID,
			"Razorpay reported refund failure",
		)

	default:
		reason := fmt.Sprintf(
			"unexpected provider refund status %q",
			refund.Status,
		)
		if reviewErr := w.repository.MarkRefundForReview(ctx, job.ID, reason); reviewErr != nil {
			return fmt.Errorf("%s; mark for review: %w", reason, reviewErr)
		}
		return errors.New(reason)
	}
}

func (w *RefundWorker) ReconcileRefund(
	ctx context.Context,
	refundJobID uuid.UUID,
) error {
	if w == nil || w.repository == nil || w.provider == nil {
		return errors.New("refund worker is not properly configured")
	}

	lookup, ok := w.provider.(RefundLookupProvider)
	if !ok {
		return errors.New("refund provider does not support refund lookup")
	}

	job, err := w.repository.GetRefundJobForReconciliation(ctx, refundJobID)
	if err != nil {
		return err
	}

	refund, err := lookup.FetchRefund(ctx, job.ProviderRefundID)
	if err != nil {
		return fmt.Errorf("fetch provider refund for reconciliation: %w", err)
	}

	if refund.ID != job.ProviderRefundID {
		return errors.New("provider refund ID does not match stored refund")
	}
	if refund.PaymentID != job.ProviderPaymentID {
		return errors.New("provider refund payment ID does not match stored payment")
	}
	if refund.Amount != job.AmountMinor {
		return errors.New("provider refund amount does not match stored amount")
	}
	if refund.Currency != job.Currency {
		return errors.New("provider refund currency does not match stored currency")
	}

	switch refund.Status {
	case "processed":
		return w.repository.MarkReconciledRefundSucceeded(
			ctx,
			job.ID,
			refund.ID,
		)

	case "pending":
		return errors.New(
			"provider refund is still pending; reconciliation is required",
		)

	case "failed":
		return errors.New(
			"provider refund failed; manual reconciliation is required",
		)

	default:
		return fmt.Errorf(
			"unexpected provider refund status %q",
			refund.Status,
		)
	}
}
