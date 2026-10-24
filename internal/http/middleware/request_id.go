package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const RequestIDHeader = "X-Request-ID"

// RequestID assigns a server-generated ID to each incoming request.
// The ID is returned in the response so a client can report failures
// and the same ID can be attached to application logs.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := uuid.NewString()

		c.Set("request_id", requestID)
		c.Header(RequestIDHeader, requestID)

		c.Next()
	}
}
