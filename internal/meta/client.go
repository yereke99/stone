package meta

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yereke99/stone/internal/config"
	"github.com/yereke99/stone/internal/whatsapp"
	"go.uber.org/zap"
)

var ErrNotConfigured = errors.New("meta whatsapp client is not fully configured")
var ErrBlockedWhatsAppGroupChat = errors.New("outgoing WhatsApp message blocked because recipient is a group chat")

type Client struct {
	cfg        config.MetaConfig
	httpClient *http.Client
	logger     *zap.Logger
}

type SendTextRequest struct {
	MessagingProduct string `json:"messaging_product"`
	RecipientType    string `json:"recipient_type"`
	To               string `json:"to"`
	Type             string `json:"type"`
	Text             Text   `json:"text"`
}

type Text struct {
	PreviewURL bool   `json:"preview_url"`
	Body       string `json:"body"`
}

func NewClient(cfg config.MetaConfig, logger *zap.Logger) *Client {
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
		logger: logger,
	}
}

func (c *Client) SendTextMessage(ctx context.Context, to string, body string) error {
	if c.cfg.APIBaseURL == "" || c.cfg.AccessToken == "" || c.cfg.PhoneNumberID == "" {
		return ErrNotConfigured
	}
	if whatsapp.IsWhatsAppGroupChatID(to) {
		if c.logger != nil {
			c.logger.Warn("outgoing WhatsApp message blocked because recipient is a group chat", zap.String("to", to))
		}
		return ErrBlockedWhatsAppGroupChat
	}

	payload := SendTextRequest{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               to,
		Type:             "text",
		Text: Text{
			PreviewURL: false,
			Body:       body,
		},
	}

	requestBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal meta send payload: %w", err)
	}

	url := fmt.Sprintf("%s/%s/messages", strings.TrimRight(c.cfg.APIBaseURL, "/"), c.cfg.PhoneNumberID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("create meta request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send meta message: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("meta api returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	c.logger.Info("meta message sent", zap.String("to", to), zap.Int("status", resp.StatusCode))
	return nil
}

func VerifySignature(body []byte, signatureHeader string, appSecret string) bool {
	if strings.TrimSpace(appSecret) == "" {
		return true
	}

	const prefix = "sha256="
	if !strings.HasPrefix(signatureHeader, prefix) {
		return false
	}

	provided, err := hex.DecodeString(strings.TrimPrefix(signatureHeader, prefix))
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(appSecret))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)

	return hmac.Equal(provided, expected)
}

type WebhookPayload struct {
	Object string         `json:"object"`
	Entry  []WebhookEntry `json:"entry"`
}

type WebhookEntry struct {
	ID      string          `json:"id"`
	Changes []WebhookChange `json:"changes"`
}

type WebhookChange struct {
	Field string             `json:"field"`
	Value WebhookChangeValue `json:"value"`
}

type WebhookChangeValue struct {
	MessagingProduct string           `json:"messaging_product"`
	Metadata         WebhookMetadata  `json:"metadata"`
	Contacts         []WebhookContact `json:"contacts"`
	Messages         []WebhookMessage `json:"messages"`
	Statuses         []WebhookStatus  `json:"statuses"`
}

type WebhookMetadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

type WebhookContact struct {
	Profile WebhookProfile `json:"profile"`
	WaID    string         `json:"wa_id"`
}

type WebhookProfile struct {
	Name string `json:"name"`
}

type WebhookMessage struct {
	From      string      `json:"from"`
	ID        string      `json:"id"`
	Timestamp string      `json:"timestamp"`
	Type      string      `json:"type"`
	Text      WebhookText `json:"text"`
}

type WebhookText struct {
	Body string `json:"body"`
}

type WebhookStatus struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Timestamp   string `json:"timestamp"`
	RecipientID string `json:"recipient_id"`
}

type TextWebhookMessage struct {
	From string
	ID   string
	Text string
}

func (p WebhookPayload) TextMessages() []TextWebhookMessage {
	var messages []TextWebhookMessage
	for _, entry := range p.Entry {
		for _, change := range entry.Changes {
			for _, message := range change.Value.Messages {
				if message.Type != "text" {
					continue
				}
				body := strings.TrimSpace(message.Text.Body)
				if body == "" {
					continue
				}
				messages = append(messages, TextWebhookMessage{
					From: message.From,
					ID:   message.ID,
					Text: body,
				})
			}
		}
	}
	return messages
}
