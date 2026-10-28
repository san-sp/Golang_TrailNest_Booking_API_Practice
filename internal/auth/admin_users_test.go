package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAdminUserRoutesRejectNonAdminRoles(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tokens, err := NewTokenManager(testJWTSecret)
	require.NoError(t, err)

	// A nil repository is intentional: unauthorized requests must be
	// rejected by middleware before either handler accesses the repository.
	handler := NewAdminUserHandler(nil)

	router := gin.New()
	admin := router.Group("/api/v1/admin")
	admin.Use(
		RequireAuthentication(tokens),
		RequireRole("admin"),
	)
	admin.GET("/users", handler.List)
	admin.PATCH("/users/:userID/role", handler.PromoteToOperator)

	testCases := []struct {
		name   string
		role   string
		method string
		path   string
	}{
		{
			name:   "customer cannot list users",
			role:   "customer",
			method: http.MethodGet,
			path:   "/api/v1/admin/users",
		},
		{
			name:   "operator cannot list users",
			role:   "operator",
			method: http.MethodGet,
			path:   "/api/v1/admin/users",
		},
		{
			name:   "customer cannot promote a user",
			role:   "customer",
			method: http.MethodPatch,
			path:   "/api/v1/admin/users/" + uuid.NewString() + "/role",
		},
		{
			name:   "operator cannot promote a user",
			role:   "operator",
			method: http.MethodPatch,
			path:   "/api/v1/admin/users/" + uuid.NewString() + "/role",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			token, err := tokens.CreateAccessToken(uuid.New(), tc.role)
			require.NoError(t, err)

			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			require.Equal(t, http.StatusForbidden, rec.Code)
			require.Contains(t, rec.Body.String(), "FORBIDDEN")
		})
	}
}

func TestAdminUserRoutesRejectMissingAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tokens, err := NewTokenManager(testJWTSecret)
	require.NoError(t, err)

	handler := NewAdminUserHandler(nil)

	router := gin.New()
	admin := router.Group("/api/v1/admin")
	admin.Use(
		RequireAuthentication(tokens),
		RequireRole("admin"),
	)
	admin.GET("/users", handler.List)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/users",
		nil,
	)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), "UNAUTHORIZED")
}
