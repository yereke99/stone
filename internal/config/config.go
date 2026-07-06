package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultOpenAIModel       = "gpt-5.5"
	defaultOpenAITemperature = 0.3
)

type Config struct {
	GreenAPI                GreenAPIConfig
	OpenAI                  OpenAIConfig
	Database                DatabaseConfig
	PortfolioVideoDir       string
	ReceiveTimeoutSeconds   int
	HTTPClientTimeout       time.Duration
	BotReplyLanguageMode    string
	BotAutoReplyEnabled     bool
	BotMaxMessageAge        time.Duration
	MaxOpenAIOutputTokens   int
	AnalyzerMaxOutputTokens int
	AdminChatIDs            []string
	AppEnv                  string
	Env                     string
	AppPort                 string
	Meta                    MetaConfig
	Portfolio               PortfolioConfig
	HistoryGuard            HistoryGuardConfig
	NewLeadAutoPackages     NewLeadAutoPackagesConfig
}

type GreenAPIConfig struct {
	APIURL      string
	MediaAPIURL string
	IDInstance  string
	APIToken    string
}

type OpenAIConfig struct {
	APIKey      string
	Model       string
	Temperature float64
}

type DatabaseConfig struct {
	Path string
}

type HistoryGuardConfig struct {
	Enabled              bool
	LookbackCount        int
	Timeout              time.Duration
	FailClosed           bool
	AIEnabled            bool
	AIMessageLimit       int
	AIMaxCharsPerMessage int
	AIMaxTotalChars      int
}

type NewLeadAutoPackagesConfig struct {
	Enabled bool
	After   time.Duration
}

// MetaConfig is kept so the previous webhook packages continue to compile.
// The GreenAPI polling bot does not use it.
type MetaConfig struct {
	APIBaseURL         string
	AccessToken        string
	PhoneNumberID      string
	BusinessAccountID  string
	WebhookVerifyToken string
	AppSecret          string
	RequestTimeout     time.Duration
}

// PortfolioConfig stores optional public portfolio links by production format.
type PortfolioConfig struct {
	TestURL     string
	BasicURL    string
	StandardURL string
}

func Load() (*Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return nil, err
	}

	var missing []string

	required := func(key string) string {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			missing = append(missing, key)
		}
		return value
	}

	receiveTimeout, err := requiredPositiveInt("RECEIVE_TIMEOUT_SECONDS", required)
	if err != nil {
		return nil, err
	}

	httpTimeoutSeconds, err := requiredPositiveInt("HTTP_CLIENT_TIMEOUT_SECONDS", required)
	if err != nil {
		return nil, err
	}

	maxOutputTokens, err := requiredPositiveInt("MAX_OPENAI_OUTPUT_TOKENS", required)
	if err != nil {
		return nil, err
	}
	analyzerMaxOutputTokens, err := optionalPositiveInt("ANALYZER_MAX_OUTPUT_TOKENS", 1500)
	if err != nil {
		return nil, err
	}

	openAITemperature, err := optionalFloat("OPENAI_TEMPERATURE", defaultOpenAITemperature)
	if err != nil {
		return nil, err
	}

	replyLanguageMode := strings.ToLower(required("BOT_REPLY_LANGUAGE_MODE"))
	if replyLanguageMode != "" && replyLanguageMode != "auto" && replyLanguageMode != "ru" && replyLanguageMode != "kk" && replyLanguageMode != "en" {
		return nil, fmt.Errorf("BOT_REPLY_LANGUAGE_MODE must be one of: auto, ru, kk, en")
	}

	autoReplyEnabled, err := optionalBool("BOT_AUTO_REPLY_ENABLED", false)
	if err != nil {
		return nil, err
	}

	maxMessageAgeSeconds, err := optionalPositiveInt("BOT_MAX_MESSAGE_AGE_SECONDS", 120)
	if err != nil {
		return nil, err
	}
	historyGuardEnabled, err := optionalBool("HISTORY_GUARD_ENABLED", true)
	if err != nil {
		return nil, err
	}
	historyGuardLookbackCount, err := optionalPositiveInt("HISTORY_GUARD_LOOKBACK_COUNT", 10)
	if err != nil {
		return nil, err
	}
	historyGuardTimeoutSeconds, err := optionalPositiveInt("HISTORY_GUARD_TIMEOUT_SECONDS", 8)
	if err != nil {
		return nil, err
	}
	historyGuardFailClosed, err := optionalBool("HISTORY_GUARD_FAIL_CLOSED", true)
	if err != nil {
		return nil, err
	}
	historyGuardAIEnabled, err := optionalBool("HISTORY_GUARD_AI_ENABLED", false)
	if err != nil {
		return nil, err
	}
	historyGuardAIMessageLimit, err := optionalPositiveInt("HISTORY_GUARD_AI_MESSAGE_LIMIT", 3)
	if err != nil {
		return nil, err
	}
	historyGuardAIMaxCharsPerMessage, err := optionalPositiveInt("HISTORY_GUARD_AI_MAX_CHARS_PER_MESSAGE", 400)
	if err != nil {
		return nil, err
	}
	historyGuardAIMaxTotalChars, err := optionalPositiveInt("HISTORY_GUARD_AI_MAX_TOTAL_CHARS", 1200)
	if err != nil {
		return nil, err
	}
	autoPackagesAfterMinutes, err := optionalPositiveInt("NEW_LEAD_AUTO_PACKAGES_AFTER_MINUTES", 15)
	if err != nil {
		return nil, err
	}
	autoPackagesEnabled, err := optionalBool("NEW_LEAD_AUTO_PACKAGES_ENABLED", true)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		GreenAPI: GreenAPIConfig{
			APIURL:      strings.TrimRight(required("GREEN_API_URL"), "/"),
			MediaAPIURL: strings.TrimRight(required("GREEN_MEDIA_API_URL"), "/"),
			IDInstance:  required("GREEN_ID_INSTANCE"),
			APIToken:    required("GREEN_API_TOKEN"),
		},
		OpenAI: OpenAIConfig{
			APIKey:      required("OPENAI_API_KEY"),
			Model:       envOrDefault("OPENAI_MODEL", defaultOpenAIModel),
			Temperature: openAITemperature,
		},
		Database: DatabaseConfig{
			Path: envOrDefault("DATABASE_PATH", "./data/stone.sqlite3"),
		},
		PortfolioVideoDir:       required("PORTFOLIO_VIDEO_DIR"),
		ReceiveTimeoutSeconds:   receiveTimeout,
		HTTPClientTimeout:       time.Duration(httpTimeoutSeconds) * time.Second,
		BotReplyLanguageMode:    replyLanguageMode,
		BotAutoReplyEnabled:     autoReplyEnabled,
		BotMaxMessageAge:        time.Duration(maxMessageAgeSeconds) * time.Second,
		MaxOpenAIOutputTokens:   maxOutputTokens,
		AnalyzerMaxOutputTokens: analyzerMaxOutputTokens,
		AdminChatIDs:            append(splitCSV(os.Getenv("OWNER_WA_CHAT_ID")), splitCSV(os.Getenv("ADMIN_CHAT_IDS"))...),
		AppEnv:                  required("APP_ENV"),
		HistoryGuard: HistoryGuardConfig{
			Enabled:              historyGuardEnabled,
			LookbackCount:        historyGuardLookbackCount,
			Timeout:              time.Duration(historyGuardTimeoutSeconds) * time.Second,
			FailClosed:           historyGuardFailClosed,
			AIEnabled:            historyGuardAIEnabled,
			AIMessageLimit:       historyGuardAIMessageLimit,
			AIMaxCharsPerMessage: historyGuardAIMaxCharsPerMessage,
			AIMaxTotalChars:      historyGuardAIMaxTotalChars,
		},
		NewLeadAutoPackages: NewLeadAutoPackagesConfig{
			Enabled: autoPackagesEnabled,
			After:   time.Duration(autoPackagesAfterMinutes) * time.Minute,
		},
	}
	cfg.Env = cfg.AppEnv

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	cfg.loadLegacyOptional()
	return cfg, nil
}

func envOrDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set env %s from %s: %w", key, path, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	return nil
}

func requiredPositiveInt(key string, required func(string) string) (int, error) {
	raw := required(key)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
	}
	return value, nil
}

func optionalFloat(key string, fallback float64) (float64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if value < 0 || value > 2 {
		return 0, fmt.Errorf("%s must be between 0 and 2", key)
	}
	return value, nil
}

func optionalBool(key string, fallback bool) (bool, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback, nil
	}
	switch raw {
	case "1", "t", "true", "yes", "y", "on":
		return true, nil
	case "0", "f", "false", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a boolean value", key)
	}
}

func optionalPositiveInt(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
	}
	return value, nil
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func (c *Config) loadLegacyOptional() {
	c.AppPort = strings.TrimSpace(os.Getenv("APP_PORT"))

	metaTimeout := 10 * time.Second
	if raw := strings.TrimSpace(os.Getenv("META_REQUEST_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			metaTimeout = parsed
		}
	}

	c.Meta = MetaConfig{
		APIBaseURL:         strings.TrimRight(strings.TrimSpace(os.Getenv("META_API_BASE_URL")), "/"),
		AccessToken:        strings.TrimSpace(os.Getenv("META_ACCESS_TOKEN")),
		PhoneNumberID:      strings.TrimSpace(os.Getenv("META_PHONE_NUMBER_ID")),
		BusinessAccountID:  strings.TrimSpace(os.Getenv("META_BUSINESS_ACCOUNT_ID")),
		WebhookVerifyToken: strings.TrimSpace(os.Getenv("META_WEBHOOK_VERIFY_TOKEN")),
		AppSecret:          strings.TrimSpace(os.Getenv("META_APP_SECRET")),
		RequestTimeout:     metaTimeout,
	}
	c.Portfolio = PortfolioConfig{
		TestURL:     strings.TrimSpace(os.Getenv("PORTFOLIO_TEST_URL")),
		BasicURL:    strings.TrimSpace(os.Getenv("PORTFOLIO_BASIC_URL")),
		StandardURL: strings.TrimSpace(os.Getenv("PORTFOLIO_STANDARD_URL")),
	}
}

func (c Config) HTTPAddress() string {
	if c.AppPort == "" {
		return ":8080"
	}
	if strings.HasPrefix(c.AppPort, ":") {
		return c.AppPort
	}
	return ":" + c.AppPort
}
