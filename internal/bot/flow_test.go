package bot

import "testing"

func TestStaticMessagesRespectPolicy(t *testing.T) {
	languages := []Language{LanguageRU, LanguageKZ, LanguageEN}
	links := PortfolioLinks{
		TestURL:     "https://example.com/portfolio/test-format",
		BasicURL:    "https://example.com/portfolio/basic-format",
		StandardURL: "https://example.com/portfolio/standard-format",
	}

	for _, language := range languages {
		messages := []string{
			welcomeMessage(language),
			askGoalMessage(language),
			askPlatformMessage(language),
			askUsedAIMessage(language),
			clarifyUsedAIMessage(language),
			offerMessage(language, false),
			offerMessage(language, true),
			portfolioPromptMessage(language),
			questionnaireMessage(language),
			objectionMessage(language),
		}
		messages = append(messages, portfolioMessages(language, links)...)
		messages = append(messages, portfolioMessages(language, PortfolioLinks{})...)

		for _, message := range messages {
			count := wordCount(message)
			if count < minBotWords || count > maxBotWords {
				t.Fatalf("message for %s has %d words: %q", language, count, message)
			}
		}
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		name string
		text string
		want Language
	}{
		{name: "russian", text: "Здравствуйте, сколько стоит ролик?", want: LanguageRU},
		{name: "kazakh", text: "Сәлем, баға қанша?", want: LanguageKZ},
		{name: "english", text: "Hello, what is the price?", want: LanguageEN},
		{name: "uncertain", text: "12345", want: LanguageRU},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectLanguage(tt.text); got != tt.want {
				t.Fatalf("DetectLanguage() = %s, want %s", got, tt.want)
			}
		})
	}
}
