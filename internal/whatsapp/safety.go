package whatsapp

import "strings"

const (
	PurposeCustomerAutomation  = "customer_automation"
	PurposeManagerNotification = "manager_notification"
)

func IsWhatsAppGroupChatID(chatID string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(chatID)), "@g.us")
}

func IsPrivateWhatsAppCustomerChatID(chatID string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(chatID)), "@c.us")
}

func CanSendToWhatsAppChat(chatID string, purpose string, allowedGroupChatIDs []string) bool {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false
	}
	if IsPrivateWhatsAppCustomerChatID(chatID) {
		return true
	}
	if IsWhatsAppGroupChatID(chatID) {
		return strings.TrimSpace(purpose) == PurposeManagerNotification &&
			IsAllowedGroupChatID(chatID, allowedGroupChatIDs)
	}
	return false
}

func IsAllowedGroupChatID(chatID string, allowedGroupChatIDs []string) bool {
	chatID = strings.ToLower(strings.TrimSpace(chatID))
	if chatID == "" {
		return false
	}
	for _, allowed := range allowedGroupChatIDs {
		if strings.ToLower(strings.TrimSpace(allowed)) == chatID {
			return true
		}
	}
	return false
}
