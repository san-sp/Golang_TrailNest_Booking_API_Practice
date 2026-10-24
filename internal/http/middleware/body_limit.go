package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func MaxBodyBytes(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if maxBytes <= 0 {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{
					"code":    "INTERNAL_ERROR",
					"message": "An internal error occurred.",
				},
			})
			return
		}

		c.Request.Body = http.MaxBytesReader(
			c.Writer,
			c.Request.Body,
			maxBytes,
		)

		c.Next()
	}
}
