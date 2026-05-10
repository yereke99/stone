package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	AppPort   string
	Env       string
	Meta      MetaConfig
	Portfolio PortfolioConfig
}

type MetaConfig struct {
	APIBaseURL         string
	AccessToken        string
	PhoneNumberID      string
	BusinessAccountID  string
	WebhookVerifyToken string
	AppSecret          string
	RequestTimeout     time.Duration
}

type PortfolioConfig struct {
	TestURL     string
	BasicURL    string
	StandardURL string
}

func Load() (*Config, error) {
	var missing []string
	required := func(key string) string {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			missing = append(missing, key)
		}
		return value
	}

	timeout := 10 * time.Second
	if raw := strings.TrimSpace(os.Getenv("META_REQUEST_TIMEOUT")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("parse META_REQUEST_TIMEOUT: %w", err)
		}
		timeout = parsed
	}

	cfg := &Config{
		AppPort: required("APP_PORT"),
		Env:     required("ENV"),
		Meta: MetaConfig{
			APIBaseURL:         strings.TrimRight(required("META_API_BASE_URL"), "/"),
			AccessToken:        strings.TrimSpace(os.Getenv("META_ACCESS_TOKEN")),
			PhoneNumberID:      strings.TrimSpace(os.Getenv("META_PHONE_NUMBER_ID")),
			BusinessAccountID:  strings.TrimSpace(os.Getenv("META_BUSINESS_ACCOUNT_ID")),
			WebhookVerifyToken: required("META_WEBHOOK_VERIFY_TOKEN"),
			AppSecret:          strings.TrimSpace(os.Getenv("META_APP_SECRET")),
			RequestTimeout:     timeout,
		},
		Portfolio: PortfolioConfig{
			TestURL:     strings.TrimSpace(os.Getenv("PORTFOLIO_TEST_URL")),
			BasicURL:    strings.TrimSpace(os.Getenv("PORTFOLIO_BASIC_URL")),
			StandardURL: strings.TrimSpace(os.Getenv("PORTFOLIO_STANDARD_URL")),
		},
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

func (c Config) HTTPAddress() string {
	if strings.HasPrefix(c.AppPort, ":") {
		return c.AppPort
	}
	return ":" + c.AppPort
}
