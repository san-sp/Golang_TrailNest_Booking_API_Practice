package payment

import (
	"context"
	"errors"
	"net/url"
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

type HostedPaymentResult struct {
	Payment        Payment `json:"payment"`
	PaymentLinkID  string  `json:"payment_link_id"`
	PaymentLinkURL string  `json:"payment_link_url"`
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

	// Do not reuse a hosted Payment Link payment in Order-based checkout.
	if payment.ProviderPaymentLinkID != "" {
		return CheckoutResult{}, errors.New(
			"payment already has a Razorpay Payment Link; use hosted checkout",
		)
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

func (s *CheckoutService) CreateHostedPayment(
	ctx context.Context,
	customerID uuid.UUID,
	reservationID uuid.UUID,
	idempotencyKey string,
) (HostedPaymentResult, error) {
	if s.razorpay == nil {
		return HostedPaymentResult{}, ErrRazorpayNotConfigured
	}

	payment, err := s.repository.CreatePending(
		ctx,
		customerID,
		reservationID,
		"razorpay",
		idempotencyKey,
	)
	if err != nil {
		return HostedPaymentResult{}, err
	}

	var result HostedPaymentResult

	err = s.repository.withPaymentLinkLock(ctx, payment.ID, func() error {
		// Reload after acquiring the lock. Another request may have
		// created and saved the link while this request was waiting.
		payment, err = s.repository.GetByIDForCustomer(
			ctx,
			payment.ID,
			customerID,
		)
		if err != nil {
			return err
		}

		// Reuse an existing hosted link on an idempotent retry.
		if payment.ProviderPaymentLinkID != "" &&
			payment.ProviderPaymentLinkURL != "" {
			result = HostedPaymentResult{
				Payment:        payment,
				PaymentLinkID:  payment.ProviderPaymentLinkID,
				PaymentLinkURL: payment.ProviderPaymentLinkURL,
			}
			return nil
		}

		// Do not silently switch an existing Order-based payment
		// to hosted checkout.
		if payment.ProviderOrderID != "" {
			return errors.New(
				"payment already has a Razorpay order; use the existing checkout flow",
			)
		}

		// Recover a link created by an earlier request that timed out
		// or failed to save it locally.
		link, err := s.razorpay.FindPaymentLinkByReferenceID(
			ctx,
			payment.ID.String(),
		)
		if err != nil {
			if !errors.Is(err, ErrRazorpayPaymentLinkNotFound) {
				return err
			}

			// Create only after the lookup confirms no matching link.
			link, err = s.razorpay.CreatePaymentLink(
				ctx,
				payment.AmountMinor,
				payment.Currency,
				payment.ID.String(),
				"TrailNest booking payment "+payment.ID.String(),
			)
			if err != nil {
				return err
			}
		}

		parsedURL, urlErr := url.Parse(link.ShortURL)
		if urlErr != nil ||
			parsedURL.Scheme != "https" ||
			!strings.EqualFold(parsedURL.Hostname(), "rzp.io") ||
			parsedURL.User != nil {
			return errors.New(
				"Razorpay payment link does not match the local payment",
			)
		}

		// Validate the provider response before saving it.
		if link.ReferenceID != payment.ID.String() ||
			link.Amount != payment.AmountMinor ||
			!strings.EqualFold(link.Currency, payment.Currency) ||
			!strings.HasPrefix(link.ID, "plink_") ||
			link.ShortURL == "" {
			return errors.New(
				"Razorpay payment link does not match the local payment",
			)
		}

		payment, err = s.repository.SaveProviderPaymentLink(
			ctx,
			payment.ID,
			link.ID,
			link.ShortURL,
		)
		if err != nil {
			return err
		}

		result = HostedPaymentResult{
			Payment:        payment,
			PaymentLinkID:  payment.ProviderPaymentLinkID,
			PaymentLinkURL: payment.ProviderPaymentLinkURL,
		}
		return nil
	})
	if err != nil {
		return HostedPaymentResult{}, err
	}

	return result, nil
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

func (s *CheckoutService) ReconcileCapturedPayment(
	ctx context.Context,
	localPaymentID uuid.UUID,
	providerPaymentID string,
) (Payment, error) {
	if s.razorpay == nil {
		return Payment{}, ErrRazorpayNotConfigured
	}

	providerPaymentID = strings.TrimSpace(providerPaymentID)
	if providerPaymentID == "" {
		return Payment{}, errors.New("provider payment ID is required")
	}

	localPayment, err := s.repository.GetAdminByID(ctx, localPaymentID)
	if err != nil {
		return Payment{}, err
	}

	if localPayment.Provider != "razorpay" ||
		localPayment.ProviderOrderID == "" ||
		localPayment.AmountMinor <= 0 {
		return Payment{}, errors.New(
			"local payment is not eligible for Razorpay reconciliation",
		)
	}

	// Verify the payment directly with Razorpay before changing local state.
	providerPayment, err := s.razorpay.FetchPayment(ctx, providerPaymentID)
	if err != nil {
		return Payment{}, err
	}

	if providerPayment.ID != providerPaymentID ||
		providerPayment.OrderID != localPayment.ProviderOrderID ||
		providerPayment.Amount != localPayment.AmountMinor ||
		!strings.EqualFold(
			strings.TrimSpace(providerPayment.Currency),
			strings.TrimSpace(localPayment.Currency),
		) {
		return Payment{}, errors.New(
			"Razorpay payment details do not match local payment",
		)
	}

	if providerPayment.Status != "captured" {
		return Payment{}, errors.New("Razorpay payment is not captured")
	}

	// A succeeded payment is safe to return only if the identity matches.
	if localPayment.Status == "succeeded" {
		if localPayment.ProviderPaymentID != providerPayment.ID {
			return Payment{}, errors.New(
				"Razorpay payment ID does not match the reconciled payment",
			)
		}
		return localPayment, nil
	}

	if localPayment.Status != "pending" {
		return Payment{}, errors.New(
			"only pending payments can be reconciled",
		)
	}

	if err := s.repository.RecordPaymentAttempt(
		ctx,
		localPayment.ID,
		"razorpay",
		localPayment.ProviderOrderID,
		providerPayment.ID,
		localPayment.AmountMinor,
		localPayment.Currency,
		"captured",
	); err != nil {
		return Payment{}, err
	}

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

	// A deterministic event ID makes retries idempotent.
	eventID := "admin_reconcile_" + providerPayment.ID
	if err := s.repository.ProcessSuccessfulWebhook(
		ctx,
		"razorpay",
		eventID,
		"payment.captured",
		providerPayment.ID,
	); err != nil {
		return Payment{}, err
	}

	return s.repository.GetAdminByID(ctx, localPayment.ID)
}
