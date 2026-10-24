package reservation

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
	reservations *Repository
}

func NewHandler(reservations *Repository) *Handler {
	return &Handler{reservations: reservations}
}

type createReservationRequest struct {
	ActivityID string `json:"activity_id"`
	Quantity   int    `json:"quantity"`
}

func (h *Handler) Create(c *gin.Context) {
	var input createReservationRequest

	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid reservation request.", nil)
		return
	}

	activityID, err := uuid.Parse(input.ActivityID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_ACTIVITY_ID", "Activity ID must be a valid UUID.", nil)
		return
	}

	if input.Quantity < 1 || input.Quantity > 10 {
		response.Error(c, http.StatusBadRequest, "INVALID_QUANTITY", "Quantity must be between 1 and 10.", nil)
		return
	}

	customerValue, exists := c.Get("user_id")
	if !exists {
		response.Error(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.", nil)
		return
	}

	customerString, ok := customerValue.(string)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.", nil)
		return
	}

	customerID, err := uuid.Parse(customerString)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.", nil)
		return
	}

	created, err := h.reservations.Create(
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
			response.Error(c, http.StatusConflict, "ACTIVITY_NOT_AVAILABLE", "This activity is not available for reservation.", nil)
		case errors.Is(err, ErrInsufficientCapacity):
			response.Error(c, http.StatusConflict, "INSUFFICIENT_CAPACITY", "Not enough spots are available.", nil)
		default:
			slog.Error("create reservation failed", "error", err)
			response.Internal(c)
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"data": created,
	})
}

func (h *Handler) AdminList(c *gin.Context) {
	page, limit := 1, 20

	if value := c.Query("page"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			response.Error(c, http.StatusBadRequest, "INVALID_PAGE", "Page must be a positive integer.", nil)
			return
		}
		page = n
	}

	if value := c.Query("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 100 {
			response.Error(c, http.StatusBadRequest, "INVALID_LIMIT", "Limit must be between 1 and 100.", nil)
			return
		}
		limit = n
	}

	items, err := h.reservations.ListAllAdmin(
		c.Request.Context(),
		limit,
		(page-1)*limit,
	)
	if err != nil {
		slog.Error("admin list reservations failed", "error", err)
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
	id, err := uuid.Parse(c.Param("reservationID"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_RESERVATION_ID", "Reservation ID must be a valid UUID.", nil)
		return
	}

	item, err := h.reservations.GetAdminByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrReservationNotFound) {
			response.Error(c, http.StatusNotFound, "RESERVATION_NOT_FOUND", "Reservation not found.", nil)
			return
		}

		slog.Error("admin get reservation failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": item})
}
