package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestReconcileCapturedPaymentExpiredReservation(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	f := createWebhookFixture(t, pool)
	orderID := "order_" + uuid.NewString()
	providerPaymentID := "pay_" + uuid.NewString()

	// Convert the fixture into a Razorpay order and expire its reservation.
	_, err := pool.Exec(ctx, `
		UPDATE payments
		SET provider = 'razorpay',
		    provider_payment_id = NULL,
		    provider_order_id = $2,
		    amount_minor = 5000,
		    currency = 'INR'
		WHERE id = $1
	`, f.paymentID, orderID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
    UPDATE reservations
    SET status = 'expired',
        expires_at = NOW() - INTERVAL '1 minute'
    WHERE id = $1
`, f.reservationID)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/payments/"+providerPaymentID, r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RazorpayPayment{
			ID:       providerPaymentID,
			OrderID:  orderID,
			Amount:   5000,
			Currency: "INR",
			Status:   "captured",
		})
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}

	service := NewCheckoutService(pool, client)

	result, err := service.ReconcileCapturedPayment(
		ctx,
		f.paymentID,
		providerPaymentID,
	)
	require.NoError(t, err)
	require.Equal(t, "succeeded", result.Status)
	require.Equal(t, "refund_required", result.FulfillmentStatus)
	require.Equal(t, providerPaymentID, result.ProviderPaymentID)

	var bookingCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM bookings
		WHERE reservation_id = $1
	`, f.reservationID).Scan(&bookingCount)
	require.NoError(t, err)
	require.Zero(t, bookingCount)

	var reservationStatus string
	var expiresAt time.Time
	err = pool.QueryRow(ctx, `
		SELECT status, expires_at
		FROM reservations
		WHERE id = $1
	`, f.reservationID).Scan(&reservationStatus, &expiresAt)
	require.NoError(t, err)
	require.Equal(t, "expired", reservationStatus)
	require.True(t, expiresAt.Before(time.Now()))
}

func TestReconcileCapturedPaymentRejectsMismatchedOrderID(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	f := createWebhookFixture(t, pool)
	orderID := "order_" + uuid.NewString()
	wrongPaymentID := "pay_wrong_" + uuid.NewString()

	_, err := pool.Exec(ctx, `
		UPDATE payments
		SET provider = 'razorpay',
		    provider_payment_id = NULL,
		    provider_order_id = $2,
		    amount_minor = 5000,
		    currency = 'INR'
		WHERE id = $1
	`, f.paymentID, orderID)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RazorpayPayment{
			ID:       wrongPaymentID,
			OrderID:  "order_belongs_to_another_payment",
			Amount:   5000,
			Currency: "INR",
			Status:   "captured",
		})
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}

	service := NewCheckoutService(pool, client)

	_, err = service.ReconcileCapturedPayment(
		ctx,
		f.paymentID,
		wrongPaymentID,
	)
	require.ErrorContains(t, err, "Razorpay payment details do not match local payment")

	var status string
	var savedProviderPaymentID *string
	err = pool.QueryRow(ctx, `
		SELECT status, provider_payment_id
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&status, &savedProviderPaymentID)
	require.NoError(t, err)
	require.Equal(t, "pending", status)
	require.Nil(t, savedProviderPaymentID)
}

func TestReconcileCapturedPaymentRecoversAfterBookingInsertFailure(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	f := createWebhookFixture(t, pool)
	orderID := "order_" + uuid.NewString()
	providerPaymentID := "pay_" + uuid.NewString()

	_, err := pool.Exec(ctx, `
		UPDATE payments
		SET provider = 'razorpay',
		    provider_payment_id = NULL,
		    provider_order_id = $2,
		    amount_minor = 5000,
		    currency = 'INR'
		WHERE id = $1
	`, f.paymentID, orderID)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/payments/"+providerPaymentID, r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RazorpayPayment{
			ID:       providerPaymentID,
			OrderID:  orderID,
			Amount:   5000,
			Currency: "INR",
			Status:   "captured",
		})
	}))
	defer server.Close()

	service := NewCheckoutService(pool, &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	})

	// Force booking insertion to fail during fulfillment.
	installBookingInsertFailure(t, pool, f.reservationID)

	_, err = service.ReconcileCapturedPayment(
		ctx,
		f.paymentID,
		providerPaymentID,
	)
	require.Error(t, err)

	// The fulfillment transaction must roll back, but the captured
	// attempt and provider-payment association should remain saved.
	var paymentStatus string
	var savedProviderPaymentID string
	err = pool.QueryRow(ctx, `
		SELECT status, provider_payment_id
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&paymentStatus, &savedProviderPaymentID)
	require.NoError(t, err)
	require.Equal(t, "pending", paymentStatus)
	require.Equal(t, providerPaymentID, savedProviderPaymentID)

	var bookingCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM bookings
		WHERE reservation_id = $1
	`, f.reservationID).Scan(&bookingCount)
	require.NoError(t, err)
	require.Zero(t, bookingCount)

	// Remove the trigger so the retry can complete.
	_, err = pool.Exec(ctx, `
		DROP TRIGGER IF EXISTS test_fail_booking_insert_trigger
			ON public.bookings;
		DROP FUNCTION IF EXISTS public.test_fail_booking_insert();
	`)
	require.NoError(t, err)

	result, err := service.ReconcileCapturedPayment(
		ctx,
		f.paymentID,
		providerPaymentID,
	)
	require.NoError(t, err)
	require.Equal(t, "succeeded", result.Status)
	require.Equal(t, "fulfilled", result.FulfillmentStatus)
	require.Equal(t, providerPaymentID, result.ProviderPaymentID)

	var reservationStatus string
	err = pool.QueryRow(ctx, `
		SELECT status
		FROM reservations
		WHERE id = $1
	`, f.reservationID).Scan(&reservationStatus)
	require.NoError(t, err)
	require.Equal(t, "confirmed", reservationStatus)

	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM bookings
		WHERE reservation_id = $1
	`, f.reservationID).Scan(&bookingCount)
	require.NoError(t, err)
	require.Equal(t, 1, bookingCount)
}
