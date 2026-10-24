package wishlist

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"trailnest/internal/http/response"
)

type Handler struct {
	wishlist *Repository
}

func NewHandler(wishlist *Repository) *Handler {
	return &Handler{wishlist: wishlist}
}

type addWishlistRequest struct {
	ActivityID string `json:"activity_id"`
}

func customerIDFromContext(c *gin.Context) (uuid.UUID, bool) {
	value, exists := c.Get("user_id")
	if !exists {
		return uuid.Nil, false
	}

	idString, ok := value.(string)
	if !ok {
		return uuid.Nil, false
	}

	id, err := uuid.Parse(idString)
	if err != nil {
		return uuid.Nil, false
	}

	return id, true
}

func (h *Handler) Add(c *gin.Context) {
	var input addWishlistRequest

	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid wishlist request.", nil)
		return
	}

	activityID, err := uuid.Parse(input.ActivityID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_ACTIVITY_ID", "Activity ID must be a valid UUID.", nil)
		return
	}

	customerID, ok := customerIDFromContext(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.", nil)
		return
	}

	item, err := h.wishlist.Add(c.Request.Context(), customerID, activityID)
	if err != nil {
		switch {
		case errors.Is(err, ErrActivityNotFound):
			response.Error(c, http.StatusNotFound, "ACTIVITY_NOT_FOUND", "Activity not found.", nil)
		case errors.Is(err, ErrAlreadySaved):
			response.Error(c, http.StatusConflict, "ALREADY_IN_WISHLIST", "This activity is already in your wishlist.", nil)
		default:
			slog.Error("add wishlist item failed", "error", err)
			response.Internal(c)
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": item})
}

func (h *Handler) List(c *gin.Context) {
	customerID, ok := customerIDFromContext(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.", nil)
		return
	}

	items, err := h.wishlist.List(c.Request.Context(), customerID)
	if err != nil {
		slog.Error("list wishlist failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": items})
}

func (h *Handler) Remove(c *gin.Context) {
	activityID, err := uuid.Parse(c.Param("activityID"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_ACTIVITY_ID", "Activity ID must be a valid UUID.", nil)
		return
	}

	customerID, ok := customerIDFromContext(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.", nil)
		return
	}

	err = h.wishlist.Remove(c.Request.Context(), customerID, activityID)
	if err != nil {
		if errors.Is(err, ErrItemNotFound) {
			response.Error(c, http.StatusNotFound, "WISHLIST_ITEM_NOT_FOUND", "Wishlist item not found.", nil)
			return
		}

		slog.Error("remove wishlist item failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{"message": "Activity removed from wishlist."},
	})
}
