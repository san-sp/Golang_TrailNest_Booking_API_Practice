package auth

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"trailnest/internal/http/response"
	"trailnest/internal/user"
)

const maxRegistrationBody = 1 << 20 // 1 MiB

type RegisterHandler struct {
	users  *user.Repository
	mailer *EmailMailer
}

func NewRegisterHandler(
	users *user.Repository,
	mailer *EmailMailer,
) *RegisterHandler {
	return &RegisterHandler{
		users:  users,
		mailer: mailer,
	}
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Register validates the request, hashes the password, and creates
// a customer account. Privileged roles cannot be selected by clients.
func (h *RegisterHandler) Register(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxRegistrationBody,
	)

	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()

	var input registerRequest
	if err := decoder.Decode(&input); err != nil {
		response.Error(
			c,
			http.StatusBadRequest,
			"INVALID_JSON",
			"Request body must contain valid registration JSON",
			nil,
		)
		return
	}

	// A second decode must reach EOF; otherwise the body contains
	// trailing data or another JSON value.
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
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
		response.Error(
			c,
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Please provide a valid email address",
			map[string]string{"email": "must be between 1 and 254 characters"},
		)
		return
	}

	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		response.Error(
			c,
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Please provide a valid email address",
			map[string]string{"email": "must be a valid email address"},
		)
		return
	}

	// Use a byte limit as well as a character limit to avoid excessive
	// password input and unbounded hashing work.
	if !utf8.ValidString(input.Password) ||
		len(input.Password) < 12 ||
		len(input.Password) > 128 {
		response.Error(
			c,
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Please correct the registration details",
			map[string]string{
				"password": "must be 12–128 bytes of valid UTF-8",
			},
		)
		return
	}

	passwordHash, err := HashPassword(input.Password)
	if err != nil {
		slog.Error("hash registration password", "error", err)
		response.Internal(c)
		return
	}

	created, err := h.users.CreateCustomer(
		c.Request.Context(),
		email,
		passwordHash,
	)
	if err != nil {
		if errors.Is(err, user.ErrEmailTaken) {
			response.Error(
				c,
				http.StatusConflict,
				"EMAIL_ALREADY_REGISTERED",
				"An account with this email already exists",
				nil,
			)
			return
		}

		slog.Error("create customer account", "error", err)
		response.Internal(c)
		return
	}

	rawToken, tokenHash, err := generateEmailVerificationToken()
	if err != nil {
		slog.Error("generate email verification token", "error", err)
		response.Internal(c)
		return
	}

	expiresAt := time.Now().Add(emailVerificationLifetime)

	if err := h.users.CreateEmailVerificationToken(
		c.Request.Context(),
		created.ID,
		tokenHash,
		expiresAt,
	); err != nil {
		slog.Error("save email verification token", "error", err)
		response.Internal(c)
		return
	}

	if err := h.mailer.SendVerificationEmail(
		c.Request.Context(),
		created.Email,
		rawToken,
	); err != nil {
		slog.Error("send verification email", "error", err)
		response.Error(
			c,
			http.StatusServiceUnavailable,
			"EMAIL_DELIVERY_FAILED",
			"We couldn't send the verification email. Please try again later.",
			nil,
		)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"data": gin.H{
			"message": "Registration successful. Please check your email to verify your account.",
			"user":    created,
		},
	})
}
