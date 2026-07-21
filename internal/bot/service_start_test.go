package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/yereke99/stone/internal/openai"
)

type fakeSender struct {
	messages       []string
	chatIDs        []string
	files          []string
	captions       []string
	fileMessageIDs []string
}

func (s *fakeSender) SendMessage(ctx context.Context, chatID string, message string) error {
	s.chatIDs = append(s.chatIDs, chatID)
	s.messages = append(s.messages, message)
	return nil
}

func (s *fakeSender) SendFileByUpload(ctx context.Context, chatID string, filePath string, caption string) (string, error) {
	messageID := ""
	if len(s.fileMessageIDs) > len(s.files) {
		messageID = s.fileMessageIDs[len(s.files)]
	}
	s.files = append(s.files, filePath)
	s.captions = append(s.captions, caption)
	return messageID, nil
}

type fakeAI struct {
	called         bool
	analysisCalled bool
}

func (ai *fakeAI) GenerateSalesReply(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.SalesResponse, error) {
	ai.called = true
	return openai.SalesResponse{
		Reply:    "Спасибо, уточню детали.",
		Language: "ru",
		Stage:    "diagnosis",
	}, nil
}

func (ai *fakeAI) GenerateReplyText(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.ReplyTextResponse, error) {
	ai.called = true
	return openai.ReplyTextResponse{ReplyText: "Спасибо, уточню детали."}, nil
}

func (ai *fakeAI) AnalyzeCustomerMessage(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.CustomerUnderstanding, error) {
	ai.analysisCalled = true
	return openai.CustomerUnderstanding{
		Language:   "ru",
		Intent:     "other",
		Confidence: 1,
	}, nil
}

func TestFirstRussianMessageSendsQualificationGreeting(t *testing.T) {
	sender := &fakeSender{}
	ai := &fakeAI{}
	store := NewConversationStore()
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)

	err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		ChatID: "chat",
		Text:   "Здравствуйте",
	})
	if err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if ai.analysisCalled {
		t.Fatal("simple first greeting must not depend on OpenAI understanding")
	}
	if ai.called {
		t.Fatal("OpenAI sales reply generation must not be called for the first welcome package")
	}
	if len(sender.messages) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(sender.messages))
	}
	if got := sender.messages[0]; got != FirstContactWelcomeText("ru") {
		t.Fatalf("unexpected welcome:\n%s", got)
	}
	if len(sender.files) != 3 {
		t.Fatalf("sent files = %#v, want three portfolio videos", sender.files)
	}
	conversation := snapshotConversation(t, store, "chat")
	if conversation.Stage != ClientStatePackagesPresented || !conversation.PackagesSent || conversation.AutoPackagesSentAt.IsZero() {
		t.Fatalf("welcome package was not persisted: stage=%q packages=%v sent_at=%v", conversation.Stage, conversation.PackagesSent, conversation.AutoPackagesSentAt)
	}
}

func TestUnknownFirstMessageDefaultsToRussianWithoutLanguageQuestion(t *testing.T) {
	sender := &fakeSender{}
	service := NewService(sender, &fakeAI{}, NewConversationStore(), testVideoDir(t), PortfolioLinks{}, "auto", nil)

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: "chat", Text: "ok"}); err != nil {
		t.Fatalf("first HandleIncomingMessage() error = %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("sent messages = %d, want 1: %#v", len(sender.messages), sender.messages)
	}
	if sender.messages[0] == LanguageChoiceText() {
		t.Fatalf("bot asked language choice: %#v", sender.messages)
	}
	if !strings.Contains(sender.messages[0], "Stone Production") || !strings.Contains(sender.messages[0], "35 000") {
		t.Fatalf("unexpected default ru reply: %#v", sender.messages)
	}
	if len(sender.files) != 3 {
		t.Fatalf("sent files = %#v, want three portfolio videos", sender.files)
	}
}

func TestLanguageFollowsLatestCustomerMessageInAutoMode(t *testing.T) {
	sender := &fakeSender{}
	service := NewService(sender, &fakeAI{}, NewConversationStore(), testVideoDir(t), PortfolioLinks{}, "auto", nil)

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: "chat", Text: "Сәлеметсіз бе"}); err != nil {
		t.Fatalf("first HandleIncomingMessage() error = %v", err)
	}
	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: "chat", Text: "цена"}); err != nil {
		t.Fatalf("second HandleIncomingMessage() error = %v", err)
	}

	if len(sender.messages) < 2 {
		t.Fatalf("sent messages = %d, want at least 2", len(sender.messages))
	}
	if got := sender.messages[len(sender.messages)-1]; !strings.Contains(got, "35 000") || strings.Contains(got, "Қай формат") {
		t.Fatalf("language did not follow latest Russian message, got:\n%s", got)
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
			service := NewService(sender, &fakeAI{}, NewConversationStore(), testVideoDir(t), PortfolioLinks{}, "auto", nil)

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
			if len(sender.files) != 3 {
				t.Fatalf("sent files = %#v, want three portfolio videos", sender.files)
			}
		})
	}
}
