package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLoginRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := `{"email":"test@example.com","password":"` +
		strings.Repeat("a", maxLoginBody+100) + `"}`

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/login",
		strings.NewReader(body),
	)

	handler := &LoginHandler{}
	handler.Login(ctx)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"expected status %d, got %d; response: %s",
			http.StatusRequestEntityTooLarge,
			recorder.Code,
			recorder.Body.String(),
		)
	}
}

func TestResendVerificationRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := `{"email":"test@example.com","padding":"` +
		strings.Repeat("a", maxRegistrationBody+100) + `"}`

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/resend-verification",
		strings.NewReader(body),
	)

	handler := &ResendVerificationHandler{}
	handler.Resend(ctx)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"expected status %d, got %d; response: %s",
			http.StatusRequestEntityTooLarge,
			recorder.Code,
			recorder.Body.String(),
		)
	}
}
