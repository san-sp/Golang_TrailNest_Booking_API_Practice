package middleware

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type rateLimitEntry struct {
	count       int
	windowStart time.Time
}

type RateLimiter struct {
	mu              sync.Mutex
	entries         map[string]rateLimitEntry
	limit           int
	window          time.Duration
	lastCleanup     time.Time
	cleanupInterval time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		entries:         make(map[string]rateLimitEntry),
		limit:           limit,
		window:          window,
		cleanupInterval: time.Minute,
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

		// Periodically remove expired entries to bound stale state.
		if rl.lastCleanup.IsZero() ||
			now.Sub(rl.lastCleanup) >= rl.cleanupInterval {
			for ip, entry := range rl.entries {
				if now.Sub(entry.windowStart) >= rl.window {
					delete(rl.entries, ip)
				}
			}
			rl.lastCleanup = now
		}

		entry, exists := rl.entries[key]
		if !exists || now.Sub(entry.windowStart) >= rl.window {
			entry = rateLimitEntry{windowStart: now}
		}

		if entry.count >= rl.limit {
			remaining := rl.window - now.Sub(entry.windowStart)
			retryAfter := int(math.Ceil(remaining.Seconds()))
			if retryAfter < 1 {
				retryAfter = 1
			}

			rl.entries[key] = entry
			rl.mu.Unlock()

			c.Header("Retry-After", strconv.Itoa(retryAfter))
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
