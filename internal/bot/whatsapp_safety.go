package bot

import (
	"strings"
	"time"

	"github.com/yereke99/stone/internal/whatsapp"
	"go.uber.org/zap"
)

const (
	WhatsAppPurposeCustomerAutomation  = whatsapp.PurposeCustomerAutomation
	WhatsAppPurposeManagerNotification = whatsapp.PurposeManagerNotification
)

func IsWhatsAppGroupChatID(chatID string) bool {
	return whatsapp.IsWhatsAppGroupChatID(chatID)
}

func IsPrivateWhatsAppCustomerChatID(chatID string) bool {
	return whatsapp.IsPrivateWhatsAppCustomerChatID(chatID)
}

func CanSendToWhatsAppChat(chatID string, purpose string) bool {
	return whatsapp.CanSendToWhatsAppChat(chatID, purpose, nil)
}

func canSendToWhatsAppChat(chatID string, purpose string, allowedGroupChatIDs []string) bool {
	return whatsapp.CanSendToWhatsAppChat(chatID, purpose, allowedGroupChatIDs)
}

func isUnsafeCustomerWhatsAppChatID(chatID string) bool {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return true
	}
	if IsWhatsAppGroupChatID(chatID) {
		return true
	}
	return strings.Contains(chatID, "@") && !IsPrivateWhatsAppCustomerChatID(chatID)
}

func (s *Service) blockOutgoingWhatsAppGroupMessage(chatID string, purpose string) {
	s.info("outgoing WhatsApp message blocked because recipient is a group chat",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("purpose", strings.TrimSpace(purpose)),
	)
}

func disableGroupConversationAutomation(conversation *Conversation) {
	if conversation == nil || !IsWhatsAppGroupChatID(conversation.ChatID) {
		return
	}
	conversation.Stage = ClientStateStopped
	conversation.AutomationClosed = true
	conversation.Stopped = true
	conversation.NextFollowupAt = time.Time{}
	conversation.FollowupStage = ""
	conversation.FollowupReferenceAt = time.Time{}
	conversation.LastFollowupSentAt = time.Time{}
	conversation.Lead.LeadStatus = LeadStatusMuted
	conversation.LeadStatus = LeadStatusMuted
}
