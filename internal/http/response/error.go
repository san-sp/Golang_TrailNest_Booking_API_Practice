package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Error writes the API's standard JSON error envelope.
// Details can contain safe, field-specific validation messages.
func Error(
	c *gin.Context,
	status int,
	code string,
	message string,
	details map[string]string,
) {
	requestID, _ := c.Get("request_id")

	c.JSON(status, gin.H{
		"error": gin.H{
			"code":       code,
			"message":    message,
			"details":    details,
			"request_id": requestID,
		},
	})
}

// Internal writes a generic server error without exposing database
// messages, SQL, stack traces, or other internal implementation details.
func Internal(c *gin.Context) {
	Error(
		c,
		http.StatusInternalServerError,
		"INTERNAL_ERROR",
		"An unexpected error occurred",
		nil,
	)
}
