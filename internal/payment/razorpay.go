package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrRazorpayNotConfigured = errors.New("Razorpay is not configured")

type RazorpayClient struct {
	keyID     string
	keySecret string
	http      *http.Client
}

type razorpayOrderRequest struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Receipt  string `json:"receipt"`
}

type RazorpayOrder struct {
	ID       string `json:"id"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
}

type RazorpayPayment struct {
	ID       string `json:"id"`
	OrderID  string `json:"order_id"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
}

func NewRazorpayClient(keyID, keySecret string) (*RazorpayClient, error) {
	keyID = strings.TrimSpace(keyID)
	keySecret = strings.TrimSpace(keySecret)

	if keyID == "" || keySecret == "" {
		return nil, ErrRazorpayNotConfigured
	}

	return &RazorpayClient{
		keyID:     keyID,
		keySecret: keySecret,
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}, nil
}

func (c *RazorpayClient) KeyID() string {
	return c.keyID
}

func (c *RazorpayClient) CreateOrder(
	ctx context.Context,
	amount int64,
	currency string,
	receipt string,
) (RazorpayOrder, error) {
	if amount <= 0 || strings.TrimSpace(currency) == "" ||
		strings.TrimSpace(receipt) == "" || len(receipt) > 40 {
		return RazorpayOrder{}, errors.New("invalid Razorpay order details")
	}

	payload, err := json.Marshal(razorpayOrderRequest{
		Amount:   amount,
		Currency: strings.ToUpper(strings.TrimSpace(currency)),
		Receipt:  receipt,
	})
	if err != nil {
		return RazorpayOrder{}, fmt.Errorf("encode Razorpay order: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://api.razorpay.com/v1/orders",
		bytes.NewReader(payload),
	)
	if err != nil {
		return RazorpayOrder{}, fmt.Errorf("build Razorpay request: %w", err)
	}

	req.SetBasicAuth(c.keyID, c.keySecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return RazorpayOrder{}, fmt.Errorf("call Razorpay Orders API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return RazorpayOrder{}, fmt.Errorf("read Razorpay response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Don't expose credentials or raw provider errors to API clients.
		return RazorpayOrder{}, fmt.Errorf(
			"Razorpay order creation failed with HTTP status %d",
			resp.StatusCode,
		)
	}

	var order RazorpayOrder
	if err := json.Unmarshal(body, &order); err != nil {
		return RazorpayOrder{}, fmt.Errorf("decode Razorpay order: %w", err)
	}

	if order.ID == "" ||
		order.Amount != amount ||
		!strings.EqualFold(order.Currency, currency) ||
		order.Status != "created" {
		return RazorpayOrder{}, errors.New("Razorpay returned an unexpected order")
	}

	return order, nil
}

func (c *RazorpayClient) FetchPayment(
	ctx context.Context,
	paymentID string,
) (RazorpayPayment, error) {
	paymentID = strings.TrimSpace(paymentID)
	if paymentID == "" || strings.Contains(paymentID, "/") {
		return RazorpayPayment{}, errors.New("invalid Razorpay payment ID")
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://api.razorpay.com/v1/payments/"+paymentID,
		nil,
	)
	if err != nil {
		return RazorpayPayment{}, fmt.Errorf("build Razorpay payment request: %w", err)
	}

	req.SetBasicAuth(c.keyID, c.keySecret)

	resp, err := c.http.Do(req)
	if err != nil {
		return RazorpayPayment{}, fmt.Errorf("fetch Razorpay payment: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return RazorpayPayment{}, fmt.Errorf("read Razorpay payment response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RazorpayPayment{}, fmt.Errorf(
			"Razorpay payment lookup failed with HTTP status %d",
			resp.StatusCode,
		)
	}

	var result RazorpayPayment
	if err := json.Unmarshal(body, &result); err != nil {
		return RazorpayPayment{}, fmt.Errorf("decode Razorpay payment: %w", err)
	}

	if result.ID != paymentID {
		return RazorpayPayment{}, errors.New("Razorpay payment ID mismatch")
	}

	return result, nil
}
