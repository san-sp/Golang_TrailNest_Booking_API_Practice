package activity

import (
	"context"
	"errors"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"trailnest/internal/http/response"
	"trailnest/internal/pagination"
)

type ImageStorage interface {
	Save(ctx context.Context, file multipart.File) (string, error)
	Delete(ctx context.Context, imageURL string) error
}

type ActivityImageRepository interface {
	GetByOwner(
		ctx context.Context,
		ownerID uuid.UUID,
		id uuid.UUID,
	) (Activity, error)

	UpdateImageURLByOwner(
		ctx context.Context,
		ownerID uuid.UUID,
		id uuid.UUID,
		imageURL string,
	) (Activity, error)
}

type Handler struct {
	activities      *Repository
	imageActivities ActivityImageRepository
	images          ImageStorage
}

func NewHandler(activities *Repository) *Handler {
	return &Handler{
		activities:      activities,
		imageActivities: activities,
	}
}

func NewHandlerWithImageStorage(
	activities *Repository,
	images ImageStorage,
) *Handler {
	return &Handler{
		activities:      activities,
		imageActivities: activities,
		images:          images,
	}
}

func NewHandlerWithImageDependencies(
	activities *Repository,
	imageActivities ActivityImageRepository,
	images ImageStorage,
) *Handler {
	return &Handler{
		activities:      activities,
		imageActivities: imageActivities,
		images:          images,
	}
}

func (h *Handler) List(c *gin.Context) {
	page := 1
	limit := 20
	search := strings.TrimSpace(c.Query("search"))
	location := strings.TrimSpace(c.Query("location"))

	if value := c.Query("page"); value != "" {
		parsed, ok := pagination.ParsePage(value)
		if !ok {
			response.Error(
				c,
				http.StatusBadRequest,
				"INVALID_PAGE",
				"Page must be between 1 and 1000000.",
				nil,
			)
			return
		}

		page = parsed
	}

	if value := c.Query("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			response.Error(
				c,
				http.StatusBadRequest,
				"INVALID_LIMIT",
				"Limit must be between 1 and 100.",
				nil,
			)
			return
		}
		limit = parsed
	}

	offset := (page - 1) * limit

	activities, err := h.activities.ListPublished(
		c.Request.Context(),
		limit,
		offset,
		search,
		location,
	)
	if err != nil {
		slog.Error("list activities failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": activities,
		"pagination": gin.H{
			"page":  page,
			"limit": limit,
		},
	})
}

func (h *Handler) GetByID(c *gin.Context) {
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

	item, err := h.activities.GetPublishedByID(
		c.Request.Context(),
		activityID,
	)
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

		slog.Error("get activity failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": item,
	})
}

func (h *Handler) AdminCreate(c *gin.Context) {
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

	if err := bindActivityJSON(c, &input); err != nil {
		respondActivityBodyError(
			c,
			err,
			"INVALID_BODY",
			"Invalid activity payload.",
		)
		return
	}

	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.Location = strings.TrimSpace(input.Location)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))

	startsAt, err := time.Parse(time.RFC3339, input.StartsAt)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_STARTS_AT", "starts_at must be an RFC3339 timestamp.", nil)
		return
	}

	endsAt, err := time.Parse(time.RFC3339, input.EndsAt)
	if err != nil || !endsAt.After(startsAt) {
		response.Error(c, http.StatusBadRequest, "INVALID_ENDS_AT", "ends_at must be a valid timestamp after starts_at.", nil)
		return
	}

	if input.Title == "" || input.Location == "" ||
		input.Capacity < 1 || input.PriceMinor < 0 ||
		len(input.Currency) != 3 {
		response.Error(c, http.StatusBadRequest, "INVALID_ACTIVITY", "Title, location, capacity, price, and currency must be valid.", nil)
		return
	}

	value, exists := c.Get("user_id")
	if !exists {
		response.Error(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication is required.", nil)
		return
	}

	ownerID, err := uuid.Parse(value.(string))
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "INVALID_TOKEN", "Invalid authenticated user ID.", nil)
		return
	}

	item, err := h.activities.Create(c.Request.Context(), CreateActivityInput{
		OwnerID:     ownerID,
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
		slog.Error("admin create activity failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": item})
}

func (h *Handler) AdminList(c *gin.Context) {
	page, limit := 1, 20

	if value := c.Query("page"); value != "" {
		parsed, ok := pagination.ParsePage(value)
		if !ok {
			response.Error(
				c,
				http.StatusBadRequest,
				"INVALID_PAGE",
				"Page must be between 1 and 1000000.",
				nil,
			)
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

	items, err := h.activities.ListAdmin(c.Request.Context(), limit, (page-1)*limit)
	if err != nil {
		slog.Error("admin list activities failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":       items,
		"pagination": gin.H{"page": page, "limit": limit},
	})
}

func (h *Handler) AdminGetByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("activityID"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_ACTIVITY_ID", "Activity ID must be a valid UUID.", nil)
		return
	}

	item, err := h.activities.GetAdminByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrActivityNotFound) {
			response.Error(c, http.StatusNotFound, "ACTIVITY_NOT_FOUND", "Activity not found.", nil)
			return
		}
		slog.Error("admin get activity failed", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": item})
}

func (h *Handler) AdminUpdate(c *gin.Context) {
	id, err := uuid.Parse(c.Param("activityID"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_ACTIVITY_ID", "Activity ID must be a valid UUID.", nil)
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

	if err := bindActivityJSON(c, &input); err != nil {
		respondActivityBodyError(
			c,
			err,
			"INVALID_BODY",
			"Invalid activity payload.",
		)
		return
	}

	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.Location = strings.TrimSpace(input.Location)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))

	startsAt, err := time.Parse(time.RFC3339, input.StartsAt)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_STARTS_AT", "starts_at must be an RFC3339 timestamp.", nil)
		return
	}

	endsAt, err := time.Parse(time.RFC3339, input.EndsAt)
	if err != nil || !endsAt.After(startsAt) {
		response.Error(c, http.StatusBadRequest, "INVALID_ENDS_AT", "ends_at must be after starts_at.", nil)
		return
	}

	if input.Title == "" || input.Location == "" ||
		input.Capacity < 1 || input.PriceMinor < 0 ||
		len(input.Currency) != 3 ||
		!isUppercaseCurrency(input.Currency) {
		response.Error(c, http.StatusBadRequest, "INVALID_ACTIVITY", "Activity fields are invalid.", nil)
		return
	}

	item, err := h.activities.UpdateDetails(
		c.Request.Context(),
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
			response.Error(c, http.StatusNotFound, "ACTIVITY_NOT_FOUND", "Activity not found.", nil)
		case errors.Is(err, ErrCapacityBelowBookings):
			response.Error(c, http.StatusConflict, "CAPACITY_TOO_LOW", err.Error(), nil)
		case errors.Is(err, ErrInvalidActivityTransition):
			response.Error(c, http.StatusConflict, "ACTIVITY_NOT_EDITABLE", err.Error(), nil)
		default:
			slog.Error("admin update activity failed", "error", err)
			response.Internal(c)
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": item})
}

func (h *Handler) AdminChangeStatus(c *gin.Context) {
	id, err := uuid.Parse(c.Param("activityID"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_ACTIVITY_ID", "Activity ID must be a valid UUID.", nil)
		return
	}

	var input struct {
		Status string `json:"status"`
	}

	if err := bindActivityJSON(c, &input); err != nil {
		respondActivityBodyError(
			c,
			err,
			"INVALID_BODY",
			"Invalid status payload.",
		)
		return
	}

	item, err := h.activities.ChangeStatus(
		c.Request.Context(),
		id,
		strings.TrimSpace(input.Status),
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrActivityNotFound):
			response.Error(c, http.StatusNotFound, "ACTIVITY_NOT_FOUND", "Activity not found.", nil)
		case errors.Is(err, ErrInvalidActivityTransition):
			response.Error(c, http.StatusConflict, "INVALID_STATUS_TRANSITION", err.Error(), nil)
		default:
			slog.Error("admin change activity status failed", "error", err)
			response.Internal(c)
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": item})
}

func isUppercaseCurrency(value string) bool {
	for _, ch := range value {
		if ch < 'A' || ch > 'Z' {
			return false
		}
	}
	return true
}
