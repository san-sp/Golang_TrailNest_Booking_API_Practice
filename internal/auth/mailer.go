package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

const resendEmailsURL = "https://api.resend.com/emails"

type EmailMailer struct {
	apiKey  string
	from    string
	baseURL string
	client  *http.Client
}

func NewEmailMailer(apiKey, from, baseURL string) (*EmailMailer, error) {
	apiKey = strings.TrimSpace(apiKey)
	from = strings.TrimSpace(from)
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")

	if apiKey == "" {
		return nil, fmt.Errorf("RESEND_API_KEY is required")
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return nil, fmt.Errorf("EMAIL_FROM must be a valid email address: %w", err)
	}
	if !strings.HasPrefix(baseURL, "http://") &&
		!strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("APP_BASE_URL must start with http:// or https://")
	}

	return &EmailMailer{
		apiKey:  apiKey,
		from:    from,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}, nil
}

type resendEmailRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
	Text    string   `json:"text"`
}

func (m *EmailMailer) SendVerificationEmail(
	ctx context.Context,
	to string,
	token string,
) error {
	verificationURL := m.baseURL +
		"/api/v1/auth/verify-email?token=" + token

	safeURL := html.EscapeString(verificationURL)

	message := resendEmailRequest{
		From:    m.from,
		To:      []string{to},
		Subject: "Verify your TrailNest email address",
		HTML: `<div style="font-family:Arial,sans-serif;max-width:600px;margin:auto;color:#222">
			<h1>Welcome to TrailNest!</h1>
			<p>Thanks for creating your account. Please verify your email address to get started.</p>
			<p><a href="` + safeURL + `" style="display:inline-block;padding:12px 20px;background:#176b50;color:#fff;text-decoration:none;border-radius:6px">Verify email address</a></p>
			<p>This link expires in 24 hours and can only be used once.</p>
			<p>If you didn't create a TrailNest account, you can ignore this email.</p>
			<p>The TrailNest Team</p>
		</div>`,
		Text: "Welcome to TrailNest!\n\n" +
			"Please verify your email address using this link:\n" +
			verificationURL +
			"\n\nThis link expires in 24 hours and can only be used once.\n" +
			"If you didn't create an account, you can ignore this email.",
	}

	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode verification email: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		resendEmailsURL,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create verification email request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TrailNest/1.0")

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("send verification email: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("email provider returned HTTP %d", resp.StatusCode)
	}

	return nil
}
