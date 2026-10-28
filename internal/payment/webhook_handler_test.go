package payment

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWebhookHandlerMissingConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewWebhookHandler(nil, nil, "")
	router := gin.New()
	router.POST("/webhooks/razorpay", handler.Handle)

	req := httptest.NewRequest(
		http.MethodPost,
		"/webhooks/razorpay",
		nil,
	)
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d",
			http.StatusServiceUnavailable, recorder.Code)
	}
}

func TestWebhookHandlerRejectsInvalidSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewWebhookHandler(
		nil,
		&RazorpayClient{},
		"test_webhook_secret",
	)
	router := gin.New()
	router.POST("/webhooks/razorpay", handler.Handle)

	req := httptest.NewRequest(
		http.MethodPost,
		"/webhooks/razorpay",
		bytes.NewReader([]byte(`{"event":"payment.captured"}`)),
	)
	req.Header.Set("X-Razorpay-Signature", "invalid-signature")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d",
			http.StatusUnauthorized, recorder.Code)
	}
}

func TestWebhookHandlerRejectsMalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const secret = "test_webhook_secret"
	body := []byte(`{"event":`)

	handler := NewWebhookHandler(
		nil,
		&RazorpayClient{},
		secret,
	)
	router := gin.New()
	router.POST("/webhooks/razorpay", handler.Handle)

	req := httptest.NewRequest(
		http.MethodPost,
		"/webhooks/razorpay",
		bytes.NewReader(body),
	)
	req.Header.Set(
		"X-Razorpay-Signature",
		testHMAC(string(body), secret),
	)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d",
			http.StatusBadRequest, recorder.Code)
	}
}

func TestWebhookHandlerAcknowledgesUnsupportedEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const secret = "test_webhook_secret"
	body := []byte(`{"event":"refund.created"}`)

	handler := NewWebhookHandler(
		nil,
		&RazorpayClient{},
		secret,
	)
	router := gin.New()
	router.POST("/webhooks/razorpay", handler.Handle)

	req := httptest.NewRequest(
		http.MethodPost,
		"/webhooks/razorpay",
		bytes.NewReader(body),
	)
	req.Header.Set(
		"X-Razorpay-Signature",
		testHMAC(string(body), secret),
	)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusOK,
			recorder.Code,
		)
	}
}

func TestWebhookHandlerRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewWebhookHandler(
		nil,
		&RazorpayClient{},
		"test_webhook_secret",
	)

	router := gin.New()
	router.POST("/webhooks/razorpay", handler.Handle)

	body := strings.Repeat("a", (1<<20)+1)
	req := httptest.NewRequest(
		http.MethodPost,
		"/webhooks/razorpay",
		strings.NewReader(body),
	)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"expected status %d, got %d; response: %s",
			http.StatusRequestEntityTooLarge,
			recorder.Code,
			recorder.Body.String(),
		)
	}
}
