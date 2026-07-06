package bot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withAIWorksTestRoot(t *testing.T, categories ...string) string {
	t.Helper()
	root := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir(%q) error = %v", root, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	videoDir := filepath.Join(root, "video")
	if err := os.MkdirAll(videoDir, 0o755); err != nil {
		t.Fatalf("mkdir video dir: %v", err)
	}
	for _, name := range ExpectedVideoFiles {
		if err := os.WriteFile(filepath.Join(videoDir, name), []byte("test"), 0o644); err != nil {
			t.Fatalf("write package video %s: %v", name, err)
		}
	}
	for _, category := range categories {
		dir := filepath.Join(root, AIWorksDir, category)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir ai works dir %s: %v", category, err)
		}
		for _, name := range aiWorksByCategory[category] {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0o644); err != nil {
				t.Fatalf("write ai work %s/%s: %v", category, name, err)
			}
		}
	}
	return videoDir
}

func TestNegativeFormatSelectionKeepsKnownNicheAndSendsRelevantExamples(t *testing.T) {
	videoDir := withAIWorksTestRoot(t, "real_estate")
	sender := &fakeSender{}
	store := NewConversationStore()
	service := NewService(sender, &fakeAI{}, store, videoDir, PortfolioLinks{}, "auto", nil)
	chatID := "chat-negative-selection"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Language = "ru"
		conversation.Stage = ClientStatePackagesPresented
		conversation.InitialMessageSent = true
		conversation.PackagesSent = true
		conversation.Lead.OfferSent = true
		conversation.Lead.PortfolioSent = true
		conversation.SentPortfolio = true
		conversation.LastReplyText = FormatQuestionText("ru")
		conversation.Lead.Niche = "земельный участок в пригороде"
		conversation.Lead.ProductOrService = "земельный участок в пригороде"
		conversation.Lead.Goal = "рост продаж"
		conversation.Lead.CampaignContext = "нужна визуализация перспектив и съёмка с дрона"
	})

	analysis := AnalyzeCustomerMessage("Никакой", LeadState{Niche: "земельный участок в пригороде", Goal: "рост продаж"}, "ru")
	if analysis.Intent != IntentNegativeSelection || analysis.Niche != nil || analysis.Goal != nil {
		t.Fatalf("analysis = %#v, want negative_selection without fields", analysis)
	}

	sendText(t, service, chatID, "Никакой")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "земельный участок в пригороде" {
		t.Fatalf("niche overwritten after negative selection: %q", conversation.Lead.Niche)
	}
	if len(sender.messages) == 0 || strings.Contains(strings.ToLower(sender.messages[len(sender.messages)-1]), "никакой") && strings.Contains(strings.ToLower(sender.messages[len(sender.messages)-1]), "понял, никакой") {
		t.Fatalf("bot treated negative selection as data: %#v", sender.messages)
	}
	if len(sender.files) == 0 || !strings.Contains(sender.files[0], "ai-works/real_estate/") {
		t.Fatalf("real estate examples were not sent: %#v", sender.files)
	}
	if len(sender.captions) == 0 || strings.TrimSpace(sender.captions[0]) == "" {
		t.Fatalf("AI work video caption missing: %#v", sender.captions)
	}
}

func TestLandPlotDroneRequestSendsRealEstateExamplesFirst(t *testing.T) {
	videoDir := withAIWorksTestRoot(t, "real_estate")
	sender := &fakeSender{}
	store := NewConversationStore()
	service := NewService(sender, &fakeAI{}, store, videoDir, PortfolioLinks{}, "auto", nil)
	chatID := "chat-land-plot"

	sendText(t, service, chatID, "Здравствуйте. Я хочу продать земельный участок в пригороде. Мне нужна визуализация перспектив и съёмка с дрона")

	conversation := snapshotConversation(t, store, chatID)
	if !strings.Contains(conversation.Lead.Niche, "земельный участок") {
		t.Fatalf("niche = %q, want land plot real estate", conversation.Lead.Niche)
	}
	if !isValidGoal(conversation.Lead.Goal) {
		t.Fatalf("goal was not inferred from sale request: %#v", conversation.Lead)
	}
	if len(sender.files) == 0 || !strings.Contains(sender.files[0], "ai-works/real_estate/") {
		t.Fatalf("real estate examples were not sent first: files=%#v messages=%#v", sender.files, sender.messages)
	}
	for _, message := range sender.messages {
		if strings.Contains(message, FormatQuestionText("ru")) {
			t.Fatalf("format question was asked before relevant examples settled: %#v", sender.messages)
		}
	}
	if len(sender.captions) == 0 || !strings.Contains(strings.ToLower(sender.captions[0]), "пример") {
		t.Fatalf("video was not sent with a relevant caption: %#v", sender.captions)
	}
}

func TestCustomerStopBotSuppressesChatAndSkipsFurtherAutomation(t *testing.T) {
	sender := &fakeSender{}
	store, err := NewSQLiteConversationStore(context.Background(), filepath.Join(t.TempDir(), "stone.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	ai := &fakeAI{}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	chatID := "77045550000@c.us"

	sendText(t, service, chatID, "стоп бот")

	if ai.analysisCalled || ai.called {
		t.Fatalf("STOP reached AI: analysis=%v sales=%v", ai.analysisCalled, ai.called)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateOptOut || !conversation.Stopped || !conversation.AutomationClosed || !conversation.OptOut {
		t.Fatalf("stop state = stage=%q stopped=%v closed=%v optout=%v", conversation.Stage, conversation.Stopped, conversation.AutomationClosed, conversation.OptOut)
	}
	if !store.IsSuppressedPhoneOrChatID(chatID, NormalizePhone(chatID)) {
		t.Fatal("chat was not persisted in automation suppression")
	}

	sendText(t, service, chatID, "Здравствуйте, нужен ролик")
	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("suppressed chat got automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
}

func TestTourismRequestSendsTourismExamples(t *testing.T) {
	videoDir := withAIWorksTestRoot(t, "tourism")
	sender := &fakeSender{}
	store := NewConversationStore()
	service := NewService(sender, &fakeAI{}, store, videoDir, PortfolioLinks{}, "auto", nil)
	chatID := "chat-tourism"

	sendText(t, service, chatID, "у нас туризм, нужны ролики для привлечения клиентов")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "туризм" {
		t.Fatalf("niche = %q, want tourism", conversation.Lead.Niche)
	}
	if conversation.Lead.Goal != "привлечь клиентов" {
		t.Fatalf("goal = %q, want attract clients", conversation.Lead.Goal)
	}
	if len(sender.files) == 0 || !strings.Contains(sender.files[0], "ai-works/tourism/") {
		t.Fatalf("tourism examples were not sent: %#v", sender.files)
	}
}
