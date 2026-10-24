package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrRazorpayNotConfigured = errors.New("Razorpay is not configured")

var ErrRazorpayPaymentLinkNotFound = errors.New(
	"Razorpay payment link not found",
)

type RazorpayClient struct {
	keyID      string
	keySecret  string
	http       *http.Client
	apiBaseURL string
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

type razorpayPaymentLinkRequest struct {
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	ReferenceID    string `json:"reference_id"`
	Description    string `json:"description"`
	AcceptPartial  bool   `json:"accept_partial"`
	ReminderEnable bool   `json:"reminder_enable"`
}

type RazorpayPaymentLink struct {
	ID          string `json:"id"`
	ShortURL    string `json:"short_url"`
	ReferenceID string `json:"reference_id"`
	Amount      int64  `json:"amount"`
	AmountPaid  int64  `json:"amount_paid"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
}

type razorpayPaymentLinksResponse struct {
	PaymentLinks []RazorpayPaymentLink `json:"payment_links"`
}

type RazorpayPayment struct {
	ID            string `json:"id"`
	OrderID       string `json:"order_id"`
	PaymentLinkID string `json:"payment_link_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	Status        string `json:"status"`
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
		apiBaseURL: "https://api.razorpay.com",
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
		strings.TrimRight(c.apiBaseURL, "/")+"/v1/orders",
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

func (c *RazorpayClient) CreatePaymentLink(
	ctx context.Context,
	amount int64,
	currency string,
	referenceID string,
	description string,
) (RazorpayPaymentLink, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	referenceID = strings.TrimSpace(referenceID)
	description = strings.TrimSpace(description)

	if amount <= 0 ||
		len(currency) != 3 ||
		referenceID == "" ||
		len(referenceID) > 40 ||
		description == "" ||
		len(description) > 2048 {
		return RazorpayPaymentLink{}, errors.New(
			"invalid Razorpay payment link details",
		)
	}

	payload, err := json.Marshal(razorpayPaymentLinkRequest{
		Amount:         amount,
		Currency:       currency,
		ReferenceID:    referenceID,
		Description:    description,
		AcceptPartial:  false,
		ReminderEnable: false,
	})
	if err != nil {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"encode Razorpay payment link: %w", err,
		)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(c.apiBaseURL, "/")+"/v1/payment_links",
		bytes.NewReader(payload),
	)
	if err != nil {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"build Razorpay payment link request: %w", err,
		)
	}

	req.SetBasicAuth(c.keyID, c.keySecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"call Razorpay Payment Links API: %w", err,
		)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"read Razorpay payment link response: %w", err,
		)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Never return the provider's raw error body to API clients.
		return RazorpayPaymentLink{}, fmt.Errorf(
			"Razorpay payment link creation failed with HTTP status %d",
			resp.StatusCode,
		)
	}

	var link RazorpayPaymentLink
	if err := json.Unmarshal(body, &link); err != nil {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"decode Razorpay payment link: %w", err,
		)
	}

	// Only accept a valid hosted HTTPS URL from Razorpay's short-link domain.
	parsedURL, err := url.Parse(link.ShortURL)
	if err != nil ||
		parsedURL.Scheme != "https" ||
		!strings.EqualFold(parsedURL.Hostname(), "rzp.io") ||
		parsedURL.User != nil {
		return RazorpayPaymentLink{}, errors.New(
			"Razorpay returned an invalid payment link URL",
		)
	}

	if !strings.HasPrefix(link.ID, "plink_") ||
		link.Amount != amount ||
		!strings.EqualFold(link.Currency, currency) ||
		link.Status != "created" {
		return RazorpayPaymentLink{}, errors.New(
			"Razorpay returned an unexpected payment link",
		)
	}

	return link, nil
}

func (c *RazorpayClient) FetchPaymentLink(
	ctx context.Context,
	linkID string,
) (RazorpayPaymentLink, error) {
	linkID = strings.TrimSpace(linkID)

	if !strings.HasPrefix(linkID, "plink_") ||
		strings.Contains(linkID, "/") {
		return RazorpayPaymentLink{}, errors.New(
			"invalid Razorpay payment link ID",
		)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		strings.TrimRight(c.apiBaseURL, "/")+"/v1/payment_links/"+linkID,
		nil,
	)
	if err != nil {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"build Razorpay payment link request: %w", err,
		)
	}

	req.SetBasicAuth(c.keyID, c.keySecret)

	resp, err := c.http.Do(req)
	if err != nil {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"fetch Razorpay payment link: %w", err,
		)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"read Razorpay payment link response: %w", err,
		)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"Razorpay payment link lookup failed with HTTP status %d",
			resp.StatusCode,
		)
	}

	var link RazorpayPaymentLink
	if err := json.Unmarshal(body, &link); err != nil {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"decode Razorpay payment link: %w", err,
		)
	}

	if link.ID != linkID {
		return RazorpayPaymentLink{}, errors.New(
			"Razorpay payment link ID mismatch",
		)
	}

	return link, nil
}

func (c *RazorpayClient) FindPaymentLinkByReferenceID(
	ctx context.Context,
	referenceID string,
) (RazorpayPaymentLink, error) {
	referenceID = strings.TrimSpace(referenceID)
	if referenceID == "" || len(referenceID) > 40 {
		return RazorpayPaymentLink{}, errors.New(
			"invalid Razorpay payment link reference ID",
		)
	}

	endpoint := strings.TrimRight(c.apiBaseURL, "/") +
		"/v1/payment_links"

	query := url.Values{}
	query.Set("reference_id", referenceID)
	query.Set("count", "100")

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint+"?"+query.Encode(),
		nil,
	)
	if err != nil {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"build Razorpay payment link search request: %w", err,
		)
	}

	req.SetBasicAuth(c.keyID, c.keySecret)

	resp, err := c.http.Do(req)
	if err != nil {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"search Razorpay payment links: %w", err,
		)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"read Razorpay payment link search response: %w", err,
		)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"Razorpay payment link search failed with HTTP status %d",
			resp.StatusCode,
		)
	}

	var result razorpayPaymentLinksResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return RazorpayPaymentLink{}, fmt.Errorf(
			"decode Razorpay payment link search response: %w", err,
		)
	}

	var match *RazorpayPaymentLink
	for i := range result.PaymentLinks {
		link := &result.PaymentLinks[i]

		if link.ReferenceID != referenceID {
			continue
		}

		if match != nil {
			return RazorpayPaymentLink{}, errors.New(
				"multiple Razorpay payment links found for reference ID",
			)
		}

		match = link
	}

	if match == nil {
		return RazorpayPaymentLink{}, ErrRazorpayPaymentLinkNotFound
	}

	return *match, nil
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
		strings.TrimRight(c.apiBaseURL, "/")+"/v1/payments/"+paymentID,
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
