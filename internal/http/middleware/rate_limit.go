package middleware

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const maxRateLimitEntries = 10_000

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
	maxEntries      int
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		entries:         make(map[string]rateLimitEntry),
		limit:           limit,
		window:          window,
		cleanupInterval: time.Minute,
		maxEntries:      maxRateLimitEntries,
	}
}

func (rl *RateLimiter) removeExpiredEntries(now time.Time) {
	for ip, entry := range rl.entries {
		if now.Sub(entry.windowStart) >= rl.window {
			delete(rl.entries, ip)
		}
	}
}

func (rl *RateLimiter) retryAfterForCapacity(now time.Time) int {
	var earliestExpiry time.Time

	for _, entry := range rl.entries {
		expiry := entry.windowStart.Add(rl.window)
		if earliestExpiry.IsZero() || expiry.Before(earliestExpiry) {
			earliestExpiry = expiry
		}
	}

	if earliestExpiry.IsZero() {
		return 1
	}

	seconds := int(math.Ceil(earliestExpiry.Sub(now).Seconds()))
	if seconds < 1 {
		return 1
	}

	return seconds
}

func rateLimitExceeded(c *gin.Context, retryAfter int) {
	c.Header("Retry-After", strconv.Itoa(retryAfter))
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"error": gin.H{
			"code":    "RATE_LIMIT_EXCEEDED",
			"message": "Too many requests. Please try again later.",
		},
	})
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

		if rl.lastCleanup.IsZero() ||
			now.Sub(rl.lastCleanup) >= rl.cleanupInterval {
			rl.removeExpiredEntries(now)
			rl.lastCleanup = now
		}

		entry, exists := rl.entries[key]

		if exists && now.Sub(entry.windowStart) >= rl.window {
			entry = rateLimitEntry{windowStart: now}
		} else if !exists {
			if len(rl.entries) >= rl.maxEntries {
				// Clean expired entries immediately when capacity is needed.
				rl.removeExpiredEntries(now)
			}

			if len(rl.entries) >= rl.maxEntries {
				retryAfter := rl.retryAfterForCapacity(now)
				rl.mu.Unlock()

				rateLimitExceeded(c, retryAfter)
				return
			}

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

			rateLimitExceeded(c, retryAfter)
			return
		}

		entry.count++
		rl.entries[key] = entry
		rl.mu.Unlock()

		c.Next()
	}
}
