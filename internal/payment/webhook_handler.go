package payment

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type WebhookHandler struct {
	repository    *Repository
	razorpay      *RazorpayClient
	webhookSecret string
}

func NewWebhookHandler(
	repository *Repository,
	razorpay *RazorpayClient,
	webhookSecret string,
) *WebhookHandler {
	return &WebhookHandler{
		repository:    repository,
		razorpay:      razorpay,
		webhookSecret: webhookSecret,
	}
}

type razorpayWebhookEvent struct {
	Event   string `json:"event"`
	Payload struct {
		Payment struct {
			Entity struct {
				ID            string `json:"id"`
				OrderID       string `json:"order_id"`
				PaymentLinkID string `json:"payment_link_id"`
				Amount        int64  `json:"amount"`
				Currency      string `json:"currency"`
				Status        string `json:"status"`
			} `json:"entity"`
		} `json:"payment"`

		PaymentLink struct {
			Entity struct {
				ID          string `json:"id"`
				ReferenceID string `json:"reference_id"`
				Amount      int64  `json:"amount"`
				Currency    string `json:"currency"`
				Status      string `json:"status"`
			} `json:"entity"`
		} `json:"payment_link"`
	} `json:"payload"`
}

func (h *WebhookHandler) Handle(c *gin.Context) {
	if h.razorpay == nil || strings.TrimSpace(h.webhookSecret) == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "webhook processing is not configured",
		})
		return
	}

	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		1<<20,
	)

	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook body"})
		return
	}

	signature := c.GetHeader("X-Razorpay-Signature")
	if err := VerifyWebhookSignature(rawBody, signature, h.webhookSecret); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid webhook signature"})
		return
	}

	var event razorpayWebhookEvent
	if err := json.Unmarshal(rawBody, &event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook event"})
		return
	}

	// A valid event that this endpoint doesn't handle is acknowledged.
	// Configure Razorpay to send payment.captured for successful fulfillment.
	// Acknowledge valid event types that this handler doesn't process.
	eventID := strings.TrimSpace(c.GetHeader("X-Razorpay-Event-Id"))

	if event.Event == "payment_link.paid" {
		h.handlePaymentLinkPaid(c, event, eventID)
		return
	}

	if event.Event != "payment.captured" &&
		event.Event != "payment.failed" {
		c.Status(http.StatusOK)
		return
	}

	entity := event.Payload.Payment.Entity

	if eventID == "" || entity.ID == "" || entity.OrderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing payment event details"})
		return
	}

	localPayment, err := h.repository.GetByProviderOrderID(
		c.Request.Context(),
		"razorpay",
		entity.OrderID,
	)
	if err != nil {
		if errors.Is(err, ErrPaymentNotFound) {
			// Don't acknowledge an event for an order we cannot reconcile.
			c.JSON(http.StatusNotFound, gin.H{"error": "payment order not found"})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load payment"})
		return
	}

	providerPayment, err := h.razorpay.FetchPayment(c.Request.Context(), entity.ID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "could not verify provider payment"})
		return
	}

	// Cross-check the signed event against Razorpay's current payment record
	// and our own expected checkout values.
	if providerPayment.ID != entity.ID ||
		providerPayment.OrderID != localPayment.ProviderOrderID ||
		providerPayment.Amount != localPayment.AmountMinor ||
		providerPayment.Amount != entity.Amount ||
		!strings.EqualFold(providerPayment.Currency, localPayment.Currency) ||
		!strings.EqualFold(providerPayment.Currency, entity.Currency) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "payment details do not match checkout",
		})
		return
	}

	if event.Event == "payment.captured" {
		if providerPayment.Status != "captured" ||
			entity.Status != "captured" {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error": "payment is not captured",
			})
			return
		}
	} else if event.Event == "payment.failed" {
		if providerPayment.Status != "failed" ||
			entity.Status != "failed" {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error": "payment is not failed",
			})
			return
		}

		if err := h.repository.ProcessFailedPaymentAttemptWebhook(
			c.Request.Context(),
			"razorpay",
			eventID,
			event.Event,
			localPayment.ID,
			localPayment.ProviderOrderID,
			providerPayment.ID,
			localPayment.AmountMinor,
			localPayment.Currency,
		); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "could not process failed payment attempt",
			})
			return
		}

		c.Status(http.StatusOK)
		return
	}

	if err := h.repository.RecordPaymentAttempt(
		c.Request.Context(),
		localPayment.ID,
		"razorpay",
		localPayment.ProviderOrderID,
		providerPayment.ID,
		localPayment.AmountMinor,
		localPayment.Currency,
		"captured",
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not record captured payment attempt",
		})
		return
	}

	if err := h.repository.AssociateProviderPayment(
		c.Request.Context(),
		localPayment.ID,
		localPayment.ProviderOrderID,
		providerPayment.ID,
		localPayment.AmountMinor,
		localPayment.Currency,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not associate provider payment",
		})
		return
	}

	if err := h.repository.ProcessSuccessfulWebhook(
		c.Request.Context(),
		"razorpay",
		eventID,
		event.Event,
		providerPayment.ID,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not process payment event",
		})
		return
	}

	c.Status(http.StatusOK)
}

func (h *WebhookHandler) handlePaymentLinkPaid(
	c *gin.Context,
	event razorpayWebhookEvent,
	eventID string,
) {
	linkEvent := event.Payload.PaymentLink.Entity
	paymentEvent := event.Payload.Payment.Entity

	if strings.TrimSpace(eventID) == "" ||
		strings.TrimSpace(linkEvent.ID) == "" ||
		strings.TrimSpace(linkEvent.ReferenceID) == "" ||
		strings.TrimSpace(paymentEvent.ID) == "" ||
		strings.TrimSpace(paymentEvent.PaymentLinkID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "missing payment link event details",
		})
		return
	}

	if linkEvent.Status != "paid" ||
		paymentEvent.Status != "captured" ||
		paymentEvent.PaymentLinkID != linkEvent.ID {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "payment link event is not a valid captured payment",
		})
		return
	}

	ctx := c.Request.Context()

	localPayment, err := h.repository.GetByProviderPaymentLinkID(
		ctx,
		"razorpay",
		linkEvent.ID,
	)
	if err != nil {
		if errors.Is(err, ErrPaymentNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "payment link not found",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not load payment link",
		})
		return
	}

	if linkEvent.ReferenceID != localPayment.ID.String() ||
		localPayment.ProviderPaymentLinkID != linkEvent.ID ||
		linkEvent.Amount != localPayment.AmountMinor ||
		paymentEvent.Amount != localPayment.AmountMinor ||
		!strings.EqualFold(linkEvent.Currency, localPayment.Currency) ||
		!strings.EqualFold(paymentEvent.Currency, localPayment.Currency) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "payment link details do not match local payment",
		})
		return
	}

	providerLink, err := h.razorpay.FetchPaymentLink(ctx, linkEvent.ID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "could not verify provider payment link",
		})
		return
	}

	if providerLink.ID != localPayment.ProviderPaymentLinkID ||
		providerLink.ReferenceID != localPayment.ID.String() ||
		providerLink.Amount != localPayment.AmountMinor ||
		providerLink.AmountPaid != localPayment.AmountMinor ||
		!strings.EqualFold(providerLink.Currency, localPayment.Currency) ||
		providerLink.Status != "paid" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "provider payment link does not match local payment",
		})
		return
	}

	providerPayment, err := h.razorpay.FetchPayment(ctx, paymentEvent.ID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "could not verify provider payment",
		})
		return
	}

	if providerPayment.ID != paymentEvent.ID ||
		providerPayment.PaymentLinkID != linkEvent.ID ||
		providerPayment.Amount != localPayment.AmountMinor ||
		!strings.EqualFold(providerPayment.Currency, localPayment.Currency) ||
		providerPayment.Status != "captured" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "provider payment does not match local payment",
		})
		return
	}

	if err := h.repository.RecordPaymentLinkAttempt(
		ctx,
		localPayment.ID,
		linkEvent.ID,
		providerPayment.ID,
		localPayment.AmountMinor,
		localPayment.Currency,
		"captured",
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not record captured payment attempt",
		})
		return
	}

	if err := h.repository.AssociateProviderPaymentLink(
		ctx,
		localPayment.ID,
		linkEvent.ID,
		providerPayment.ID,
		localPayment.AmountMinor,
		localPayment.Currency,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not associate provider payment",
		})
		return
	}

	if err := h.repository.ProcessSuccessfulWebhook(
		ctx,
		"razorpay",
		eventID,
		event.Event,
		providerPayment.ID,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not process payment event",
		})
		return
	}

	c.Status(http.StatusOK)
}
