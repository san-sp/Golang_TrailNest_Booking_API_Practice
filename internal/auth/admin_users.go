package auth

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"trailnest/internal/user"
)

type AdminUserHandler struct {
	users *user.Repository
}

func NewAdminUserHandler(users *user.Repository) *AdminUserHandler {
	return &AdminUserHandler{users: users}
}

func (h *AdminUserHandler) List(c *gin.Context) {
	page := 1
	limit := 20

	if value := c.Query("page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid page",
			})
			return
		}
		page = parsed
	}

	if value := c.Query("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "limit must be between 1 and 100",
			})
			return
		}
		limit = parsed
	}

	users, err := h.users.ListAllAdmin(
		c.Request.Context(),
		limit,
		(page-1)*limit,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not list users",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": users,
		"pagination": gin.H{
			"page":  page,
			"limit": limit,
		},
	})
}

func (h *AdminUserHandler) GetByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("userID"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid user ID",
		})
		return
	}

	found, err := h.users.FindByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "user not found",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not retrieve user",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": found})
}

func (h *AdminUserHandler) PromoteToOperator(c *gin.Context) {
	id, err := uuid.Parse(c.Param("userID"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid user ID",
		})
		return
	}

	updated, err := h.users.PromoteCustomerToOperator(
		c.Request.Context(),
		id,
	)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "customer not found or is not eligible for promotion",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not promote customer",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": updated})
}
