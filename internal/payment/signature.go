package payment

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
)

var ErrInvalidSignature = errors.New("invalid Razorpay signature")

// VerifyCheckoutSignature verifies the signature returned by Razorpay Checkout.
// The order ID must come from our own saved payment record.
func VerifyCheckoutSignature(
	orderID string,
	paymentID string,
	signature string,
	keySecret string,
) error {
	orderID = strings.TrimSpace(orderID)
	paymentID = strings.TrimSpace(paymentID)
	signature = strings.TrimSpace(signature)

	if orderID == "" || paymentID == "" ||
		signature == "" || keySecret == "" {
		return ErrInvalidSignature
	}

	mac := hmac.New(sha256.New, []byte(keySecret))
	_, _ = mac.Write([]byte(orderID + "|" + paymentID))
	expected := mac.Sum(nil)

	received, err := hex.DecodeString(signature)
	if err != nil || len(received) != sha256.Size {
		return ErrInvalidSignature
	}

	if subtle.ConstantTimeCompare(expected, received) != 1 {
		return ErrInvalidSignature
	}

	return nil
}

// VerifyWebhookSignature verifies the X-Razorpay-Signature header.
// rawBody must be the exact bytes received from the HTTP request.
func VerifyWebhookSignature(
	rawBody []byte,
	signature string,
	webhookSecret string,
) error {
	signature = strings.TrimSpace(signature)

	if len(rawBody) == 0 || signature == "" || webhookSecret == "" {
		return ErrInvalidSignature
	}

	received, err := hex.DecodeString(signature)
	if err != nil || len(received) != sha256.Size {
		return ErrInvalidSignature
	}

	mac := hmac.New(sha256.New, []byte(webhookSecret))
	_, _ = mac.Write(rawBody)
	expected := mac.Sum(nil)

	if subtle.ConstantTimeCompare(expected, received) != 1 {
		return ErrInvalidSignature
	}

	return nil
}
