package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const testJWTSecret = "test-secret-that-is-at-least-32-bytes-long"

func newMiddlewareTestRouter(
	t *testing.T,
	role string,
) (*gin.Engine, string) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	tokens, err := NewTokenManager(testJWTSecret)
	require.NoError(t, err)

	userID := uuid.New()
	token, err := tokens.CreateAccessToken(userID, role)
	require.NoError(t, err)

	router := gin.New()
	router.GET(
		"/protected",
		RequireAuthentication(tokens),
		func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"user_id":   c.GetString("user_id"),
				"user_role": c.GetString("user_role"),
			})
		},
	)

	return router, token
}

func TestRequireAuthenticationRejectsMissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tokens, err := NewTokenManager(testJWTSecret)
	require.NoError(t, err)

	router := gin.New()
	router.GET("/protected", RequireAuthentication(tokens), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuthenticationRejectsMalformedBearerHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tokens, err := NewTokenManager(testJWTSecret)
	require.NoError(t, err)

	router := gin.New()
	router.GET("/protected", RequireAuthentication(tokens), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	tests := []struct {
		name   string
		header string
	}{
		{name: "missing token", header: "Bearer"},
		{name: "wrong scheme", header: "Basic abc"},
		{name: "extra fields", header: "Bearer abc extra"},
		{name: "empty header", header: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			require.Equal(t, http.StatusUnauthorized, rec.Code)
		})
	}
}

func TestRequireAuthenticationRejectsInvalidToken(t *testing.T) {
	router, _ := newMiddlewareTestRouter(t, "customer")

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.token")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuthenticationAcceptsValidTokenAndSetsClaims(t *testing.T) {
	router, token := newMiddlewareTestRouter(t, "customer")

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"user_id"`)
	require.Contains(t, rec.Body.String(), `"user_role":"customer"`)
}

func TestRequireRole(t *testing.T) {
	tests := []struct {
		name           string
		tokenRole      string
		allowedRole    string
		expectedStatus int
	}{
		{
			name:           "admin allowed",
			tokenRole:      "admin",
			allowedRole:    "admin",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "customer denied admin access",
			tokenRole:      "customer",
			allowedRole:    "admin",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "operator denied admin access",
			tokenRole:      "operator",
			allowedRole:    "admin",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "operator allowed operator access",
			tokenRole:      "operator",
			allowedRole:    "operator",
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)

			tokens, err := NewTokenManager(testJWTSecret)
			require.NoError(t, err)

			token, err := tokens.CreateAccessToken(uuid.New(), tt.tokenRole)
			require.NoError(t, err)

			router := gin.New()
			router.GET(
				"/protected",
				RequireAuthentication(tokens),
				RequireRole(tt.allowedRole),
				func(c *gin.Context) {
					c.Status(http.StatusOK)
				},
			)

			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Authorization", "Bearer "+token)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			require.Equal(t, tt.expectedStatus, rec.Code)
		})
	}
}

func TestRequireRoleRejectsMissingRoleContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET(
		"/protected",
		RequireRole("admin"),
		func(c *gin.Context) {
			c.Status(http.StatusOK)
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}
