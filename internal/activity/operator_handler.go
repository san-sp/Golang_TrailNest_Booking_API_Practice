package activity

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"trailnest/internal/http/response"
)

func authenticatedOperatorID(c *gin.Context) (uuid.UUID, bool) {
	value, exists := c.Get("user_id")
	if !exists {
		response.Error(c, http.StatusUnauthorized, "UNAUTHORIZED",
			"Authentication is required.", nil)
		return uuid.Nil, false
	}

	userID, ok := value.(string)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "INVALID_TOKEN",
			"Invalid authenticated user ID.", nil)
		return uuid.Nil, false
	}

	id, err := uuid.Parse(userID)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "INVALID_TOKEN",
			"Invalid authenticated user ID.", nil)
		return uuid.Nil, false
	}

	return id, true
}

func (h *Handler) OperatorCreate(c *gin.Context) {
	operatorID, ok := authenticatedOperatorID(c)
	if !ok {
		return
	}

	var input struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Location    string `json:"location"`
		StartsAt    string `json:"starts_at"`
		EndsAt      string `json:"ends_at"`
		Capacity    int    `json:"capacity"`
		PriceMinor  int64  `json:"price_minor"`
		Currency    string `json:"currency"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_BODY",
			"Invalid activity payload.", nil)
		return
	}

	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.Location = strings.TrimSpace(input.Location)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))

	startsAt, err := time.Parse(time.RFC3339, input.StartsAt)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_STARTS_AT",
			"starts_at must be an RFC3339 timestamp.", nil)
		return
	}

	endsAt, err := time.Parse(time.RFC3339, input.EndsAt)
	if err != nil || !endsAt.After(startsAt) {
		response.Error(c, http.StatusBadRequest, "INVALID_ENDS_AT",
			"ends_at must be after starts_at.", nil)
		return
	}

	if input.Title == "" || input.Location == "" ||
		input.Capacity < 1 || input.PriceMinor < 0 ||
		len(input.Currency) != 3 || !isUppercaseCurrency(input.Currency) {
		response.Error(c, http.StatusBadRequest, "INVALID_ACTIVITY",
			"Activity fields are invalid.", nil)
		return
	}

	item, err := h.activities.Create(c.Request.Context(), CreateActivityInput{
		OwnerID:     operatorID,
		Title:       input.Title,
		Description: input.Description,
		Location:    input.Location,
		StartsAt:    startsAt,
		EndsAt:      endsAt,
		Capacity:    input.Capacity,
		PriceMinor:  input.PriceMinor,
		Currency:    input.Currency,
	})
	if err != nil {
		slog.Error("operator create activity failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": item})
}

func (h *Handler) OperatorList(c *gin.Context) {
	operatorID, ok := authenticatedOperatorID(c)
	if !ok {
		return
	}

	page, limit := 1, 20

	if value := c.Query("page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			response.Error(c, http.StatusBadRequest, "INVALID_PAGE",
				"Page must be a positive integer.", nil)
			return
		}
		page = parsed
	}

	if value := c.Query("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			response.Error(c, http.StatusBadRequest, "INVALID_LIMIT",
				"Limit must be between 1 and 100.", nil)
			return
		}
		limit = parsed
	}

	items, err := h.activities.ListByOwner(
		c.Request.Context(), operatorID, limit, (page-1)*limit,
	)
	if err != nil {
		slog.Error("operator list activities failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":       items,
		"pagination": gin.H{"page": page, "limit": limit},
	})
}

func (h *Handler) OperatorGetByID(c *gin.Context) {
	operatorID, ok := authenticatedOperatorID(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("activityID"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_ACTIVITY_ID",
			"Activity ID must be a valid UUID.", nil)
		return
	}

	item, err := h.activities.GetByOwner(c.Request.Context(), operatorID, id)
	if err != nil {
		if errors.Is(err, ErrActivityNotFound) {
			response.Error(c, http.StatusNotFound, "ACTIVITY_NOT_FOUND",
				"Activity not found.", nil)
			return
		}
		slog.Error("operator get activity failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": item})
}

func (h *Handler) OperatorUpdate(c *gin.Context) {
	operatorID, ok := authenticatedOperatorID(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("activityID"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_ACTIVITY_ID",
			"Activity ID must be a valid UUID.", nil)
		return
	}

	var input struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Location    string `json:"location"`
		StartsAt    string `json:"starts_at"`
		EndsAt      string `json:"ends_at"`
		Capacity    int    `json:"capacity"`
		PriceMinor  int64  `json:"price_minor"`
		Currency    string `json:"currency"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_BODY",
			"Invalid activity payload.", nil)
		return
	}

	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.Location = strings.TrimSpace(input.Location)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))

	startsAt, err := time.Parse(time.RFC3339, input.StartsAt)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_STARTS_AT",
			"starts_at must be an RFC3339 timestamp.", nil)
		return
	}

	endsAt, err := time.Parse(time.RFC3339, input.EndsAt)
	if err != nil || !endsAt.After(startsAt) {
		response.Error(c, http.StatusBadRequest, "INVALID_ENDS_AT",
			"ends_at must be after starts_at.", nil)
		return
	}

	if input.Title == "" || input.Location == "" ||
		input.Capacity < 1 || input.PriceMinor < 0 ||
		len(input.Currency) != 3 || !isUppercaseCurrency(input.Currency) {
		response.Error(c, http.StatusBadRequest, "INVALID_ACTIVITY",
			"Activity fields are invalid.", nil)
		return
	}

	item, err := h.activities.UpdateDetailsByOwner(
		c.Request.Context(),
		operatorID,
		id,
		UpdateActivityInput{
			Title:       input.Title,
			Description: input.Description,
			Location:    input.Location,
			StartsAt:    startsAt,
			EndsAt:      endsAt,
			Capacity:    input.Capacity,
			PriceMinor:  input.PriceMinor,
			Currency:    input.Currency,
		},
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrActivityNotFound):
			response.Error(c, http.StatusNotFound, "ACTIVITY_NOT_FOUND",
				"Activity not found.", nil)
		case errors.Is(err, ErrCapacityBelowBookings):
			response.Error(c, http.StatusConflict, "CAPACITY_TOO_LOW",
				err.Error(), nil)
		case errors.Is(err, ErrInvalidActivityTransition):
			response.Error(c, http.StatusConflict, "ACTIVITY_NOT_EDITABLE",
				err.Error(), nil)
		default:
			slog.Error("operator update activity failed", "error", err)
			response.Internal(c)
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": item})
}

func (h *Handler) OperatorChangeStatus(c *gin.Context) {
	operatorID, ok := authenticatedOperatorID(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("activityID"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_ACTIVITY_ID",
			"Activity ID must be a valid UUID.", nil)
		return
	}

	var input struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_BODY",
			"Invalid status payload.", nil)
		return
	}

	item, err := h.activities.ChangeStatusByOwner(
		c.Request.Context(),
		operatorID,
		id,
		strings.TrimSpace(input.Status),
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrActivityNotFound):
			response.Error(c, http.StatusNotFound, "ACTIVITY_NOT_FOUND",
				"Activity not found.", nil)
		case errors.Is(err, ErrInvalidActivityTransition):
			response.Error(c, http.StatusConflict, "INVALID_STATUS_TRANSITION",
				err.Error(), nil)
		default:
			slog.Error("operator change activity status failed", "error", err)
			response.Internal(c)
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": item})
}

func (h *Handler) OperatorUploadImage(c *gin.Context) {
	if h.images == nil {
		response.Error(
			c,
			http.StatusServiceUnavailable,
			"IMAGE_STORAGE_UNAVAILABLE",
			"Image uploads are currently unavailable.",
			nil,
		)
		return
	}

	operatorID, ok := authenticatedOperatorID(c)
	if !ok {
		return
	}

	activityID, err := uuid.Parse(c.Param("activityID"))
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

	ctx := c.Request.Context()

	// Verify ownership before accepting or saving the image.
	existing, err := h.imageActivities.GetByOwner(ctx, operatorID, activityID)
	if err != nil {
		if errors.Is(err, ErrActivityNotFound) {
			response.Error(
				c,
				http.StatusNotFound,
				"ACTIVITY_NOT_FOUND",
				"Activity not found.",
				nil,
			)
			return
		}

		slog.Error("operator image upload ownership check failed", "error", err)
		response.Internal(c)
		return
	}

	// Limit the entire multipart request, including its boundaries and headers.
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		6<<20,
	)

	file, header, err := c.Request.FormFile("image")
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			response.Error(
				c,
				http.StatusRequestEntityTooLarge,
				"IMAGE_TOO_LARGE",
				"Image upload must not exceed 5 MiB.",
				nil,
			)
			return
		}

		response.Error(
			c,
			http.StatusBadRequest,
			"INVALID_IMAGE_UPLOAD",
			"Provide an image using the multipart field named image.",
			nil,
		)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Warn("close uploaded image", "error", err)
		}
	}()

	// Reject a file whose declared size already exceeds the limit.
	// Storage independently validates the actual bytes and image format.
	if header.Size > 5<<20 {
		response.Error(
			c,
			http.StatusRequestEntityTooLarge,
			"IMAGE_TOO_LARGE",
			"Image upload must not exceed 5 MiB.",
			nil,
		)
		return
	}

	imageURL, err := h.images.Save(ctx, file)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			response.Error(
				c,
				http.StatusRequestEntityTooLarge,
				"IMAGE_TOO_LARGE",
				"Image upload must not exceed 5 MiB.",
				nil,
			)
			return
		}

		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return
		}

		slog.Warn("operator image upload rejected", "error", err)
		response.Error(
			c,
			http.StatusBadRequest,
			"INVALID_IMAGE",
			"Image must be a valid JPEG or PNG within the permitted size and dimensions.",
			nil,
		)
		return
	}

	updated, err := h.imageActivities.UpdateImageURLByOwner(
		ctx,
		operatorID,
		activityID,
		imageURL,
	)
	if err != nil {
		if cleanupErr := h.images.Delete(context.Background(), imageURL); cleanupErr != nil {
			slog.Error("cleanup uploaded image failed", "error", cleanupErr)
		}

		if errors.Is(err, ErrActivityNotFound) {
			response.Error(
				c,
				http.StatusNotFound,
				"ACTIVITY_NOT_FOUND",
				"Activity not found.",
				nil,
			)
			return
		}

		slog.Error("save activity image URL failed", "error", err)
		response.Internal(c)
		return
	}

	// Delete the previous image only after the database update succeeds.
	// Use a separate timeout so a disconnected client doesn't cancel cleanup.
	if existing.ImageURL != nil && *existing.ImageURL != imageURL {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)

		err := h.images.Delete(cleanupCtx, *existing.ImageURL)
		cancel()

		if err != nil {
			slog.Warn(
				"delete previous activity image failed",
				"error", err,
			)
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": updated})
}
