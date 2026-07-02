package bot

import (
	"path/filepath"
	"strings"
)

func detectPackageFromReplyContext(conversation Conversation, msg IncomingMessage) (string, bool) {
	if !incomingHasReplyContext(msg) {
		return "", false
	}
	if isQuotedQualificationPrompt(msg.QuotedText) {
		return "", false
	}

	if quotedID := strings.TrimSpace(msg.QuotedMessageID); quotedID != "" {
		if metadata, ok := conversation.OutgoingPackageMessages[quotedID]; ok {
			if packageKeyValue := normalizePackageInterest(metadata.PackageKey); isPackageSelectionKey(packageKeyValue) {
				return packageKeyValue, true
			}
		}
	}

	for _, value := range []string{
		msg.QuotedFileName,
		msg.QuotedCaption,
		msg.QuotedText,
	} {
		if packageKeyValue, ok := detectPackageFromQuotedContent(value); ok {
			return packageKeyValue, true
		}
	}

	return "", false
}

func isQuotedQualificationPrompt(value string) bool {
	normalized := normalizeForAnalysis(value)
	if normalized == "" {
		return false
	}
	hasNicheQuestion := containsAny(normalized, []string{
		"в какой нише", "какая ниша", "что продаете", "какая у вас ниша",
		"что именно продвигаем", "что продвигаем", "кто ваша аудитория",
		"кай нишада", "кай ниша", "не сатасыз",
		"what niche", "what is your niche", "what do you sell", "what are we promoting",
	})
	hasGoalQuestion := containsAny(normalized, []string{"какая цель", "цель ролика", "максат", "goal"})
	hasDeadlineQuestion := containsAny(normalized, []string{"в какие сроки", "кашан иске", "when do you need"})
	return hasNicheQuestion && (hasGoalQuestion || hasDeadlineQuestion)
}

func incomingHasReplyContext(msg IncomingMessage) bool {
	return strings.TrimSpace(msg.QuotedMessageID) != "" ||
		strings.TrimSpace(msg.QuotedText) != "" ||
		strings.TrimSpace(msg.QuotedCaption) != "" ||
		strings.TrimSpace(msg.QuotedType) != "" ||
		strings.TrimSpace(msg.QuotedFileName) != ""
}

func detectPackageFromQuotedContent(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}

	fileName := strings.TrimSpace(filepath.Base(value))
	if offer, ok := OfferByVideo(fileName); ok {
		return packageKey(offer.Level), true
	}

	normalized := normalizeForAnalysis(value)
	candidates := map[string]bool{}
	add := func(packageKeyValue string) {
		if isPackageSelectionKey(packageKeyValue) {
			candidates[packageKeyValue] = true
		}
	}

	if containsAny(normalized, []string{
		"video_level_1.mp4", "тестовый формат", "тестилик формат", "test", "35 000", "35000",
	}) {
		add("test")
	}
	if containsAny(normalized, []string{
		"video_level_2.mp4", "базовый формат", "базалык формат", "basic", "50 000", "50000",
	}) {
		add("basic")
	}
	if containsAny(normalized, []string{
		"video_level_3.mp4", "стандарт (премиум", "стандарт", "премиум", "standard", "premium", "75 000", "75000",
	}) {
		add("standard")
	}

	if len(candidates) != 1 {
		return "", false
	}
	for packageKeyValue := range candidates {
		return packageKeyValue, true
	}
	return "", false
}

func levelByPackageKey(packageKeyValue string) int {
	switch normalizePackageInterest(packageKeyValue) {
	case "test":
		return 1
	case "basic":
		return 2
	case "standard":
		return 3
	default:
		return 0
	}
}
