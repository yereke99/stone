package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/yereke99/stone/internal/bot"
	"github.com/yereke99/stone/internal/config"
	"github.com/yereke99/stone/internal/meta"
	"go.uber.org/zap"
)

const maxWebhookBodyBytes = 1 << 20

type MessageSender interface {
	SendTextMessage(ctx context.Context, to string, body string) error
}

type WebhookHandler struct {
	metaConfig config.MetaConfig
	bot        *bot.Manager
	sender     MessageSender
	logger     *zap.Logger
}

func NewWebhookHandler(metaConfig config.MetaConfig, botManager *bot.Manager, sender MessageSender, logger *zap.Logger) *WebhookHandler {
	return &WebhookHandler{
		metaConfig: metaConfig,
		bot:        botManager,
		sender:     sender,
		logger:     logger,
	}
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.verifyWebhook(w, r)
	case http.MethodPost:
		h.receiveWebhook(w, r)
	default:
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

func (h *WebhookHandler) verifyWebhook(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "subscribe" && token == h.metaConfig.WebhookVerifyToken {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(challenge))
		return
	}

	h.logger.Warn("webhook verification failed", zap.String("mode", mode))
	http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
}

func (h *WebhookHandler) receiveWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes))
	if err != nil {
		http.Error(w, "read webhook body", http.StatusBadRequest)
		return
	}

	if !meta.VerifySignature(body, r.Header.Get("X-Hub-Signature-256"), h.metaConfig.AppSecret) {
		h.logger.Warn("invalid meta webhook signature")
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	var payload meta.WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.logger.Warn("invalid webhook json", zap.Error(err))
		http.Error(w, "invalid webhook json", http.StatusBadRequest)
		return
	}

	messages := payload.TextMessages()
	sentCount := 0
	failedCount := 0

	for _, message := range messages {
		replies, err := h.bot.HandleIncoming(r.Context(), message.From, message.Text)
		if err != nil {
			failedCount++
			h.logger.Error("bot reply failed", zap.Error(err), zap.String("from", message.From), zap.String("message_id", message.ID))
			continue
		}

		for _, reply := range replies {
			if err := h.sender.SendTextMessage(r.Context(), message.From, reply.Text); err != nil {
				failedCount++
				if errors.Is(err, meta.ErrNotConfigured) {
					h.logger.Warn("meta client is not configured, reply was not sent", zap.String("from", message.From))
					continue
				}
				h.logger.Error("send whatsapp reply failed", zap.Error(err), zap.String("from", message.From))
				continue
			}
			sentCount++
		}
	}

	writeJSON(w, http.StatusOK, map[string]int{
		"received": len(messages),
		"sent":     sentCount,
		"failed":   failedCount,
	})
}
