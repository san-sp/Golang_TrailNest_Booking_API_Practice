package activity

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"trailnest/internal/http/response"
)

const maxActivityRequestBody int64 = 1 << 20 // 1 MiB

func bindActivityJSON(c *gin.Context, target any) error {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxActivityRequestBody,
	)

	return c.ShouldBindJSON(target)
}

func respondActivityBodyError(c *gin.Context, err error, code, message string) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		response.Error(
			c,
			http.StatusRequestEntityTooLarge,
			"REQUEST_TOO_LARGE",
			"Request body exceeds the maximum allowed size.",
			nil,
		)
		return
	}

	response.Error(c, http.StatusBadRequest, code, message, nil)
}
