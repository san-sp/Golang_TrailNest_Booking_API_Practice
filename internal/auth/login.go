package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"trailnest/internal/http/response"
	"trailnest/internal/user"
)

const maxLoginBody = 1 << 20

// LoginHandler authenticates users and issues access tokens.
type LoginHandler struct {
	users  *user.Repository
	tokens *TokenManager
}

// NewLoginHandler creates the login handler.
func NewLoginHandler(
	users *user.Repository,
	tokens *TokenManager,
) *LoginHandler {
	return &LoginHandler{
		users:  users,
		tokens: tokens,
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Login verifies credentials and returns a short-lived JWT.
func (h *LoginHandler) Login(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxLoginBody,
	)

	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()

	var input loginRequest

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

		response.Error(
			c,
			http.StatusBadRequest,
			"INVALID_JSON",
			"Request body must contain valid login JSON",
			nil,
		)
		return
	}

	var extra any
	if err := decoder.Decode(&extra); err != nil {
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
	if email == "" || len(email) > 254 ||
		input.Password == "" || len(input.Password) > 512 {
		response.Error(
			c,
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Please provide valid login credentials",
			nil,
		)
		return
	}

	credentials, err := h.users.FindCredentialsByEmail(
		c.Request.Context(),
		email,
	)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			// Keep the response identical for an unknown email
			// and an incorrect password.
			response.Error(
				c,
				http.StatusUnauthorized,
				"INVALID_CREDENTIALS",
				"Email or password is incorrect",
				nil,
			)
			return
		}

		slog.Error("look up login credentials", "error", err)
		response.Internal(c)
		return
	}

	valid, err := VerifyPassword(
		input.Password,
		credentials.PasswordHash,
	)
	if err != nil {
		slog.Error("verify login password", "error", err)
		response.Internal(c)
		return
	}

	if !valid {
		response.Error(
			c,
			http.StatusUnauthorized,
			"INVALID_CREDENTIALS",
			"Email or password is incorrect",
			nil,
		)
		return
	}

	if !credentials.EmailVerified {
		response.Error(
			c,
			http.StatusForbidden,
			"EMAIL_NOT_VERIFIED",
			"Please verify your email address before logging in.",
			nil,
		)
		return
	}

	accessToken, err := h.tokens.CreateAccessToken(
		credentials.User.ID,
		credentials.User.Role,
	)
	if err != nil {
		slog.Error("create access token", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_in":   int(accessTokenLifetime.Seconds()),
			"user":         credentials.User,
		},
	})
}
