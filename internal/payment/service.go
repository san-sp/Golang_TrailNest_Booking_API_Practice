package payment

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidQuantity = errors.New("invalid reservation quantity")
	ErrFreeActivity    = errors.New("free activities do not require payment")
)

type CheckoutService struct {
	pool       *pgxpool.Pool
	repository *Repository
	razorpay   *RazorpayClient
}

type CheckoutResult struct {
	Payment         Payment `json:"payment"`
	RazorpayKeyID   string  `json:"razorpay_key_id"`
	RazorpayOrderID string  `json:"razorpay_order_id"`
}

func NewCheckoutService(
	pool *pgxpool.Pool,
	razorpayClient *RazorpayClient,
) *CheckoutService {
	return &CheckoutService{
		pool:       pool,
		repository: NewRepository(pool),
		razorpay:   razorpayClient,
	}
}

func (s *CheckoutService) CreatePayment(
	ctx context.Context,
	customerID uuid.UUID,
	reservationID uuid.UUID,
	idempotencyKey string,
) (CheckoutResult, error) {
	if s.razorpay == nil {
		return CheckoutResult{}, ErrRazorpayNotConfigured
	}

	payment, err := s.repository.CreatePending(
		ctx,
		customerID,
		reservationID,
		"razorpay",
		idempotencyKey,
	)
	if err != nil {
		return CheckoutResult{}, err
	}

	// Reuse the existing order when this is a checkout retry.
	if payment.ProviderOrderID != "" {
		return CheckoutResult{
			Payment:         payment,
			RazorpayKeyID:   s.razorpay.KeyID(),
			RazorpayOrderID: payment.ProviderOrderID,
		}, nil
	}

	order, err := s.razorpay.CreateOrder(
		ctx,
		payment.AmountMinor,
		payment.Currency,
		payment.ID.String(),
	)
	if err != nil {
		return CheckoutResult{}, err
	}

	payment, err = s.repository.SaveProviderOrderID(
		ctx,
		payment.ID,
		order.ID,
	)
	if err != nil {
		return CheckoutResult{}, err
	}

	return CheckoutResult{
		Payment:         payment,
		RazorpayKeyID:   s.razorpay.KeyID(),
		RazorpayOrderID: payment.ProviderOrderID,
	}, nil
}

func (s *CheckoutService) VerifyCheckoutPayment(
	ctx context.Context,
	customerID uuid.UUID,
	localPaymentID uuid.UUID,
	orderID string,
	providerPaymentID string,
	signature string,
) (Payment, error) {
	if s.razorpay == nil {
		return Payment{}, ErrRazorpayNotConfigured
	}

	// Load the payment belonging to this customer.
	localPayment, err := s.repository.GetByIDForCustomer(
		ctx,
		localPaymentID,
		customerID,
	)
	if err != nil {
		return Payment{}, err
	}

	// Never trust the order ID supplied by the browser.
	if localPayment.Provider != "razorpay" ||
		localPayment.ProviderOrderID == "" ||
		localPayment.ProviderOrderID != strings.TrimSpace(orderID) {
		return Payment{}, ErrInvalidSignature
	}

	// Verify the Razorpay Checkout callback signature.
	if err := VerifyCheckoutSignature(
		localPayment.ProviderOrderID,
		providerPaymentID,
		signature,
		s.razorpay.keySecret,
	); err != nil {
		return Payment{}, err
	}

	// Fetch payment details independently from Razorpay.
	providerPayment, err := s.razorpay.FetchPayment(ctx, providerPaymentID)
	if err != nil {
		return Payment{}, err
	}

	if providerPayment.OrderID != localPayment.ProviderOrderID ||
		providerPayment.Amount != localPayment.AmountMinor ||
		!strings.EqualFold(providerPayment.Currency, localPayment.Currency) {
		return Payment{}, errors.New("Razorpay payment details do not match checkout")
	}

	// An authorized payment is not necessarily captured yet.
	if providerPayment.Status != "captured" {
		return localPayment, nil
	}

	// Associate the verified provider payment with our local record.
	if err := s.repository.AssociateProviderPayment(
		ctx,
		localPayment.ID,
		localPayment.ProviderOrderID,
		providerPayment.ID,
		localPayment.AmountMinor,
		localPayment.Currency,
	); err != nil {
		return Payment{}, err
	}

	// The existing fulfillment transaction handles duplicate events and
	// ensures we don't create duplicate bookings.
	eventID := "checkout_" + providerPayment.ID
	if err := s.repository.ProcessSuccessfulWebhook(
		ctx,
		"razorpay",
		eventID,
		"payment.captured",
		providerPayment.ID,
	); err != nil {
		return Payment{}, err
	}

	return s.repository.GetByIDForCustomer(
		ctx,
		localPayment.ID,
		customerID,
	)
}
