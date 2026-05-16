package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/yereke99/stone/internal/openai"
)

type fakeSender struct {
	messages []string
	chatIDs  []string
	files    []string
}

func (s *fakeSender) SendMessage(ctx context.Context, chatID string, message string) error {
	s.chatIDs = append(s.chatIDs, chatID)
	s.messages = append(s.messages, message)
	return nil
}

func (s *fakeSender) SendFileByUpload(ctx context.Context, chatID string, filePath string, caption string) error {
	s.files = append(s.files, filePath)
	return nil
}

type fakeAI struct {
	called bool
}

func (ai *fakeAI) GenerateSalesReply(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.SalesResponse, error) {
	ai.called = true
	return openai.SalesResponse{
		Reply:    "Спасибо, уточню детали.",
		Language: "ru",
		Stage:    "diagnosis",
	}, nil
}

func TestFirstRussianMessageSendsQualificationGreeting(t *testing.T) {
	sender := &fakeSender{}
	ai := &fakeAI{}
	service := NewService(sender, ai, NewConversationStore(), "./video", PortfolioLinks{}, "auto", nil)

	err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		ChatID: "chat",
		Text:   "Здравствуйте",
	})
	if err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if ai.called {
		t.Fatal("OpenAI must not be called for the first qualification greeting")
	}
	if len(sender.messages) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(sender.messages))
	}
	if got := sender.messages[0]; got != QualificationGreetingText("ru") {
		t.Fatalf("unexpected greeting:\n%s", got)
	}
}

func TestUnknownFirstMessageDefaultsToRussianWithoutLanguageQuestion(t *testing.T) {
	sender := &fakeSender{}
	service := NewService(sender, &fakeAI{}, NewConversationStore(), "./video", PortfolioLinks{}, "auto", nil)

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: "chat", Text: "ok"}); err != nil {
		t.Fatalf("first HandleIncomingMessage() error = %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("sent messages = %d, want 1: %#v", len(sender.messages), sender.messages)
	}
	if sender.messages[0] == LanguageChoiceText() {
		t.Fatalf("bot asked language choice: %#v", sender.messages)
	}
	if !strings.Contains(sender.messages[0], "нишу") {
		t.Fatalf("unexpected default ru reply: %#v", sender.messages)
	}
}

func TestLanguageIsLockedAfterFirstDetection(t *testing.T) {
	sender := &fakeSender{}
	service := NewService(sender, &fakeAI{}, NewConversationStore(), "./video", PortfolioLinks{}, "auto", nil)

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: "chat", Text: "Сәлеметсіз бе"}); err != nil {
		t.Fatalf("first HandleIncomingMessage() error = %v", err)
	}
	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: "chat", Text: "цена"}); err != nil {
		t.Fatalf("second HandleIncomingMessage() error = %v", err)
	}

	if len(sender.messages) != 2 {
		t.Fatalf("sent messages = %d, want 2", len(sender.messages))
	}
	if got := sender.messages[1]; got != PriceText("kk") {
		t.Fatalf("language was not locked to Kazakh, got:\n%s", got)
	}
}

func TestRepliesUseDetectedLanguageWithoutAskingChoice(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		contains string
	}{
		{name: "ru", text: "Здравствуйте", contains: "Здравствуйте"},
		{name: "kk", text: "Сәлем", contains: "Сәлеметсіз"},
		{name: "en", text: "Hello", contains: "Hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakeSender{}
			service := NewService(sender, &fakeAI{}, NewConversationStore(), "./video", PortfolioLinks{}, "auto", nil)

			if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: "chat-" + tt.name, Text: tt.text}); err != nil {
				t.Fatalf("HandleIncomingMessage() error = %v", err)
			}
			if len(sender.messages) != 1 {
				t.Fatalf("sent messages = %d, want 1: %#v", len(sender.messages), sender.messages)
			}
			if sender.messages[0] == LanguageChoiceText() {
				t.Fatalf("bot asked language choice: %#v", sender.messages)
			}
			if !strings.Contains(sender.messages[0], tt.contains) {
				t.Fatalf("reply = %q, want containing %q", sender.messages[0], tt.contains)
			}
		})
	}
}
