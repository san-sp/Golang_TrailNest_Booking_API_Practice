package payment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func testHMAC(message, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyCheckoutSignature(t *testing.T) {
	const secret = "test_key_secret"
	const orderID = "order_test123"
	const paymentID = "pay_test123"

	signature := testHMAC(orderID+"|"+paymentID, secret)

	if err := VerifyCheckoutSignature(
		orderID, paymentID, signature, secret,
	); err != nil {
		t.Fatalf("expected valid signature, got: %v", err)
	}

	if err := VerifyCheckoutSignature(
		orderID, "pay_wrong", signature, secret,
	); err == nil {
		t.Fatal("expected mismatched payment ID to be rejected")
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	const secret = "test_webhook_secret"
	body := []byte(`{"event":"payment.captured"}`)
	signature := testHMAC(string(body), secret)

	if err := VerifyWebhookSignature(
		body, signature, secret,
	); err != nil {
		t.Fatalf("expected valid signature, got: %v", err)
	}

	modifiedBody := []byte(`{"event": "payment.captured"}`)
	if err := VerifyWebhookSignature(
		modifiedBody, signature, secret,
	); err == nil {
		t.Fatal("expected modified raw body to be rejected")
	}
}
