package activity

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBindActivityJSONRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.POST("/test", func(c *gin.Context) {
		var input struct {
			Name string `json:"name"`
		}

		err := bindActivityJSON(c, &input)
		if err != nil {
			respondActivityBodyError(
				c,
				err,
				"INVALID_BODY",
				"Invalid activity payload.",
			)
			return
		}

		c.Status(http.StatusOK)
	})

	body := `{"name":"` +
		strings.Repeat("x", int(maxActivityRequestBody)) +
		`"}`

	request := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	require.Contains(t, recorder.Body.String(), "REQUEST_TOO_LARGE")
}
