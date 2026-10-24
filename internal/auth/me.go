package auth

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"trailnest/internal/user"
)

// MeHandler returns the authenticated user's current profile.
type MeHandler struct {
	users *user.Repository
}

func NewMeHandler(users *user.Repository) *MeHandler {
	return &MeHandler{users: users}
}

func (h *MeHandler) Me(c *gin.Context) {
	value, exists := c.Get("user_id")
	if !exists {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"code":    "UNAUTHORIZED",
				"message": "Authentication is required",
			},
		})
		return
	}

	subject, ok := value.(string)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"code":    "UNAUTHORIZED",
				"message": "Invalid authenticated user",
			},
		})
		return
	}

	userID, err := uuid.Parse(subject)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"code":    "UNAUTHORIZED",
				"message": "Invalid authenticated user",
			},
		})
		return
	}

	found, err := h.users.FindByID(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"code":    "ACCOUNT_UNAVAILABLE",
					"message": "This account is no longer available",
				},
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "INTERNAL_ERROR",
				"message": "An unexpected error occurred",
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": found,
	})
}
