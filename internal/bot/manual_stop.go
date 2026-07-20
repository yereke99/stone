package bot

import (
	"context"
	"strings"
	"unicode"
)

// NormalizeAdminStopCommand returns a human-readable, comparable form of an
// operator command: trimmed, lower-cased, with surrounding punctuation removed
// and inner whitespace collapsed. It does NOT fold confusable characters, so it
// is safe to use in logs (e.g. "стоп" stays "стоп", not a folded token).
func NormalizeAdminStopCommand(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	var builder strings.Builder
	builder.Grow(len(text))
	for _, r := range text {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			builder.WriteRune(' ')
			continue
		}
		builder.WriteRune(r)
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

// IsAdminStopCommand reports whether a manual operator or customer message is
// a stop command. It accepts case, spaces, punctuation and common RU/EN stop
// phrases, while still avoiding broad sentences such as "stop motion video".
func IsAdminStopCommand(text string) bool {
	normalized := NormalizeAdminStopCommand(text)
	if normalized == "" {
		return false
	}
	folded := foldStopConfusables(normalized)
	switch folded {
	case foldStopConfusables("stop"),
		foldStopConfusables("стоп"),
		foldStopConfusables("stop bot"),
		foldStopConfusables("стоп бот"):
		return true
	}
	switch normalized {
	case "отключить",
		"не писать",
		"не пишите",
		"не надо писать",
		"больше не писать",
		"больше не пишите",
		"отписаться",
		"unsubscribe",
		"тоқта",
		"токта",
		"тоқтатыңыз",
		"токтатыныз",
		"хватит",
		"остановить":
		return true
	default:
		return false
	}
}

// stopConfusableFolds maps Cyrillic (and a few other Unicode) look-alike runes
// to their Latin equivalents so that a token typed with a mix of scripts folds
// to a single canonical form. "стоп" still differs from "stop" after folding
// (the Cyrillic "п" has no Latin look-alike), so the two distinct commands stay
// distinct while every mixed-script spelling of each one collapses correctly.
var stopConfusableFolds = map[rune]rune{
	'а': 'a', 'е': 'e', 'о': 'o', 'р': 'p', 'с': 'c', 'у': 'y', 'х': 'x',
	'к': 'k', 'м': 'm', 'т': 't', 'н': 'h', 'в': 'b',
	'ѕ': 's', 'і': 'i', 'ј': 'j', 'ԁ': 'd',
}

func foldStopConfusables(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range value {
		if folded, ok := stopConfusableFolds[r]; ok {
			builder.WriteRune(folded)
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

// IsAutomationAllowed reports whether the bot may still send automated messages
// to the given chat. It re-reads the latest persisted state, so a chat that was
// just marked stopped (manual admin stop, opt-out, handoff or any closed state)
// returns false even mid-processing. A chat with no stored state yet is allowed,
// so first-contact automation is not blocked. This is the canonical final guard
// to consult right before any automated WhatsApp send.
func (s *ConversationStore) IsAutomationAllowed(ctx context.Context, chatID string) (bool, error) {
	if s == nil {
		return false, nil
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false, nil
	}
	exists, err := s.ConversationExists(ctx, chatID)
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil
	}
	latest, err := s.Snapshot(ctx, chatID)
	if err != nil {
		return false, err
	}
	if isConversationManuallyStopped(latest) {
		return false, nil
	}
	return canSendAutomationToConversation(latest), nil
}
