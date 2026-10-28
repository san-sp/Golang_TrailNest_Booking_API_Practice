package config

import (
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
