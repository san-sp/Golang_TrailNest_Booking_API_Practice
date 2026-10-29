package auth

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"trailnest/internal/http/response"
	"trailnest/internal/user"
)

type ResendVerificationHandler struct {
	users  *user.Repository
	mailer *EmailMailer
}

func NewResendVerificationHandler(
	users *user.Repository,
	mailer *EmailMailer,
) *ResendVerificationHandler {
	return &ResendVerificationHandler{
		users:  users,
		mailer: mailer,
	}
}

type resendVerificationRequest struct {
	Email string `json:"email"`
}

func (h *ResendVerificationHandler) Resend(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRegistrationBody)

	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()

	var input resendVerificationRequest
	if err := decoder.Decode(&input); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			response.Error(
				c,
				http.StatusRequestEntityTooLarge,
				"REQUEST_TOO_LARGE",
				"Request body exceeds the maximum allowed size.",
				nil,
			)
			return
		}
		response.Error(c, http.StatusBadRequest, "INVALID_JSON",
			"Request body must contain valid JSON", nil)
		return
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				response.Error(
					c,
					http.StatusRequestEntityTooLarge,
					"REQUEST_TOO_LARGE",
					"Request body exceeds the maximum allowed size.",
					nil,
				)
				return
			}
		}

		response.Error(
			c,
			http.StatusBadRequest,
			"INVALID_JSON",
			"Request body must contain a single JSON object",
			nil,
		)
		return
	}

	email := strings.ToLower(strings.TrimSpace(input.Email))
	if len(email) == 0 || len(email) > 254 {
		response.Error(c, http.StatusBadRequest, "VALIDATION_ERROR",
			"Please provide a valid email address", nil)
		return
	}

	genericResponse := gin.H{
		"data": gin.H{
			"message": "If the account exists and needs verification, instructions will be sent.",
		},
	}

	userID, verified, err := h.users.FindEmailVerificationStatus(
		c.Request.Context(), email,
	)
	if errors.Is(err, user.ErrUserNotFound) {
		c.JSON(http.StatusAccepted, genericResponse)
		return
	}
	if err != nil {
		slog.Error("look up email verification status", "error", err)
		response.Internal(c)
		return
	}

	if verified {
		c.JSON(http.StatusAccepted, genericResponse)
		return
	}

	rawToken, tokenHash, err := generateEmailVerificationToken()
	if err != nil {
		slog.Error("generate verification token", "error", err)
		response.Internal(c)
		return
	}

	if err := h.users.CreateEmailVerificationToken(
		c.Request.Context(),
		userID,
		tokenHash,
		time.Now().Add(emailVerificationLifetime),
	); err != nil {
		slog.Error("save verification token", "error", err)
		response.Internal(c)
		return
	}

	if err := h.mailer.SendVerificationEmail(
		c.Request.Context(), email, rawToken,
	); err != nil {
		slog.Error("send verification email", "error", err)
		// Do not reveal account existence through the response.
	}

	c.JSON(http.StatusAccepted, genericResponse)
}
