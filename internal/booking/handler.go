package booking

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"trailnest/internal/http/response"
)

type Handler struct {
	bookings *Repository
}

func NewHandler(bookings *Repository) *Handler {
	return &Handler{bookings: bookings}
}

type createBookingRequest struct {
	ActivityID string `json:"activity_id"`
	Quantity   int    `json:"quantity"`
}

func (h *Handler) Create(c *gin.Context) {
	var input createBookingRequest

	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(
			c,
			http.StatusBadRequest,
			"INVALID_REQUEST",
			"Invalid booking request.",
			nil,
		)
		return
	}

	activityID, err := uuid.Parse(input.ActivityID)
	if err != nil {
		response.Error(
			c,
			http.StatusBadRequest,
			"INVALID_ACTIVITY_ID",
			"Activity ID must be a valid UUID.",
			nil,
		)
		return
	}

	if input.Quantity < 1 || input.Quantity > 10 {
		response.Error(
			c,
			http.StatusBadRequest,
			"INVALID_QUANTITY",
			"Quantity must be between 1 and 10.",
			nil,
		)
		return
	}
	customerIDValue, exists := c.Get("user_id")
	if !exists {
		response.Error(
			c,
			http.StatusUnauthorized,
			"UNAUTHENTICATED",
			"Authentication is required.",
			nil,
		)
		return
	}

	customerIDString, ok := customerIDValue.(string)
	if !ok {
		response.Error(
			c,
			http.StatusUnauthorized,
			"UNAUTHENTICATED",
			"Authentication is required.",
			nil,
		)
		return
	}

	customerID, err := uuid.Parse(customerIDString)
	if err != nil {
		response.Error(
			c,
			http.StatusUnauthorized,
			"UNAUTHENTICATED",
			"Authentication is required.",
			nil,
		)
		return
	}

	created, err := h.bookings.Create(
		c.Request.Context(),
		customerID,
		activityID,
		input.Quantity,
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrActivityNotFound):
			response.Error(c, http.StatusNotFound, "ACTIVITY_NOT_FOUND", "Activity not found.", nil)
		case errors.Is(err, ErrActivityNotBookable):
			response.Error(c, http.StatusConflict, "ACTIVITY_NOT_AVAILABLE", "This activity is not available for booking.", nil)
		case errors.Is(err, ErrInsufficientCapacity):
			response.Error(c, http.StatusConflict, "INSUFFICIENT_CAPACITY", "Not enough spots are available.", nil)
		case errors.Is(err, ErrPaymentRequired):
			response.Error(
				c,
				http.StatusConflict,
				"PAYMENT_REQUIRED",
				"This activity requires payment through checkout.",
				nil,
			)
		default:
			slog.Error("create booking failed", "error", err)
			response.Internal(c)
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"data": created,
	})
}

func (h *Handler) Cancel(c *gin.Context) {
	bookingID, err := uuid.Parse(c.Param("bookingID"))
	if err != nil {
		response.Error(
			c,
			http.StatusBadRequest,
			"INVALID_BOOKING_ID",
			"Booking ID must be a valid UUID.",
			nil,
		)
		return
	}

	customerIDValue, exists := c.Get("user_id")
	if !exists {
		response.Error(
			c,
			http.StatusUnauthorized,
			"UNAUTHENTICATED",
			"Authentication is required.",
			nil,
		)
		return
	}

	customerIDString, ok := customerIDValue.(string)
	if !ok {
		response.Error(
			c,
			http.StatusUnauthorized,
			"UNAUTHENTICATED",
			"Authentication is required.",
			nil,
		)
		return
	}

	customerID, err := uuid.Parse(customerIDString)
	if err != nil {
		response.Error(
			c,
			http.StatusUnauthorized,
			"UNAUTHENTICATED",
			"Authentication is required.",
			nil,
		)
		return
	}

	err = h.bookings.Cancel(
		c.Request.Context(),
		customerID,
		bookingID,
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrBookingNotFound):
			response.Error(c, http.StatusNotFound, "BOOKING_NOT_FOUND", "Booking not found.", nil)
		case errors.Is(err, ErrBookingAlreadyCancelled):
			response.Error(c, http.StatusConflict, "BOOKING_ALREADY_CANCELLED", "This booking has already been cancelled.", nil)
		case errors.Is(err, ErrCancellationDeadlinePassed):
			response.Error(
				c,
				http.StatusConflict,
				"CANCELLATION_DEADLINE_PASSED",
				"The cancellation deadline has passed.",
				nil,
			)
		default:
			slog.Error("cancel booking failed", "error", err)
			response.Internal(c)
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"message": "Booking cancelled successfully.",
		},
	})
}

func (h *Handler) ListMine(c *gin.Context) {
	customerIDValue, exists := c.Get("user_id")
	if !exists {
		response.Error(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.", nil)
		return
	}

	customerIDString, ok := customerIDValue.(string)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.", nil)
		return
	}

	customerID, err := uuid.Parse(customerIDString)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.", nil)
		return
	}

	bookings, err := h.bookings.ListByCustomer(
		c.Request.Context(),
		customerID,
	)
	if err != nil {
		slog.Error("list customer bookings failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": bookings,
	})
}

func (h *Handler) GetByID(c *gin.Context) {
	bookingID, err := uuid.Parse(c.Param("bookingID"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_BOOKING_ID", "Booking ID must be a valid UUID.", nil)
		return
	}

	customerIDValue, exists := c.Get("user_id")
	if !exists {
		response.Error(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.", nil)
		return
	}

	customerIDString, ok := customerIDValue.(string)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.", nil)
		return
	}

	customerID, err := uuid.Parse(customerIDString)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.", nil)
		return
	}

	b, err := h.bookings.GetByID(c.Request.Context(), customerID, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			response.Error(c, http.StatusNotFound, "BOOKING_NOT_FOUND", "Booking not found.", nil)
			return
		}

		slog.Error("get booking failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": b})
}

func (h *Handler) AdminList(c *gin.Context) {
	page, limit := 1, 20

	if value := c.Query("page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			response.Error(c, http.StatusBadRequest, "INVALID_PAGE", "Page must be a positive integer.", nil)
			return
		}
		page = parsed
	}

	if value := c.Query("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			response.Error(c, http.StatusBadRequest, "INVALID_LIMIT", "Limit must be between 1 and 100.", nil)
			return
		}
		limit = parsed
	}

	items, err := h.bookings.ListAllAdmin(
		c.Request.Context(),
		limit,
		(page-1)*limit,
	)
	if err != nil {
		slog.Error("admin list bookings failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": items,
		"pagination": gin.H{
			"page":  page,
			"limit": limit,
		},
	})
}

func (h *Handler) AdminGetByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("bookingID"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_BOOKING_ID", "Booking ID must be a valid UUID.", nil)
		return
	}

	item, err := h.bookings.GetAdminByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			response.Error(c, http.StatusNotFound, "BOOKING_NOT_FOUND", "Booking not found.", nil)
			return
		}

		slog.Error("admin get booking failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": item})
}
