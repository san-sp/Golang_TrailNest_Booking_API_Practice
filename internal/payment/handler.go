package payment

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxPaymentRequestBody = 64 << 10 // 64 KiB

func paymentBodyError(c *gin.Context, err error, message string) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": "request body exceeds the maximum allowed size",
		})
		return
	}

	c.JSON(http.StatusBadRequest, gin.H{
		"error": message,
	})
}

type Handler struct {
	service    *CheckoutService
	repository *Repository
}

type createPaymentRequest struct {
	ReservationID string `json:"reservation_id" binding:"required"`
}

type verifyPaymentRequest struct {
	PaymentID         string `json:"payment_id" binding:"required"`
	RazorpayOrderID   string `json:"razorpay_order_id" binding:"required"`
	RazorpayPaymentID string `json:"razorpay_payment_id" binding:"required"`
	RazorpaySignature string `json:"razorpay_signature" binding:"required"`
}

func NewHandler(
	service *CheckoutService,
	repository *Repository,
) *Handler {
	return &Handler{
		service:    service,
		repository: repository,
	}
}

func (h *Handler) Create(c *gin.Context) {
	userIDValue, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "authentication required",
		})
		return
	}

	userIDString, ok := userIDValue.(string)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "invalid authentication context",
		})
		return
	}

	customerID, err := uuid.Parse(userIDString)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "invalid authentication context",
		})
		return
	}

	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if len(idempotencyKey) == 0 || len(idempotencyKey) > 255 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Idempotency-Key header is required and must be at most 255 characters",
		})
		return
	}

	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxPaymentRequestBody,
	)

	var request createPaymentRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		paymentBodyError(c, err, "invalid request body")
		return
	}

	reservationID, err := uuid.Parse(request.ReservationID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid reservation_id",
		})
		return
	}

	checkoutResult, err := h.service.CreatePayment(
		c.Request.Context(),
		customerID,
		reservationID,
		idempotencyKey,
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrReservationNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": "reservation not found",
			})
		case errors.Is(err, ErrReservationNotReady):
			c.JSON(http.StatusConflict, gin.H{
				"error": "reservation is not available for checkout",
			})
		case errors.Is(err, ErrIdempotencyConflict):
			c.JSON(http.StatusConflict, gin.H{
				"error": "Idempotency-Key was already used for a different payment request",
			})
		case errors.Is(err, ErrFreeActivity):
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "free activities do not require payment",
			})
		case errors.Is(err, ErrInvalidQuantity):
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid reservation quantity or amount",
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "could not create payment",
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": checkoutResult,
	})
}

func (h *Handler) Verify(c *gin.Context) {
	userIDValue, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	userIDString, ok := userIDValue.(string)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid authentication context"})
		return
	}

	customerID, err := uuid.Parse(userIDString)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid authentication context"})
		return
	}

	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxPaymentRequestBody,
	)

	var request verifyPaymentRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		paymentBodyError(c, err, "invalid request body")
		return
	}

	localPaymentID, err := uuid.Parse(request.PaymentID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payment_id"})
		return
	}

	payment, err := h.service.VerifyCheckoutPayment(
		c.Request.Context(),
		customerID,
		localPaymentID,
		request.RazorpayOrderID,
		request.RazorpayPaymentID,
		request.RazorpaySignature,
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrPaymentNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "payment not found"})
		case errors.Is(err, ErrInvalidSignature):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "payment signature verification failed"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not verify payment"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": payment})
}

func (h *Handler) AdminList(c *gin.Context) {
	page := 1
	limit := 20

	if value := c.Query("page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid page"})
			return
		}
		page = parsed
	}

	if value := c.Query("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 100"})
			return
		}
		limit = parsed
	}

	payments, err := h.repository.ListAllAdmin(
		c.Request.Context(),
		limit,
		(page-1)*limit,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not list payments",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": payments,
		"pagination": gin.H{
			"page":  page,
			"limit": limit,
		},
	})
}

func (h *Handler) AdminGetByID(c *gin.Context) {
	paymentID, err := uuid.Parse(c.Param("paymentID"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payment ID"})
		return
	}

	payment, err := h.repository.GetAdminByID(
		c.Request.Context(),
		paymentID,
	)
	if err != nil {
		if errors.Is(err, ErrPaymentNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "payment not found"})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not retrieve payment",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": payment})
}

func (h *Handler) AdminReconcileCapturedPayment(c *gin.Context) {
	paymentID, err := uuid.Parse(c.Param("paymentID"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payment ID"})
		return
	}

	var request struct {
		RazorpayPaymentID string `json:"razorpay_payment_id" binding:"required"`
	}

	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxPaymentRequestBody,
	)

	if err := c.ShouldBindJSON(&request); err != nil {
		paymentBodyError(c, err, "razorpay_payment_id is required")
		return
	}

	payment, err := h.service.ReconcileCapturedPayment(
		c.Request.Context(),
		paymentID,
		request.RazorpayPaymentID,
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrPaymentNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "payment not found"})
		case errors.Is(err, ErrRazorpayNotConfigured):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Razorpay is not configured"})
		default:
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error": "could not reconcile captured payment",
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": payment})
}
