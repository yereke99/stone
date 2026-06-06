package greenapi

import (
	"encoding/json"
	"strings"

	"github.com/yereke99/stone/internal/whatsapp"
)

const (
	TypeWebhookIncomingMessage    = "incomingMessageReceived"
	TypeWebhookOutgoingMessage    = "outgoingMessageReceived"
	TypeWebhookOutgoingAPIMessage = "outgoingAPIMessageReceived"

	TypeMessageText         = "textMessage"
	TypeMessageExtendedText = "extendedTextMessage"
	TypeMessageQuoted       = "quotedMessage"
	TypeMessageImage        = "imageMessage"
	TypeMessageVideo        = "videoMessage"
	TypeMessageAudio        = "audioMessage"
	TypeMessageVoice        = "voiceMessage"
	TypeMessageDocument     = "documentMessage"
	TypeMessageSticker      = "stickerMessage"
)

type Notification struct {
	ReceiptID int              `json:"receiptId"`
	Body      NotificationBody `json:"body"`
	RawJSON   json.RawMessage  `json:"-"`
}

func (n *Notification) UnmarshalJSON(data []byte) error {
	type notificationAlias struct {
		ReceiptID int              `json:"receiptId"`
		Body      NotificationBody `json:"body"`
	}
	var decoded notificationAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	n.ReceiptID = decoded.ReceiptID
	n.Body = decoded.Body
	n.RawJSON = append(n.RawJSON[:0], data...)
	return nil
}

type NotificationBody struct {
	TypeWebhook string      `json:"typeWebhook"`
	Timestamp   int64       `json:"timestamp"`
	IDMessage   string      `json:"idMessage"`
	SenderData  SenderData  `json:"senderData"`
	MessageData MessageData `json:"messageData"`
}

type SenderData struct {
	ChatID            string `json:"chatId"`
	Sender            string `json:"sender"`
	SenderName        string `json:"senderName"`
	SenderContactName string `json:"senderContactName"`
}

type MessageData struct {
	TypeMessage             string                  `json:"typeMessage"`
	ChatID                  string                  `json:"chatId"`
	Chat                    string                  `json:"chat"`
	RemoteJID               string                  `json:"remoteJid"`
	From                    string                  `json:"from"`
	Text                    string                  `json:"text"`
	TextMessage             string                  `json:"textMessage"`
	Message                 string                  `json:"message"`
	Caption                 string                  `json:"caption"`
	TextMessageData         TextMessageData         `json:"textMessageData"`
	ExtendedTextMessageData ExtendedTextMessageData `json:"extendedTextMessageData"`
	FileMessageData         FileMessageData         `json:"fileMessageData"`
	QuotedMessage           QuotedMessageData       `json:"quotedMessage"`
}

type TextMessageData struct {
	TextMessage string `json:"textMessage"`
}

type ExtendedTextMessageData struct {
	Text        string `json:"text"`
	Description string `json:"description"`
	StanzaID    string `json:"stanzaId"`
	Participant string `json:"participant"`
}

type FileMessageData struct {
	Caption string `json:"caption"`
}

type QuotedMessageData struct {
	StanzaID            string                    `json:"stanzaId"`
	Participant         string                    `json:"participant"`
	TypeMessage         string                    `json:"typeMessage"`
	TextMessage         string                    `json:"textMessage"`
	Caption             string                    `json:"caption"`
	FileName            string                    `json:"fileName"`
	ExtendedTextMessage QuotedExtendedTextMessage `json:"extendedTextMessage"`
}

type QuotedExtendedTextMessage struct {
	Description string `json:"description"`
	Title       string `json:"title"`
}

type ChatHistoryMessage struct {
	Type                    string                       `json:"type"`
	IDMessage               string                       `json:"idMessage"`
	Timestamp               int64                        `json:"timestamp"`
	TypeMessage             string                       `json:"typeMessage"`
	ChatID                  string                       `json:"chatId"`
	TextMessage             string                       `json:"textMessage"`
	Caption                 string                       `json:"caption"`
	ExtendedTextMessage     ChatHistoryExtendedText      `json:"extendedTextMessage"`
	ExtendedTextMessageData ChatHistoryExtendedText      `json:"extendedTextMessageData"`
	QuotedMessage           ChatHistoryQuotedMessageData `json:"quotedMessage"`
	DeletedMessageID        string                       `json:"deletedMessageId"`
	EditedMessageID         string                       `json:"editedMessageId"`
	IsDeleted               bool                         `json:"isDeleted"`
	IsEdited                bool                         `json:"isEdited"`
}

type ChatHistoryExtendedText struct {
	Text        string `json:"text"`
	Description string `json:"description"`
	Title       string `json:"title"`
	StanzaID    string `json:"stanzaId"`
	Participant string `json:"participant"`
}

type ChatHistoryQuotedMessageData struct {
	StanzaID            string                  `json:"stanzaId"`
	Participant         string                  `json:"participant"`
	TypeMessage         string                  `json:"typeMessage"`
	TextMessage         string                  `json:"textMessage"`
	Caption             string                  `json:"caption"`
	FileName            string                  `json:"fileName"`
	ExtendedTextMessage ChatHistoryExtendedText `json:"extendedTextMessage"`
}

func (m ChatHistoryMessage) Direction() string {
	switch strings.TrimSpace(m.Type) {
	case "incoming":
		return "incoming"
	case "outgoing":
		return "outgoing"
	default:
		return ""
	}
}

func (m ChatHistoryMessage) Text() string {
	if text := strings.TrimSpace(m.TextMessage); text != "" {
		return text
	}
	if text := strings.TrimSpace(m.ExtendedTextMessage.Text); text != "" {
		return text
	}
	if text := strings.TrimSpace(m.ExtendedTextMessage.Description); text != "" {
		return text
	}
	if text := strings.TrimSpace(m.ExtendedTextMessage.Title); text != "" {
		return text
	}
	if text := strings.TrimSpace(m.Caption); text != "" {
		return text
	}
	if text := strings.TrimSpace(m.QuotedMessage.TextMessage); text != "" {
		return text
	}
	if text := strings.TrimSpace(m.QuotedMessage.Caption); text != "" {
		return text
	}
	if text := strings.TrimSpace(m.QuotedMessage.ExtendedTextMessage.Text); text != "" {
		return text
	}
	if text := strings.TrimSpace(m.QuotedMessage.ExtendedTextMessage.Description); text != "" {
		return text
	}
	return strings.TrimSpace(m.QuotedMessage.ExtendedTextMessage.Title)
}

func (n *Notification) IsIncomingMessage() bool {
	return n != nil && n.Body.TypeWebhook == TypeWebhookIncomingMessage
}

func (n *Notification) IDMessage() string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.Body.IDMessage)
}

func (n *Notification) ChatID() string {
	if n == nil {
		return ""
	}
	return firstPreferredWhatsAppChatID(n.chatIDCandidates(
		n.Body.SenderData.ChatID,
		n.Body.MessageData.ChatID,
		n.Body.MessageData.Chat,
		n.Body.MessageData.RemoteJID,
		n.Body.MessageData.From,
		n.Body.SenderData.Sender,
	))
}

func (n *Notification) OutgoingChatID() string {
	if n == nil {
		return ""
	}
	return firstPreferredWhatsAppChatID(n.chatIDCandidates(
		n.Body.MessageData.ChatID,
		n.Body.MessageData.Chat,
		n.Body.MessageData.RemoteJID,
		n.Body.SenderData.ChatID,
		n.Body.SenderData.Sender,
		n.Body.MessageData.From,
	))
}

func (n *Notification) ChatIDCandidates() []string {
	if n == nil {
		return nil
	}
	return n.chatIDCandidates(
		n.Body.SenderData.ChatID,
		n.Body.MessageData.ChatID,
		n.Body.MessageData.Chat,
		n.Body.MessageData.RemoteJID,
		n.Body.MessageData.From,
		n.Body.SenderData.Sender,
	)
}

func (n *Notification) chatIDCandidates(candidates ...string) []string {
	seen := make(map[string]bool, len(candidates))
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		key := strings.ToLower(candidate)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, candidate)
	}
	return result
}

func firstPreferredWhatsAppChatID(candidates []string) string {
	for _, candidate := range candidates {
		if whatsapp.IsPrivateWhatsAppCustomerChatID(candidate) || whatsapp.IsWhatsAppGroupChatID(candidate) {
			return strings.TrimSpace(candidate)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	return strings.TrimSpace(candidates[0])
}

func (n *Notification) IsWhatsAppGroupMessage() bool {
	if n == nil {
		return false
	}
	for _, candidate := range []string{
		n.Body.SenderData.ChatID,
		n.Body.SenderData.Sender,
		n.Body.MessageData.ChatID,
		n.Body.MessageData.Chat,
		n.Body.MessageData.RemoteJID,
		n.Body.MessageData.From,
	} {
		if whatsapp.IsWhatsAppGroupChatID(candidate) {
			return true
		}
	}
	if rawPayloadIndicatesGroup(n.RawJSON) {
		return true
	}
	chatID := n.ChatID()
	return chatID != "" && !whatsapp.IsPrivateWhatsAppCustomerChatID(chatID)
}

func rawPayloadIndicatesGroup(data json.RawMessage) bool {
	if len(data) == 0 {
		return false
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return false
	}
	return valueIndicatesWhatsAppGroup(payload)
}

func valueIndicatesWhatsAppGroup(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalizedKey := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "_", ""), "-", ""))
			if normalizedKey == "isgroup" {
				if group, ok := nested.(bool); ok && group {
					return true
				}
			}
			if strings.Contains(normalizedKey, "group") {
				switch groupValue := nested.(type) {
				case bool:
					if groupValue {
						return true
					}
				case string:
					if strings.TrimSpace(groupValue) != "" &&
						!strings.EqualFold(strings.TrimSpace(groupValue), "false") &&
						strings.TrimSpace(groupValue) != "0" {
						return true
					}
				}
			}
			if valueIndicatesWhatsAppGroup(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if valueIndicatesWhatsAppGroup(nested) {
				return true
			}
		}
	case string:
		value := strings.ToLower(strings.TrimSpace(typed))
		return whatsapp.IsWhatsAppGroupChatID(value) || strings.Contains(value, "@g.us")
	}
	return false
}

func (n *Notification) SenderName() string {
	if n == nil {
		return ""
	}
	if name := strings.TrimSpace(n.Body.SenderData.SenderName); name != "" {
		return name
	}
	return strings.TrimSpace(n.Body.SenderData.SenderContactName)
}

func (n *Notification) TypeMessage() string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.Body.MessageData.TypeMessage)
}

func (n *Notification) IsTextMessage() bool {
	messageType := n.TypeMessage()
	return messageType == TypeMessageText || messageType == TypeMessageExtendedText || messageType == TypeMessageQuoted
}

func (n *Notification) IsMediaMessage() bool {
	switch n.TypeMessage() {
	case TypeMessageImage, TypeMessageVideo, TypeMessageAudio, TypeMessageVoice, TypeMessageDocument, TypeMessageSticker:
		return true
	default:
		return false
	}
}

func (n *Notification) Text() string {
	if n == nil {
		return ""
	}

	data := n.Body.MessageData
	candidates := []string{
		data.TextMessageData.TextMessage,
		data.ExtendedTextMessageData.Text,
		data.ExtendedTextMessageData.Description,
		data.FileMessageData.Caption,
		data.Text,
		data.TextMessage,
		data.Message,
		data.Caption,
	}
	switch n.TypeMessage() {
	case TypeMessageText:
		candidates = []string{
			data.TextMessageData.TextMessage,
			data.ExtendedTextMessageData.Text,
			data.ExtendedTextMessageData.Description,
			data.Text,
			data.TextMessage,
			data.Message,
		}
	case TypeMessageExtendedText:
		candidates = []string{
			data.ExtendedTextMessageData.Text,
			data.TextMessageData.TextMessage,
			data.ExtendedTextMessageData.Description,
			data.Text,
			data.TextMessage,
			data.Message,
		}
	case TypeMessageQuoted:
		candidates = []string{
			data.ExtendedTextMessageData.Text,
			data.TextMessageData.TextMessage,
			data.ExtendedTextMessageData.Description,
			data.Text,
			data.TextMessage,
			data.Message,
		}
	case TypeMessageImage, TypeMessageVideo, TypeMessageAudio, TypeMessageVoice, TypeMessageDocument, TypeMessageSticker:
		candidates = []string{
			data.FileMessageData.Caption,
			data.Caption,
			data.Text,
			data.TextMessage,
			data.Message,
		}
	}
	if text := firstTrimmedText(candidates...); text != "" {
		return text
	}
	return textFromRawMessageData(n.RawJSON)
}

func firstTrimmedText(candidates ...string) string {
	for _, candidate := range candidates {
		if text := strings.TrimSpace(candidate); text != "" {
			return text
		}
	}
	return ""
}

func textFromRawMessageData(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	body := mapValue(payload, "body")
	if body == nil {
		body = payload
	}
	messageData := mapValue(body, "messageData")
	if messageData == nil {
		return ""
	}
	return firstTrimmedText(
		stringValue(mapValue(messageData, "textMessageData"), "textMessage"),
		stringValue(mapValue(messageData, "extendedTextMessageData"), "text"),
		stringValue(mapValue(messageData, "extendedTextMessageData"), "description"),
		stringValue(mapValue(messageData, "fileMessageData"), "caption"),
		stringValue(messageData, "text"),
		stringValue(messageData, "textMessage"),
		stringValue(messageData, "message"),
		stringValue(messageData, "caption"),
	)
}

func mapValue(payload map[string]any, key string) map[string]any {
	if payload == nil {
		return nil
	}
	value, ok := payload[key].(map[string]any)
	if !ok {
		return nil
	}
	return value
}

func stringValue(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func (n *Notification) QuotedMessageID() string {
	if n == nil {
		return ""
	}
	if id := strings.TrimSpace(n.Body.MessageData.QuotedMessage.StanzaID); id != "" {
		return id
	}
	return strings.TrimSpace(n.Body.MessageData.ExtendedTextMessageData.StanzaID)
}

func (n *Notification) QuotedText() string {
	if n == nil {
		return ""
	}
	quoted := n.Body.MessageData.QuotedMessage
	if text := strings.TrimSpace(quoted.TextMessage); text != "" {
		return text
	}
	if text := strings.TrimSpace(quoted.ExtendedTextMessage.Title); text != "" {
		return text
	}
	return strings.TrimSpace(quoted.ExtendedTextMessage.Description)
}

func (n *Notification) QuotedCaption() string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.Body.MessageData.QuotedMessage.Caption)
}

func (n *Notification) QuotedType() string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.Body.MessageData.QuotedMessage.TypeMessage)
}

func (n *Notification) QuotedFileName() string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.Body.MessageData.QuotedMessage.FileName)
}
