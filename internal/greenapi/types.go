package greenapi

import "strings"

const (
	TypeWebhookIncomingMessage = "incomingMessageReceived"

	TypeMessageText         = "textMessage"
	TypeMessageExtendedText = "extendedTextMessage"
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
}

type TextMessageData struct {
	TextMessage string `json:"textMessage"`
}

type ExtendedTextMessageData struct {
	Text        string `json:"text"`
	Description string `json:"description"`
}

type FileMessageData struct {
	Caption string `json:"caption"`
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
	return messageType == TypeMessageText || messageType == TypeMessageExtendedText
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
		return strings.TrimSpace(n.Body.MessageData.ExtendedTextMessageData.Description)
	default:
		return ""
	}
}
