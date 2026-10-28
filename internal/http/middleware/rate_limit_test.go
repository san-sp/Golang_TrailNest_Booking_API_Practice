package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRateLimiterAllowsRequestsWithinLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter := NewRateLimiter(2, time.Minute)
	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
	}
}

func TestRateLimiterRejectsRequestOverLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter := NewRateLimiter(1, time.Minute)
	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	require.NotEmpty(t, second.Header().Get("Retry-After"))
}

func TestRateLimiterDisabledForInvalidConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		limit  int
		window time.Duration
	}{
		{name: "zero limit", limit: 0, window: time.Minute},
		{name: "negative limit", limit: -1, window: time.Minute},
		{name: "zero window", limit: 1, window: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewRateLimiter(tt.limit, tt.window)
			router := gin.New()
			router.Use(limiter.Middleware())
			router.GET("/", func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			for i := 0; i < 3; i++ {
				rec := httptest.NewRecorder()
				router.ServeHTTP(
					rec,
					httptest.NewRequest(http.MethodGet, "/", nil),
				)
				require.Equal(t, http.StatusOK, rec.Code)
			}
		})
	}
}

func TestRateLimiterResetsAfterWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter := NewRateLimiter(1, 30*time.Millisecond)
	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	request := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		router.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, request().Code)
	require.Equal(t, http.StatusTooManyRequests, request().Code)

	time.Sleep(50 * time.Millisecond)

	require.Equal(t, http.StatusOK, request().Code)
}

func TestRateLimiterConcurrentRequestsRespectLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		limit       = 10
		concurrency = 50
	)

	limiter := NewRateLimiter(limit, time.Minute)
	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	start := make(chan struct{})
	statuses := make(chan int, concurrency)

	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			router.ServeHTTP(rec, req)
			statuses <- rec.Code
		}()
	}

	close(start)
	wg.Wait()
	close(statuses)

	allowed, rejected := 0, 0

	for status := range statuses {
		switch status {
		case http.StatusOK:
			allowed++
		case http.StatusTooManyRequests:
			rejected++
		default:
			t.Fatalf("unexpected HTTP status: %d", status)
		}
	}

	require.Equal(t, limit, allowed)
	require.Equal(t, concurrency-limit, rejected)
}

func TestRateLimiterTracksClientsIndependently(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter := NewRateLimiter(1, time.Minute)
	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	request := func(remoteAddr string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remoteAddr
		router.ServeHTTP(rec, req)
		return rec
	}

	firstClient := request("192.0.2.10:12345")
	require.Equal(t, http.StatusOK, firstClient.Code)

	firstClientAgain := request("192.0.2.10:12346")
	require.Equal(t, http.StatusTooManyRequests, firstClientAgain.Code)

	secondClient := request("192.0.2.20:12345")
	require.Equal(t, http.StatusOK, secondClient.Code)
}

func TestRateLimiterCannotBeBypassedWithSpoofedForwardedIP(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter := NewRateLimiter(1, time.Minute)
	router := gin.New()

	// No trusted proxies are configured.
	require.NoError(t, router.SetTrustedProxies(nil))

	router.Use(limiter.Middleware())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	request := func(forwardedIP string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.10:12345"

		// A client can freely supply this header.
		req.Header.Set("X-Forwarded-For", forwardedIP)

		router.ServeHTTP(rec, req)
		return rec
	}

	first := request("198.51.100.10")
	require.Equal(t, http.StatusOK, first.Code)

	// Changing the forwarded IP must not create a new rate-limit identity.
	second := request("203.0.113.20")
	require.Equal(t, http.StatusTooManyRequests, second.Code)
}

func TestRateLimiterUsesForwardedIPFromTrustedProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter := NewRateLimiter(1, time.Minute)
	router := gin.New()

	// Trust only this specific reverse proxy.
	require.NoError(t, router.SetTrustedProxies([]string{"192.0.2.10"}))

	router.Use(limiter.Middleware())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	request := func(forwardedIP string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.10:8080"
		req.Header.Set("X-Forwarded-For", forwardedIP)

		router.ServeHTTP(rec, req)
		return rec
	}

	// These requests come through the trusted proxy but represent
	// different client IPs, so each gets its own rate-limit bucket.
	first := request("198.51.100.10")
	require.Equal(t, http.StatusOK, first.Code)

	second := request("203.0.113.20")
	require.Equal(t, http.StatusOK, second.Code)

	// The first client's limit is still enforced.
	firstAgain := request("198.51.100.10")
	require.Equal(t, http.StatusTooManyRequests, firstAgain.Code)
}

func TestRateLimiterRejectsNewClientsAtCapacity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter := NewRateLimiter(2, time.Minute)
	limiter.maxEntries = 2

	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	request := func(ip string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":12345"
		router.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, request("192.0.2.10").Code)
	require.Equal(t, http.StatusOK, request("192.0.2.20").Code)

	// Existing clients retain their counters at capacity.
	require.Equal(t, http.StatusOK, request("192.0.2.10").Code)

	// A new client is rejected rather than evicting active entries.
	rejected := request("192.0.2.30")
	require.Equal(t, http.StatusTooManyRequests, rejected.Code)
	require.NotEmpty(t, rejected.Header().Get("Retry-After"))
	require.Len(t, limiter.entries, 2)
}

func TestRateLimiterRecoversCapacityAfterExpiredEntry(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter := NewRateLimiter(2, time.Minute)
	limiter.maxEntries = 2

	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	request := func(ip string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":12345"
		router.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, request("192.0.2.10").Code)
	require.Equal(t, http.StatusOK, request("192.0.2.20").Code)

	// Make one entry expired without waiting for a real minute.
	limiter.mu.Lock()
	entry := limiter.entries["192.0.2.10"]
	entry.windowStart = time.Now().Add(-2 * time.Minute)
	limiter.entries["192.0.2.10"] = entry

	// Prevent periodic cleanup from running on the next request.
	// This forces the limiter to recover capacity through its full-map path.
	limiter.lastCleanup = time.Now()
	limiter.mu.Unlock()

	// Capacity-triggered cleanup should free the expired slot.
	require.Equal(t, http.StatusOK, request("192.0.2.30").Code)
	require.Len(t, limiter.entries, 2)
	require.NotContains(t, limiter.entries, "192.0.2.10")
}
