package bot

import (
	"context"
	"database/sql"
	"strings"

	"go.uber.org/zap"
)

func NormalizePhone(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	if phone, _, ok := strings.Cut(input, "@"); ok {
		input = phone
	}

	var digits strings.Builder
	for _, r := range input {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}

	phone := digits.String()
	if (len(phone) == 10 || len(phone) == 11) && strings.HasPrefix(phone, "8") {
		phone = "7" + phone[1:]
	}
	return phone
}

func (s *ConversationStore) IsSuppressedPhoneOrChatID(chatID string, phone string) bool {
	if s == nil || s.db == nil {
		return false
	}

	normalizedPhones := uniqueNonEmptyStrings([]string{
		NormalizePhone(phone),
		NormalizePhone(chatID),
	})
	chatIDs := uniqueNonEmptyStrings([]string{
		strings.TrimSpace(chatID),
		chatIDFromNormalizedPhone(NormalizePhone(phone)),
		chatIDFromNormalizedPhone(NormalizePhone(chatID)),
	})

	ctx := context.Background()
	for _, normalizedPhone := range normalizedPhones {
		if suppressionValueExists(ctx, s.db, `SELECT 1 FROM whatsapp_automation_suppression WHERE normalized_phone = ? LIMIT 1`, normalizedPhone) {
			return true
		}
	}
	for _, candidateChatID := range chatIDs {
		if suppressionValueExists(ctx, s.db, `SELECT 1 FROM whatsapp_automation_suppression WHERE chat_id = ? LIMIT 1`, candidateChatID) {
			return true
		}
	}
	return false
}

func (s *ConversationStore) SuppressAutomation(ctx context.Context, chatID string, reason string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}
	normalizedPhone := NormalizePhone(chatID)
	if normalizedPhone == "" {
		return nil
	}
	normalizedChatID := chatID
	if !strings.Contains(normalizedChatID, "@") {
		normalizedChatID = chatIDFromNormalizedPhone(normalizedPhone)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "automation_stop"
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO whatsapp_automation_suppression
		(raw_phone, normalized_phone, chat_id, reason)
		VALUES (?, ?, ?, ?)`,
		chatID,
		normalizedPhone,
		normalizedChatID,
		reason,
	)
	return err
}

func suppressionValueExists(ctx context.Context, db *sql.DB, query string, value string) bool {
	var exists int
	err := db.QueryRowContext(ctx, query, strings.TrimSpace(value)).Scan(&exists)
	return err == nil && exists == 1
}

func chatIDFromNormalizedPhone(phone string) string {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return ""
	}
	return phone + "@c.us"
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (s *Service) isAutomationSuppressed(chatID string) bool {
	if s == nil || s.store == nil {
		return false
	}
	return s.store.IsSuppressedPhoneOrChatID(chatID, phoneFromChatID(chatID))
}

func (s *Service) logAutomationSuppressionSkip(message string, chatID string, fields ...zap.Field) {
	fields = append([]zap.Field{zap.String("chat_hash", chatFingerprint(chatID))}, fields...)
	s.info(message, fields...)
}
