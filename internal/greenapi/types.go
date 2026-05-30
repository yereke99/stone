package greenapi

import "strings"

const (
	TypeWebhookIncomingMessage = "incomingMessageReceived"

	TypeMessageText         = "textMessage"
	TypeMessageExtendedText = "extendedTextMessage"
	TypeMessageQuoted       = "quotedMessage"
)

type Notification struct {
	ReceiptID int              `json:"receiptId"`
	Body      NotificationBody `json:"body"`
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
	return strings.TrimSpace(n.Body.SenderData.ChatID)
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

func (n *Notification) Text() string {
	if n == nil {
		return ""
	}

	switch n.TypeMessage() {
	case TypeMessageText:
		return strings.TrimSpace(n.Body.MessageData.TextMessageData.TextMessage)
	case TypeMessageExtendedText:
		if text := strings.TrimSpace(n.Body.MessageData.ExtendedTextMessageData.Text); text != "" {
			return text
		}
		if text := strings.TrimSpace(n.Body.MessageData.TextMessageData.TextMessage); text != "" {
			return text
		}
		return strings.TrimSpace(n.Body.MessageData.ExtendedTextMessageData.Description)
	case TypeMessageQuoted:
		if text := strings.TrimSpace(n.Body.MessageData.ExtendedTextMessageData.Text); text != "" {
			return text
		}
		if text := strings.TrimSpace(n.Body.MessageData.TextMessageData.TextMessage); text != "" {
			return text
		}
		return strings.TrimSpace(n.Body.MessageData.ExtendedTextMessageData.Description)
	default:
		return ""
	}
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
