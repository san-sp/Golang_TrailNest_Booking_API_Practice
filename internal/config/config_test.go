package config

import (
	"strings"
	"testing"
)

func setRequiredConfigEnv(t *testing.T) {
	t.Helper()

	t.Setenv("PORT", "8080")
	t.Setenv("GIN_MODE", "release")
	t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-characters-long")
}

func TestLoadTrustedProxies(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{
			name:  "empty value",
			value: "",
			want:  nil,
		},
		{
			name:  "single IP",
			value: "192.0.2.10",
			want:  []string{"192.0.2.10"},
		},
		{
			name:  "multiple IPs and CIDRs with whitespace",
			value: " 192.0.2.10, 198.51.100.0/24 , 203.0.113.5 ",
			want:  []string{"192.0.2.10", "198.51.100.0/24", "203.0.113.5"},
		},
		{
			name:  "empty entries are ignored",
			value: "192.0.2.10,,  ,198.51.100.0/24,",
			want:  []string{"192.0.2.10", "198.51.100.0/24"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredConfigEnv(t)
			t.Setenv("TRUSTED_PROXIES", tt.value)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			if len(cfg.TrustedProxies) != len(tt.want) {
				t.Fatalf(
					"TrustedProxies = %#v, want %#v",
					cfg.TrustedProxies,
					tt.want,
				)
			}

			for i := range tt.want {
				if cfg.TrustedProxies[i] != tt.want[i] {
					t.Errorf(
						"TrustedProxies[%d] = %q, want %q",
						i,
						cfg.TrustedProxies[i],
						tt.want[i],
					)
				}
			}
		})
	}
}

func TestLoadAllowedOrigins(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{
			name:  "defaults to localhost",
			value: "",
			want:  []string{"http://localhost:8090"},
		},
		{
			name:  "single origin",
			value: "https://sansp.in",
			want:  []string{"https://sansp.in"},
		},
		{
			name:  "multiple origins with whitespace",
			value: " https://sansp.in, https://www.sansp.in ",
			want: []string{
				"https://sansp.in",
				"https://www.sansp.in",
			},
		},
		{
			name:  "empty entries are ignored",
			value: "https://sansp.in,, ,https://www.sansp.in,",
			want: []string{
				"https://sansp.in",
				"https://www.sansp.in",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredConfigEnv(t)
			t.Setenv("CORS_ALLOWED_ORIGINS", tt.value)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			if len(cfg.AllowedOrigins) != len(tt.want) {
				t.Fatalf(
					"AllowedOrigins = %#v, want %#v",
					cfg.AllowedOrigins,
					tt.want,
				)
			}

			for i := range tt.want {
				if cfg.AllowedOrigins[i] != tt.want[i] {
					t.Errorf(
						"AllowedOrigins[%d] = %q, want %q",
						i,
						cfg.AllowedOrigins[i],
						tt.want[i],
					)
				}
			}
		})
	}
}

func TestLoadImageStorageConfig(t *testing.T) {
	tests := []struct {
		name       string
		driver     string
		bucket     string
		region     string
		publicURL  string
		wantDriver string
		wantError  string
	}{
		{
			name:       "defaults to local storage",
			wantDriver: "local",
		},
		{
			name:       "accepts local storage",
			driver:     "local",
			wantDriver: "local",
		},
		{
			name:       "accepts valid S3 configuration",
			driver:     "s3",
			bucket:     "trailnest-images",
			region:     "ap-south-1",
			publicURL:  "https://cdn.example.com/activities",
			wantDriver: "s3",
		},
		{
			name:      "rejects unsupported driver",
			driver:    "azure",
			wantError: "IMAGE_STORAGE_DRIVER must be local or s3",
		},
		{
			name:      "requires S3 bucket",
			driver:    "s3",
			region:    "ap-south-1",
			publicURL: "https://cdn.example.com",
			wantError: "S3_BUCKET is required",
		},
		{
			name:      "requires AWS region",
			driver:    "s3",
			bucket:    "trailnest-images",
			publicURL: "https://cdn.example.com",
			wantError: "AWS_REGION is required",
		},
		{
			name:      "requires public URL",
			driver:    "s3",
			bucket:    "trailnest-images",
			region:    "ap-south-1",
			wantError: "IMAGE_STORAGE_PUBLIC_URL is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredConfigEnv(t)

			t.Setenv("IMAGE_STORAGE_DRIVER", tt.driver)
			t.Setenv("S3_BUCKET", tt.bucket)
			t.Setenv("AWS_REGION", tt.region)
			t.Setenv("IMAGE_STORAGE_PUBLIC_URL", tt.publicURL)

			cfg, err := Load()

			if tt.wantError != "" {
				if err == nil {
					t.Fatal("Load() expected an error")
				}

				if !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf(
						"Load() error = %q, want it to contain %q",
						err.Error(),
						tt.wantError,
					)
				}

				return
			}

			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			if cfg.ImageStorageDriver != tt.wantDriver {
				t.Errorf(
					"ImageStorageDriver = %q, want %q",
					cfg.ImageStorageDriver,
					tt.wantDriver,
				)
			}

			if cfg.S3Bucket != tt.bucket {
				t.Errorf("S3Bucket = %q, want %q", cfg.S3Bucket, tt.bucket)
			}

			if cfg.AWSRegion != tt.region {
				t.Errorf("AWSRegion = %q, want %q", cfg.AWSRegion, tt.region)
			}
		})
	}
}
