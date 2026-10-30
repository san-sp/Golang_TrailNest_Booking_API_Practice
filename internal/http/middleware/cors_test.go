package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func TestCORSAllowsConfiguredOrigins(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		origin string
		want   string
	}{
		{
			name:   "allows configured origin",
			origin: "https://sansp.in",
			want:   "https://sansp.in",
		},
		{
			name:   "allows another configured origin",
			origin: "https://www.sansp.in",
			want:   "https://www.sansp.in",
		},
		{
			name:   "does not allow unconfigured origin",
			origin: "https://evil.example.com",
			want:   "",
		},
	}

	allowedOrigins := []string{
		"https://sansp.in",
		"https://www.sansp.in",
	}

	router := gin.New()
	router.Use(cors.New(cors.Config{
		AllowOrigins: allowedOrigins,
		AllowMethods: []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodOptions,
		},
		AllowHeaders: []string{
			"Origin",
			"Content-Type",
			"Authorization",
		},
	}))

	router.GET("/healthz", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(
				http.MethodGet,
				"/healthz",
				nil,
			)
			req.Header.Set("Origin", tt.origin)

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)

			got := recorder.Header().Get("Access-Control-Allow-Origin")

			if got != tt.want {
				t.Errorf(
					"Access-Control-Allow-Origin = %q, want %q",
					got,
					tt.want,
				)
			}
		})
	}
}

func TestCORSPreflightRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(cors.New(cors.Config{
		AllowOrigins: []string{"https://sansp.in"},
		AllowMethods: []string{
			http.MethodGet,
			http.MethodPost,
		},
		AllowHeaders: []string{
			"Authorization",
			"Content-Type",
		},
	}))

	req := httptest.NewRequest(
		http.MethodOptions,
		"/healthz",
		nil,
	)
	req.Header.Set("Origin", "https://sansp.in")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "Authorization,Content-Type")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf(
			"status = %d, want %d",
			recorder.Code,
			http.StatusNoContent,
		)
	}

	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://sansp.in" {
		t.Errorf("Allow-Origin = %q", got)
	}

	if got := recorder.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("expected Access-Control-Allow-Methods header")
	}

	if got := recorder.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("expected Access-Control-Allow-Headers header")
	}
}
