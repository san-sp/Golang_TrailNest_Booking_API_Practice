package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type rateLimitEntry struct {
	count       int
	windowStart time.Time
}

type RateLimiter struct {
	mu      sync.Mutex
	entries map[string]rateLimitEntry
	limit   int
	window  time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		entries: make(map[string]rateLimitEntry),
		limit:   limit,
		window:  window,
	}
}

func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if rl.limit < 1 || rl.window <= 0 {
			c.Next()
			return
		}

		key := c.ClientIP()
		now := time.Now()

		rl.mu.Lock()

		entry, exists := rl.entries[key]
		if !exists || now.Sub(entry.windowStart) >= rl.window {
			entry = rateLimitEntry{
				count:       0,
				windowStart: now,
			}
		}

		if entry.count >= rl.limit {
			rl.entries[key] = entry
			rl.mu.Unlock()

			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"code":    "RATE_LIMIT_EXCEEDED",
					"message": "Too many requests. Please try again later.",
				},
			})
			return
		}

		entry.count++
		rl.entries[key] = entry
		rl.mu.Unlock()

		c.Next()
	}
}
