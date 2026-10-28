package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders adds defensive HTTP response headers.
// HSTS is intentionally omitted until HTTPS is configured for deployment.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

		c.Next()
	}
}
