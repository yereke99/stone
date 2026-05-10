package bot

import (
	"strings"
	"unicode"
)

type Language string

const (
	LanguageRU Language = "ru"
	LanguageKZ Language = "kz"
	LanguageEN Language = "en"
)

func DetectLanguage(text string) Language {
	normalized := strings.ToLower(text)

	kazakhMarkers := []string{
		"ә", "ғ", "қ", "ң", "ө", "ұ", "ү", "һ", "і",
		"сәлем", "баға", "қанша", "қалай", "қымбат", "ойлан", "сатылым",
	}
	for _, marker := range kazakhMarkers {
		if strings.Contains(normalized, marker) {
			return LanguageKZ
		}
	}

	hasCyrillic := false
	hasLatin := false
	for _, r := range normalized {
		if unicode.Is(unicode.Cyrillic, r) {
			hasCyrillic = true
		}
		if unicode.Is(unicode.Latin, r) {
			hasLatin = true
		}
	}

	if hasCyrillic {
		return LanguageRU
	}
	if hasLatin {
		return LanguageEN
	}

	return LanguageRU
}
