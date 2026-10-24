package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"trailnest/internal/http/response"
	"trailnest/internal/user"
)

const emailVerificationLifetime = 24 * time.Hour

// generateEmailVerificationToken creates a cryptographically secure token.
// Only its hash should be stored in the database.
func generateEmailVerificationToken() (rawToken, tokenHash string, err error) {
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", "", err
	}

	rawToken = base64.RawURLEncoding.EncodeToString(randomBytes)
	tokenHash = hashEmailVerificationToken(rawToken)

	return rawToken, tokenHash, nil
}

func hashEmailVerificationToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// EmailVerificationHandler handles email verification links.
type EmailVerificationHandler struct {
	users *user.Repository
}

// NewEmailVerificationHandler creates the verification handler.
func NewEmailVerificationHandler(users *user.Repository) *EmailVerificationHandler {
	return &EmailVerificationHandler{users: users}
}

// VerifyEmail consumes a valid verification token and verifies its account.
func (h *EmailVerificationHandler) VerifyEmail(c *gin.Context) {
	token := strings.TrimSpace(c.Query("token"))
	if len(token) != 43 {
		response.Error(
			c,
			http.StatusBadRequest,
			"INVALID_VERIFICATION_TOKEN",
			"The verification link is invalid or expired",
			nil,
		)
		return
	}

	if _, err := base64.RawURLEncoding.DecodeString(token); err != nil {
		response.Error(
			c,
			http.StatusBadRequest,
			"INVALID_VERIFICATION_TOKEN",
			"The verification link is invalid or expired",
			nil,
		)
		return
	}

	err := h.users.VerifyEmailToken(
		c.Request.Context(),
		hashEmailVerificationToken(token),
	)
	if err != nil {
		if errors.Is(err, user.ErrInvalidVerificationToken) {
			response.Error(
				c,
				http.StatusBadRequest,
				"INVALID_VERIFICATION_TOKEN",
				"The verification link is invalid or expired",
				nil,
			)
			return
		}

		slog.Error("verify email", "error", err)
		response.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"message": "Email verified successfully",
		},
	})
}
