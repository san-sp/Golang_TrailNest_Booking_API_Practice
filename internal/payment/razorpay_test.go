package payment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreatePaymentLinkSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/payment_links" {
			t.Errorf("path = %q, want /v1/payment_links", r.URL.Path)
		}

		username, password, ok := r.BasicAuth()
		if !ok || username != "test_key" || password != "test_secret" {
			t.Error("request has incorrect Basic Auth credentials")
		}

		var request razorpayPaymentLinkRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		if request.Amount != 100000 ||
			request.Currency != "INR" ||
			request.ReferenceID != "payment-123" ||
			request.AcceptPartial {
			t.Errorf("unexpected request: %+v", request)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(RazorpayPaymentLink{
			ID:       "plink_test123",
			ShortURL: "https://rzp.io/i/test123",
			Amount:   100000,
			Currency: "INR",
			Status:   "created",
		})
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}

	link, err := client.CreatePaymentLink(
		context.Background(),
		100000,
		"inr",
		"payment-123",
		"TrailNest booking",
	)
	if err != nil {
		t.Fatalf("CreatePaymentLink() error = %v", err)
	}

	if link.ID != "plink_test123" {
		t.Errorf("link ID = %q, want plink_test123", link.ID)
	}
	if link.ShortURL != "https://rzp.io/i/test123" {
		t.Errorf("short URL = %q", link.ShortURL)
	}
}

func TestCreatePaymentLinkRejectsInvalidURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RazorpayPaymentLink{
			ID:       "plink_test123",
			ShortURL: "https://example.com/fake-payment",
			Amount:   100000,
			Currency: "INR",
			Status:   "created",
		})
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}

	_, err := client.CreatePaymentLink(
		context.Background(),
		100000,
		"INR",
		"payment-123",
		"TrailNest booking",
	)
	if err == nil {
		t.Fatal("expected invalid URL to be rejected")
	}
}

func TestCreatePaymentLinkRejectsProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		http.Error(w, "sensitive provider details", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}

	_, err := client.CreatePaymentLink(
		context.Background(),
		100000,
		"INR",
		"payment-123",
		"TrailNest booking",
	)
	if err == nil {
		t.Fatal("expected provider error")
	}
	if got := err.Error(); got == "" || containsSensitiveDetails(got) {
		t.Fatalf("error exposed provider response: %q", got)
	}
}

func containsSensitiveDetails(message string) bool {
	const sensitive = "sensitive provider details"
	for i := 0; i+len(sensitive) <= len(message); i++ {
		if message[i:i+len(sensitive)] == sensitive {
			return true
		}
	}
	return false
}

func TestFetchPaymentLinkSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/v1/payment_links/plink_test123" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		username, password, ok := r.BasicAuth()
		if !ok || username != "test_key" || password != "test_secret" {
			t.Fatal("unexpected Basic Auth credentials")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"plink_test123",
			"short_url":"https://rzp.io/i/test123",
			"reference_id":"local-payment-123",
			"amount":100000,
			"amount_paid":100000,
			"currency":"INR",
			"status":"paid"
		}`))
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}

	link, err := client.FetchPaymentLink(
		t.Context(),
		"plink_test123",
	)
	if err != nil {
		t.Fatalf("FetchPaymentLink() error = %v", err)
	}

	if link.ID != "plink_test123" {
		t.Fatalf("unexpected link ID: %s", link.ID)
	}
	if link.ReferenceID != "local-payment-123" {
		t.Fatalf("unexpected reference ID: %s", link.ReferenceID)
	}
	if link.AmountPaid != 100000 || link.Status != "paid" {
		t.Fatalf("unexpected payment link response: %+v", link)
	}
}

func TestFetchPaymentLinkRejectsInvalidID(t *testing.T) {
	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       http.DefaultClient,
		apiBaseURL: "https://example.invalid",
	}

	_, err := client.FetchPaymentLink(t.Context(), "plink_test123/extra")
	if err == nil {
		t.Fatal("expected invalid Payment Link ID to be rejected")
	}
}

func TestFindPaymentLinkByReferenceIDSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/v1/payment_links" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("reference_id"); got != "local-payment-123" {
			t.Fatalf("unexpected reference_id: %s", got)
		}
		if got := r.URL.Query().Get("count"); got != "100" {
			t.Fatalf("unexpected count: %s", got)
		}

		username, password, ok := r.BasicAuth()
		if !ok || username != "test_key" || password != "test_secret" {
			t.Fatal("unexpected Basic Auth credentials")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"payment_links": [{
				"id": "plink_test123",
				"short_url": "https://rzp.io/i/test123",
				"reference_id": "local-payment-123",
				"amount": 100000,
				"amount_paid": 0,
				"currency": "INR",
				"status": "created"
			}]
		}`))
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}

	link, err := client.FindPaymentLinkByReferenceID(
		t.Context(),
		"local-payment-123",
	)
	if err != nil {
		t.Fatalf("FindPaymentLinkByReferenceID() error = %v", err)
	}
	if link.ID != "plink_test123" {
		t.Fatalf("unexpected link ID: %s", link.ID)
	}
	if link.ReferenceID != "local-payment-123" {
		t.Fatalf("unexpected reference ID: %s", link.ReferenceID)
	}
}

func TestFindPaymentLinkByReferenceIDNoMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"payment_links":[]}`))
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}

	_, err := client.FindPaymentLinkByReferenceID(
		t.Context(),
		"local-payment-123",
	)
	if err == nil {
		t.Fatal("expected an error when no matching link exists")
	}
}

func TestFindPaymentLinkByReferenceIDDuplicateMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"payment_links": [
				{"id":"plink_one","reference_id":"local-payment-123"},
				{"id":"plink_two","reference_id":"local-payment-123"}
			]
		}`))
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}

	_, err := client.FindPaymentLinkByReferenceID(
		t.Context(),
		"local-payment-123",
	)
	if err == nil {
		t.Fatal("expected duplicate matching links to be rejected")
	}
}

func TestFindPaymentLinkByReferenceIDProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("sensitive provider details"))
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}

	_, err := client.FindPaymentLinkByReferenceID(
		t.Context(),
		"local-payment-123",
	)
	if err == nil {
		t.Fatal("expected provider error")
	}
	if containsSensitiveDetails(err.Error()) {
		t.Fatal("error exposed sensitive provider details")
	}
}

func TestFindPaymentLinkByReferenceIDMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"payment_links":`))
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}

	_, err := client.FindPaymentLinkByReferenceID(
		t.Context(),
		"local-payment-123",
	)
	if err == nil {
		t.Fatal("expected malformed provider response to return an error")
	}

	if errors.Is(err, ErrRazorpayPaymentLinkNotFound) {
		t.Fatal("malformed response must not be treated as a missing link")
	}
}

func TestFetchPaymentCaptured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}

		if r.URL.Path != "/v1/payments/pay_test123" {
			t.Errorf("path = %q, want /v1/payments/pay_test123", r.URL.Path)
		}

		username, password, ok := r.BasicAuth()
		if !ok || username != "test_key" || password != "test_secret" {
			t.Error("request has incorrect Basic Auth credentials")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RazorpayPayment{
			ID:       "pay_test123",
			OrderID:  "order_test123",
			Amount:   50000,
			Currency: "INR",
			Status:   "captured",
		})
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}

	got, err := client.FetchPayment(t.Context(), "pay_test123")
	if err != nil {
		t.Fatalf("FetchPayment() error = %v", err)
	}

	if got.ID != "pay_test123" ||
		got.OrderID != "order_test123" ||
		got.Amount != 50000 ||
		got.Currency != "INR" ||
		got.Status != "captured" {
		t.Fatalf("unexpected payment returned: %+v", got)
	}
}
