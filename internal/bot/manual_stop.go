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
	clean := strings.ToLower(strings.TrimSpace(text))
	clean = strings.TrimFunc(clean, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	return strings.Join(strings.Fields(clean), " ")
}

// IsAdminStopCommand reports whether a manual operator message is the stop
// command. It accepts case, surrounding spaces, simple punctuation and the
// slash-command form (e.g. "СТОП!", " stop ", "/стоп"). It also tolerates mixed
// Cyrillic/Latin look-alike characters, which phone keyboards routinely produce
// for "стоп"/"stop". To avoid false positives it only matches a single short
// command token, so sentences like "stop motion video" or "останови ролик" do
// not trigger a stop.
func IsAdminStopCommand(text string) bool {
	normalized := NormalizeAdminStopCommand(text)
	if normalized == "" {
		return false
	}
	// Only a single command-like token may stop the bot.
	if strings.ContainsRune(normalized, ' ') {
		return false
	}
	folded := foldStopConfusables(normalized)
	return folded == foldStopConfusables("stop") || folded == foldStopConfusables("стоп")
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
