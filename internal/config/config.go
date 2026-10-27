package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port                  string
	DatabaseURL           string
	GinMode               string
	JWTSecret             string
	ResendAPIKey          string
	EmailFrom             string
	AppBaseURL            string
	RazorpayKeyID         string
	RazorpayKeySecret     string
	RazorpayWebhookSecret string
	ImageStorageDirectory string
	ImageStoragePublicURL string
}

func Load() (Config, error) {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}

	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return Config{}, fmt.Errorf(
			"PORT must be a valid port between 1 and 65535",
		)
	}

	ginMode := strings.TrimSpace(os.Getenv("GIN_MODE"))
	if ginMode == "" {
		ginMode = "debug"
	}

	if ginMode != "debug" && ginMode != "release" {
		return Config{}, fmt.Errorf(
			"GIN_MODE must be debug or release",
		)
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if len(jwtSecret) < 32 {
		return Config{}, fmt.Errorf(
			"JWT_SECRET must contain at least 32 bytes",
		)
	}

	resendAPIKey := strings.TrimSpace(os.Getenv("RESEND_API_KEY"))
	emailFrom := strings.TrimSpace(os.Getenv("EMAIL_FROM"))
	appBaseURL := strings.TrimRight(
		strings.TrimSpace(os.Getenv("APP_BASE_URL")),
		"/",
	)
	razorpayKeyID := strings.TrimSpace(os.Getenv("RAZORPAY_KEY_ID"))
	razorpayKeySecret := strings.TrimSpace(os.Getenv("RAZORPAY_KEY_SECRET"))
	razorpayWebhookSecret := strings.TrimSpace(os.Getenv("RAZORPAY_WEBHOOK_SECRET"))

	imageStorageDirectory := strings.TrimSpace(
		os.Getenv("IMAGE_STORAGE_DIRECTORY"),
	)
	if imageStorageDirectory == "" {
		imageStorageDirectory = "uploads/activities"
	}

	imageStoragePublicURL := strings.TrimRight(
		strings.TrimSpace(os.Getenv("IMAGE_STORAGE_PUBLIC_URL")),
		"/",
	)
	if imageStoragePublicURL == "" {
		imageStoragePublicURL = "/uploads/activities"
	}

	return Config{
		Port:                  port,
		DatabaseURL:           strings.TrimSpace(os.Getenv("DATABASE_URL")),
		GinMode:               ginMode,
		JWTSecret:             jwtSecret,
		ResendAPIKey:          resendAPIKey,
		EmailFrom:             emailFrom,
		AppBaseURL:            appBaseURL,
		RazorpayKeyID:         razorpayKeyID,
		RazorpayKeySecret:     razorpayKeySecret,
		RazorpayWebhookSecret: razorpayWebhookSecret,
		ImageStorageDirectory: imageStorageDirectory,
		ImageStoragePublicURL: imageStoragePublicURL,
	}, nil
}
