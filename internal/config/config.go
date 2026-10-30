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
	ImageStorageDriver    string
	S3Bucket              string
	AWSRegion             string
	TrustedProxies        []string
	AllowedOrigins        []string
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

	var trustedProxies []string

	for _, proxy := range strings.Split(
		os.Getenv("TRUSTED_PROXIES"),
		",",
	) {
		proxy = strings.TrimSpace(proxy)
		if proxy != "" {
			trustedProxies = append(trustedProxies, proxy)
		}
	}

	var allowedOrigins []string

	for _, origin := range strings.Split(
		os.Getenv("CORS_ALLOWED_ORIGINS"),
		",",
	) {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowedOrigins = append(allowedOrigins, origin)
		}
	}

	if len(allowedOrigins) == 0 {
		allowedOrigins = []string{"http://localhost:8090"}
	}

	imageStorageDriver := strings.TrimSpace(
		os.Getenv("IMAGE_STORAGE_DRIVER"),
	)
	if imageStorageDriver == "" {
		imageStorageDriver = "local"
	}

	if imageStorageDriver != "local" && imageStorageDriver != "s3" {
		return Config{}, fmt.Errorf(
			"IMAGE_STORAGE_DRIVER must be local or s3",
		)
	}

	s3Bucket := strings.TrimSpace(os.Getenv("S3_BUCKET"))
	awsRegion := strings.TrimSpace(os.Getenv("AWS_REGION"))

	if imageStorageDriver == "s3" {
		if s3Bucket == "" {
			return Config{}, fmt.Errorf(
				"S3_BUCKET is required when IMAGE_STORAGE_DRIVER=s3",
			)
		}

		if awsRegion == "" {
			return Config{}, fmt.Errorf(
				"AWS_REGION is required when IMAGE_STORAGE_DRIVER=s3",
			)
		}

		if imageStoragePublicURL == "" {
			return Config{}, fmt.Errorf(
				"IMAGE_STORAGE_PUBLIC_URL is required when IMAGE_STORAGE_DRIVER=s3",
			)
		}
	}

	// Apply the local-storage default only after S3 validation.
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
		ImageStorageDriver:    imageStorageDriver,
		S3Bucket:              s3Bucket,
		AWSRegion:             awsRegion,
		TrustedProxies:        trustedProxies,
		AllowedOrigins:        allowedOrigins,
	}, nil
}
