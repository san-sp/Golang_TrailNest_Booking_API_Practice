package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestLoginJSONBodyAcceptsSingleObject(t *testing.T) {
	body := []byte(`{"email":"test@example.com","password":"example-password"}`)

	decoder := json.NewDecoder(
		http.MaxBytesReader(nil, io.NopCloser(bytes.NewReader(body)), maxLoginBody),
	)
	decoder.DisallowUnknownFields()

	var input loginRequest
	if err := decoder.Decode(&input); err != nil {
		t.Fatalf("decode valid login JSON: %v", err)
	}

	if input.Email != "test@example.com" {
		t.Fatalf("unexpected email: %q", input.Email)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF after one JSON object, got %v", err)
	}
}

func TestResendVerificationJSONBodyAcceptsSingleObject(t *testing.T) {
	body := []byte(`{"email":"test@example.com"}`)

	decoder := json.NewDecoder(
		http.MaxBytesReader(nil, io.NopCloser(bytes.NewReader(body)), maxRegistrationBody),
	)
	decoder.DisallowUnknownFields()

	var input resendVerificationRequest
	if err := decoder.Decode(&input); err != nil {
		t.Fatalf("decode valid resend-verification JSON: %v", err)
	}

	if input.Email != "test@example.com" {
		t.Fatalf("unexpected email: %q", input.Email)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF after one JSON object, got %v", err)
	}
}
