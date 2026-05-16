package bot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestService(sender *fakeSender, store *ConversationStore, links PortfolioLinks) *Service {
	return NewService(sender, &fakeAI{}, store, "./video", links, "auto", nil)
}

func newTestServiceWithVideoDir(sender *fakeSender, store *ConversationStore, links PortfolioLinks, videoDir string) *Service {
	return NewService(sender, &fakeAI{}, store, videoDir, links, "auto", nil)
}

func testVideoDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range ExpectedVideoFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0o644); err != nil {
			t.Fatalf("write test video %s: %v", name, err)
		}
	}
	return dir
}

func sendText(t *testing.T, service *Service, chatID string, text string) {
	t.Helper()
	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: chatID, Text: text}); err != nil {
		t.Fatalf("HandleIncomingMessage(%q) error = %v", text, err)
	}
}

func snapshotLead(t *testing.T, store *ConversationStore, chatID string) LeadState {
	t.Helper()
	conversation, err := store.Snapshot(context.Background(), chatID)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	return conversation.Lead
}

func TestPlatformsOnlyAreStoredAsPlatforms(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})

	sendText(t, service, "chat-platforms", "Инстаграм таргет тикток и тд")

	lead := snapshotLead(t, store, "chat-platforms")
	if lead.Niche != "" {
		t.Fatalf("niche = %q, want empty", lead.Niche)
	}
	if got := strings.Join(lead.Platforms, ","); !strings.Contains(got, "Instagram") || !strings.Contains(got, "TikTok") {
		t.Fatalf("platforms = %#v, want Instagram and TikTok", lead.Platforms)
	}
	if len(sender.messages) != 1 || !strings.Contains(sender.messages[0], "нишу") || strings.Contains(sender.messages[0], "площад") {
		t.Fatalf("unexpected reply: %#v", sender.messages)
	}
}

func TestGoalAndDeadlineAreStoredWithoutRepeatingGeneralQuestion(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})

	sendText(t, service, "chat-goal", "Цель продажа больше и срок за неделю")

	lead := snapshotLead(t, store, "chat-goal")
	if lead.Goal != "рост продаж" {
		t.Fatalf("goal = %q, want рост продаж", lead.Goal)
	}
	if lead.Deadline != "через неделю" {
		t.Fatalf("deadline = %q, want через неделю", lead.Deadline)
	}
	if len(sender.messages) != 1 || !strings.Contains(sender.messages[0], "В какой нише") || !strings.Contains(sender.messages[0], "где планируете рекламу") {
		t.Fatalf("unexpected reply: %#v", sender.messages)
	}
}

func TestInstagramAndDeadlineDoNotAskPlatformAgain(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})

	sendText(t, service, "chat-instagram-deadline", "Нужен ролик для Instagram, срок до пятницы")

	lead := snapshotLead(t, store, "chat-instagram-deadline")
	if got := strings.Join(lead.Platforms, ","); !strings.Contains(got, "Instagram") {
		t.Fatalf("platforms = %#v, want Instagram", lead.Platforms)
	}
	if lead.Deadline != "до пятницы" {
		t.Fatalf("deadline = %q, want до пятницы", lead.Deadline)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("sent messages = %d, want 1: %#v", len(sender.messages), sender.messages)
	}
	reply := strings.ToLower(sender.messages[0])
	if strings.Contains(reply, "где планируете") || strings.Contains(reply, "instagram/tiktok") || strings.Contains(reply, "площад") {
		t.Fatalf("bot asked platform again: %q", sender.messages[0])
	}
}

func TestNicheGoalDeadlineDoNotTriggerGeneralRepeat(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})

	sendText(t, service, "chat-core", "Инстаграм таргет тикток")
	sendText(t, service, "chat-core", "Ниша спорт, цель удвоение продажи, сроки за неделя")

	lead := snapshotLead(t, store, "chat-core")
	if lead.Niche != "спорт" || lead.Goal != "удвоить продажи" || lead.Deadline != "через неделю" {
		t.Fatalf("lead = %#v, want core fields collected", lead)
	}
	last := sender.messages[len(sender.messages)-1]
	if strings.Contains(last, "Подскажите нишу, цель") {
		t.Fatalf("bot repeated general question: %q", last)
	}
	if !strings.Contains(last, "Ранее использовали ИИ-ролики") {
		t.Fatalf("unexpected reply: %q", last)
	}
}

func TestPartialAnswersAccumulateIntoState(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})

	sendText(t, service, "chat-parts", "Инстаграм и тикток")
	sendText(t, service, "chat-parts", "Цель продажа больше и срок за неделю")
	sendText(t, service, "chat-parts", "Ниша спорт")

	lead := snapshotLead(t, store, "chat-parts")
	if lead.Niche != "спорт" || lead.Goal != "рост продаж" || lead.Deadline != "через неделю" || len(lead.Platforms) == 0 {
		t.Fatalf("lead = %#v, want accumulated state", lead)
	}
	last := sender.messages[len(sender.messages)-1]
	if !strings.Contains(last, "Ранее использовали ИИ-ролики") {
		t.Fatalf("unexpected reply after completed state: %q", last)
	}
}

func TestNegativeReactionAsksOnlyMissingField(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})

	sendText(t, service, "chat-negative", "ой иди ты надоел")

	if len(sender.messages) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(sender.messages))
	}
	got := sender.messages[0]
	if !strings.Contains(got, "Не буду повторяться") || !strings.Contains(got, "для какой ниши") {
		t.Fatalf("unexpected negative reaction reply: %q", got)
	}
}

func TestPortfolioRequestUsesConfiguredLinksOrAsksFormat(t *testing.T) {
	t.Run("configured links", func(t *testing.T) {
		sender := &fakeSender{}
		store := NewConversationStore()
		service := newTestService(sender, store, PortfolioLinks{TestURL: "https://example.com/test"})

		sendText(t, service, "chat-portfolio-links", "покажите портфолио")

		if len(sender.messages) != 1 || !strings.Contains(sender.messages[0], "https://example.com/test") {
			t.Fatalf("unexpected portfolio links reply: %#v", sender.messages)
		}
	})

	t.Run("missing links", func(t *testing.T) {
		sender := &fakeSender{}
		store := NewConversationStore()
		service := newTestService(sender, store, PortfolioLinks{})

		sendText(t, service, "chat-portfolio-missing", "портфолио")

		if len(sender.messages) != 1 || sender.messages[0] != PortfolioIntroText("ru") {
			t.Fatalf("unexpected missing portfolio reply: %#v", sender.messages)
		}
	})
}

func TestReadyToOrderSendsBrief(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})

	sendText(t, service, "chat-ready", "давайте")

	if len(sender.messages) != 1 || !strings.Contains(sender.messages[0], "Что рекламируем") {
		t.Fatalf("unexpected ready reply: %#v", sender.messages)
	}
}

func TestPreviousAIAnswerOffersTestThenReadySendsBrief(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})

	sendText(t, service, "chat-offer", "Инстаграм и тикток")
	sendText(t, service, "chat-offer", "Ниша спорт, цель удвоение продажи, сроки за неделя")
	sendText(t, service, "chat-offer", "нет не использовал")

	lead := snapshotLead(t, store, "chat-offer")
	if lead.PreviousAIAds == nil || *lead.PreviousAIAds {
		t.Fatalf("previous_ai_ads = %#v, want false", lead.PreviousAIAds)
	}
	if last := sender.messages[len(sender.messages)-1]; !strings.Contains(last, "тестовый формат за 35 000 тг") {
		t.Fatalf("unexpected offer reply: %q", last)
	}

	sendText(t, service, "chat-offer", "давайте")

	if last := sender.messages[len(sender.messages)-1]; !strings.Contains(last, "Что рекламируем") {
		t.Fatalf("unexpected brief reply: %q", last)
	}
}

func TestPackageSelectionIntentStandard(t *testing.T) {
	analysis := AnalyzeCustomerMessage("Ок стандарт нам надо", LeadState{}, "ru")

	if analysis.Intent != IntentPackageSelection {
		t.Fatalf("intent = %q, want %q", analysis.Intent, IntentPackageSelection)
	}
	if analysis.SelectedLevel != 3 {
		t.Fatalf("selected level = %d, want 3", analysis.SelectedLevel)
	}
}

func TestStandardSelectionMovesToBriefRequested(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})

	sendText(t, service, "chat-standard", "Ок стандарт нам надо")

	if len(sender.messages) != 1 {
		t.Fatalf("sent messages = %d, want 1: %#v", len(sender.messages), sender.messages)
	}
	if len(sender.files) != 0 {
		t.Fatalf("sent files = %d, want 0", len(sender.files))
	}
	got := sender.messages[0]
	if !strings.Contains(got, "берём стандарт / премиум формат") || !strings.Contains(got, "Что рекламируем") {
		t.Fatalf("unexpected package brief: %q", got)
	}

	conversation, err := store.Snapshot(context.Background(), "chat-standard")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if conversation.Stage != StageBriefRequested {
		t.Fatalf("stage = %q, want %q", conversation.Stage, StageBriefRequested)
	}
	if conversation.SelectedLevel != 3 || conversation.Lead.SelectedPackage != "standard" {
		t.Fatalf("package state = level %d package %q, want standard", conversation.SelectedLevel, conversation.Lead.SelectedPackage)
	}
	if !conversation.Lead.BriefRequested {
		t.Fatal("brief requested flag = false, want true")
	}
}

func TestTestPackageSelectionSendsBriefAndSelectedVideo(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))

	sendText(t, service, "chat-test-package", "Берём test")

	conversation, err := store.Snapshot(context.Background(), "chat-test-package")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if conversation.SelectedLevel != 1 || conversation.Lead.SelectedPackage != "test" {
		t.Fatalf("package state = level %d package %q, want test", conversation.SelectedLevel, conversation.Lead.SelectedPackage)
	}
	if conversation.Lead.LeadStatus != LeadStatusHot {
		t.Fatalf("lead status = %q, want %q", conversation.Lead.LeadStatus, LeadStatusHot)
	}
	if conversation.Stage != StageBriefRequested {
		t.Fatalf("stage = %q, want %q", conversation.Stage, StageBriefRequested)
	}
	if len(sender.messages) != 1 || !strings.Contains(sender.messages[0], "Короткий бриф") {
		t.Fatalf("unexpected brief message: %#v", sender.messages)
	}
	if len(sender.files) != 1 || filepath.Base(sender.files[0]) != VideoLevel1 {
		t.Fatalf("sent files = %#v, want %s", sender.files, VideoLevel1)
	}
}

func TestSelectedPackageDoesNotTriggerPackageOptionsAgain(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})

	sendText(t, service, "chat-no-repeat", "Ок стандарт нам надо")
	sendText(t, service, "chat-no-repeat", "Ок")

	if len(sender.messages) != 2 {
		t.Fatalf("sent messages = %d, want 2: %#v", len(sender.messages), sender.messages)
	}
	last := sender.messages[len(sender.messages)-1]
	if strings.Contains(last, "35 000") || strings.Contains(last, "50 000") || strings.Contains(last, "75 000") {
		t.Fatalf("bot repeated package options after selection: %q", last)
	}
	if !strings.Contains(last, "короткий бриф") {
		t.Fatalf("unexpected follow-up after brief request: %q", last)
	}
}

func TestReturningClientWithSelectedPackageGetsBriefContinuation(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})
	chatID := "chat-returning-brief"

	if err := store.UpdateLanguage(context.Background(), chatID, "ru"); err != nil {
		t.Fatalf("UpdateLanguage() error = %v", err)
	}
	if err := store.UpdateLead(context.Background(), chatID, LeadState{
		HasBeenGreeted:  true,
		SelectedPackage: "standard",
		PortfolioSent:   true,
		OfferSent:       true,
		BriefRequested:  true,
		LeadStatus:      LeadStatusHot,
	}); err != nil {
		t.Fatalf("UpdateLead() error = %v", err)
	}
	if err := store.UpdateState(context.Background(), chatID, StageBriefRequested, 3); err != nil {
		t.Fatalf("UpdateState() error = %v", err)
	}

	sendText(t, service, chatID, "Здравствуйте")

	if len(sender.messages) != 1 {
		t.Fatalf("sent messages = %d, want 1: %#v", len(sender.messages), sender.messages)
	}
	got := sender.messages[0]
	if !strings.Contains(got, "продолжить по стандарт") || !strings.Contains(got, "короткий бриф") {
		t.Fatalf("unexpected returning reply: %q", got)
	}
	if strings.Contains(got, "35 000") || strings.Contains(got, "нишу") || strings.Contains(got, "Портфолио") {
		t.Fatalf("returning reply repeated sales script: %q", got)
	}
}

func TestOfferSentStandardPriceQuestionAnswersOnlyStandard(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})
	chatID := "chat-standard-price"

	if err := store.UpdateLanguage(context.Background(), chatID, "ru"); err != nil {
		t.Fatalf("UpdateLanguage() error = %v", err)
	}
	if err := store.UpdateLead(context.Background(), chatID, LeadState{
		HasBeenGreeted: true,
		PortfolioSent:  true,
		OfferSent:      true,
		LeadStatus:     LeadStatusWarm,
	}); err != nil {
		t.Fatalf("UpdateLead() error = %v", err)
	}
	if err := store.UpdateState(context.Background(), chatID, StagePackageSuggested, 0); err != nil {
		t.Fatalf("UpdateState() error = %v", err)
	}

	sendText(t, service, chatID, "А стандарт сколько стоит?")

	if len(sender.messages) != 1 {
		t.Fatalf("sent messages = %d, want 1: %#v", len(sender.messages), sender.messages)
	}
	got := sender.messages[0]
	if got != "Стандарт / премиум формат — от 75 000 тг. Он подходит, если нужен сильный ролик под рекламу и масштабирование." {
		t.Fatalf("unexpected standard price reply: %q", got)
	}
	if strings.Contains(got, "тестовый") || strings.Contains(got, "базовый") || strings.Contains(got, "Портфолио") {
		t.Fatalf("price reply repeated full offer: %q", got)
	}
}

func TestPackageQuestionDoesNotSelectPackageOrRestartFunnel(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})
	chatID := "chat-basic-question"

	if err := store.UpdateLanguage(context.Background(), chatID, "ru"); err != nil {
		t.Fatalf("UpdateLanguage() error = %v", err)
	}
	if err := store.UpdateLead(context.Background(), chatID, LeadState{
		HasBeenGreeted: true,
		PortfolioSent:  true,
		OfferSent:      true,
		LeadStatus:     LeadStatusWarm,
	}); err != nil {
		t.Fatalf("UpdateLead() error = %v", err)
	}
	if err := store.UpdateState(context.Background(), chatID, StagePackageSuggested, 0); err != nil {
		t.Fatalf("UpdateState() error = %v", err)
	}

	sendText(t, service, chatID, "А basic чем отличается?")

	conversation, err := store.Snapshot(context.Background(), chatID)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if conversation.SelectedLevel != 0 || conversation.Lead.SelectedPackage != "" {
		t.Fatalf("package was selected: level=%d package=%q", conversation.SelectedLevel, conversation.Lead.SelectedPackage)
	}
	got := sender.messages[len(sender.messages)-1]
	if !strings.Contains(got, "Basic за 50 000 тг") || strings.Contains(got, "какая у вас ниша") {
		t.Fatalf("unexpected package question reply: %q", got)
	}
}

func TestPortfolioRequestDoesNotSelectPackage(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{StandardURL: "https://example.com/standard"})

	sendText(t, service, "chat-portfolio-not-selected", "покажите стандарт портфолио")

	conversation, err := store.Snapshot(context.Background(), "chat-portfolio-not-selected")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !conversation.Lead.PortfolioSent {
		t.Fatal("portfolio sent flag = false, want true")
	}
	if conversation.SelectedLevel != 0 || conversation.Lead.SelectedPackage != "" {
		t.Fatalf("portfolio request selected package: level=%d package=%q", conversation.SelectedLevel, conversation.Lead.SelectedPackage)
	}
	if conversation.Lead.LeadStatus != LeadStatusWarm {
		t.Fatalf("lead status = %q, want %q", conversation.Lead.LeadStatus, LeadStatusWarm)
	}
}

func TestPortfolioRequestTwiceDoesNotRepeatVideo(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))

	sendText(t, service, "chat-portfolio-repeat", "Можно примеры?")
	sendText(t, service, "chat-portfolio-repeat", "Пришлите примеры")

	if len(sender.files) != 1 {
		t.Fatalf("sent files = %#v, want one video", sender.files)
	}
	if last := sender.messages[len(sender.messages)-1]; !strings.Contains(last, "Пример уже отправлял") {
		t.Fatalf("unexpected repeat portfolio reply: %q", last)
	}
}

func TestBriefAnswerIsCollectedAfterPackageSelection(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})

	sendText(t, service, "chat-brief", "Стандарт")
	sendText(t, service, "chat-brief", "Продаём мебель, цель заявки, аудитория семьи, instagram @stone")

	conversation, err := store.Snapshot(context.Background(), "chat-brief")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if conversation.Stage != StageHandoffRequired {
		t.Fatalf("stage = %q, want %q", conversation.Stage, StageHandoffRequired)
	}
	if !conversation.Lead.ContactBriefReady {
		t.Fatal("contact brief ready = false, want true")
	}
	if !conversation.Lead.BriefCompleted {
		t.Fatal("brief completed = false, want true")
	}
	if conversation.Lead.LeadStatus != LeadStatusHandoffRequired {
		t.Fatalf("lead status = %q, want %q", conversation.Lead.LeadStatus, LeadStatusHandoffRequired)
	}
	if last := sender.messages[len(sender.messages)-1]; strings.Contains(last, "35 000") || !strings.Contains(last, "Принял") {
		t.Fatalf("unexpected brief collected reply: %q", last)
	}
}

func TestCanceledContextDoesNotSend(t *testing.T) {
	sender := &fakeSender{}
	service := newTestService(sender, NewConversationStore(), PortfolioLinks{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := service.HandleIncomingMessage(ctx, IncomingMessage{ChatID: "chat-cancel", Text: "Здравствуйте"})
	if err == nil {
		t.Fatal("HandleIncomingMessage() error = nil, want context cancellation")
	}
	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("sent after cancellation: messages=%#v files=%#v", sender.messages, sender.files)
	}
}
