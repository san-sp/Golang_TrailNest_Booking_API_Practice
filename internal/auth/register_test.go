package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := `{"email":"test@example.com","password":"` +
		strings.Repeat("a", maxRegistrationBody+100) + `"}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/register",
		strings.NewReader(body),
	)
	recorder := httptest.NewRecorder()

	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = req

	handler := &RegisterHandler{}
	handler.Register(ctx)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"expected status %d, got %d; response: %s",
			http.StatusRequestEntityTooLarge,
			recorder.Code,
			recorder.Body.String(),
		)
	}

	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}

	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	if payload.Error.Code != "REQUEST_TOO_LARGE" {
		t.Fatalf(
			"expected error code REQUEST_TOO_LARGE, got %q",
			payload.Error.Code,
		)
	}
}
