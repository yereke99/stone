package bot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestService(sender *fakeSender, store *ConversationStore, links PortfolioLinks) *Service {
	return NewService(sender, &fakeAI{}, store, "./video", links, "auto", nil)
}

func newTestServiceWithVideoDir(sender *fakeSender, store *ConversationStore, links PortfolioLinks, videoDir string, admins ...string) *Service {
	return NewService(sender, &fakeAI{}, store, videoDir, links, "auto", nil, admins...)
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

func snapshotConversation(t *testing.T, store *ConversationStore, chatID string) Conversation {
	t.Helper()
	conversation, err := store.Snapshot(context.Background(), chatID)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	return conversation
}

func TestServiceConstructionSendsNothing(t *testing.T) {
	sender := &fakeSender{}
	_ = newTestService(sender, NewConversationStore(), PortfolioLinks{})

	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("service construction sent outbound content: messages=%#v files=%#v", sender.messages, sender.files)
	}
}

func TestNewClientFirstIncomingSendsOpeningOnce(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})

	sendText(t, service, "chat-new", "Здравствуйте")

	if len(sender.messages) != 1 {
		t.Fatalf("sent messages = %d, want 1: %#v", len(sender.messages), sender.messages)
	}
	if got := sender.messages[0]; got != QualificationGreetingText("ru") {
		t.Fatalf("unexpected opening:\n%s", got)
	}
	conversation := snapshotConversation(t, store, "chat-new")
	if conversation.Stage != ClientStateAwaitingQualification || !conversation.InitialMessageSent {
		t.Fatalf("state = %q initial=%v, want awaiting qualification with opening sent", conversation.Stage, conversation.InitialMessageSent)
	}
}

func TestQualificationReplySendsPortfolioAndPackagesOnce(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-qualified"

	sendText(t, service, chatID, "Здравствуйте")
	sendText(t, service, chatID, "у меня салон красоты, надо заявки в инсту на этой неделе")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStatePackagesPresented {
		t.Fatalf("state = %q, want packages_presented", conversation.Stage)
	}
	if !conversation.SentPortfolio || !conversation.PackagesSent || !conversation.Lead.PortfolioSent || !conversation.Lead.OfferSent {
		t.Fatalf("portfolio/packages flags not set: %#v", conversation)
	}
	if conversation.Lead.Niche != "салон красоты" || conversation.Lead.Goal != "получать заявки" || conversation.Lead.Deadline != "на этой неделе" {
		t.Fatalf("lead = %#v, want extracted niche/goal/deadline", conversation.Lead)
	}
	if countMessagesContaining(sender.messages, "Пакеты:") != 1 {
		t.Fatalf("package options were not sent exactly once: %#v", sender.messages)
	}
	if len(sender.files) != 3 {
		t.Fatalf("sent files = %#v, want all package examples", sender.files)
	}
}

func TestDuplicateExamplesAreNotSpammed(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-examples"

	sendText(t, service, chatID, "Здравствуйте")
	sendText(t, service, chatID, "салон красоты, цель заявки, сроки на этой неделе")
	initialFiles := len(sender.files)

	sendText(t, service, chatID, "скиньте примеры")
	sendText(t, service, chatID, "можно еще примеры?")

	if len(sender.files) != initialFiles {
		t.Fatalf("examples repeated: files before=%d after=%d %#v", initialFiles, len(sender.files), sender.files)
	}
	if last := sender.messages[len(sender.messages)-1]; !strings.Contains(last, "Пример уже отправлял") {
		t.Fatalf("unexpected repeat examples reply: %q", last)
	}
}

func TestDavaiteSendsQuestionnaireNotifiesAdminAndStops(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "77010000000@c.us"

	sendText(t, service, chatID, "Здравствуйте")
	sendText(t, service, chatID, "салон красоты, цель заявки, сроки на этой неделе")
	sendText(t, service, chatID, "давайте")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateHandedOff || !conversation.Stopped || !conversation.HandedOffToOwner || !conversation.QuestionnaireSent {
		t.Fatalf("handoff state not set: stage=%q stopped=%v handed=%v questionnaire=%v", conversation.Stage, conversation.Stopped, conversation.HandedOffToOwner, conversation.QuestionnaireSent)
	}
	if len(sender.chatIDs) == 0 || sender.chatIDs[len(sender.chatIDs)-1] != "77019519013@c.us" {
		t.Fatalf("last outbound chat = %q, want admin notification", sender.chatIDs[len(sender.chatIDs)-1])
	}
	adminMessage := sender.messages[len(sender.messages)-1]
	if !strings.Contains(adminMessage, "Новый горячий лид из WhatsApp") ||
		!strings.Contains(adminMessage, "ChatID: "+chatID) ||
		!strings.Contains(adminMessage, "Статус: передан менеджеру") {
		t.Fatalf("unexpected admin summary:\n%s", adminMessage)
	}

	before := len(sender.messages)
	sendText(t, service, chatID, "и еще вопрос")
	if len(sender.messages) != before {
		t.Fatalf("bot replied after handoff: %#v", sender.messages[before:])
	}
}

func TestSQLiteRestartContinuesSavedState(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stone.sqlite3")
	chatID := "77020000000@c.us"

	store1, err := NewSQLiteConversationStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore() error = %v", err)
	}
	sender1 := &fakeSender{}
	service1 := newTestServiceWithVideoDir(sender1, store1, PortfolioLinks{}, testVideoDir(t))
	sendText(t, service1, chatID, "Здравствуйте")
	sendText(t, service1, chatID, "мебель, цель продажи, срок через неделю")
	if err := store1.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	store2, err := NewSQLiteConversationStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore() after restart error = %v", err)
	}
	defer func() {
		_ = store2.Close()
	}()
	sender2 := &fakeSender{}
	service2 := newTestServiceWithVideoDir(sender2, store2, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")

	sendText(t, service2, chatID, "давайте")

	if countMessagesContaining(sender2.messages, "Спасибо за обращение") != 0 {
		t.Fatalf("opening was resent after restart: %#v", sender2.messages)
	}
	conversation := snapshotConversation(t, store2, chatID)
	if conversation.Stage != ClientStateHandedOff || !conversation.Stopped {
		t.Fatalf("state after restart = %q stopped=%v, want handed_off/stopped", conversation.Stage, conversation.Stopped)
	}
}

func TestSQLiteMessageDedupeSurvivesRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stone.sqlite3")
	log := WhatsAppMessageLog{
		ChatID:            "77025500000@c.us",
		GreenAPIMessageID: "green-msg-1",
		DedupeKey:         "77025500000@c.us|green-msg-1",
		Direction:         "incoming",
		MessageType:       "textMessage",
		Text:              "Здравствуйте",
	}

	store1, err := NewSQLiteConversationStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore() error = %v", err)
	}
	decision, err := store1.BeginIncomingMessageProcessing(context.Background(), log)
	if err != nil {
		t.Fatalf("BeginIncomingMessageProcessing() error = %v", err)
	}
	if decision != MessageDedupeNew {
		t.Fatalf("decision = %q, want new", decision)
	}
	if err := store1.FinishMessageProcessing(context.Background(), log.DedupeKey, true); err != nil {
		t.Fatalf("FinishMessageProcessing() error = %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	store2, err := NewSQLiteConversationStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore() after restart error = %v", err)
	}
	defer func() {
		_ = store2.Close()
	}()
	decision, err = store2.BeginIncomingMessageProcessing(context.Background(), log)
	if err != nil {
		t.Fatalf("BeginIncomingMessageProcessing() after restart error = %v", err)
	}
	if decision != MessageDedupeDuplicate {
		t.Fatalf("decision after restart = %q, want duplicate", decision)
	}
}

func TestExistingProcessedClientContinuesFromSavedState(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})
	chatID := "chat-existing"

	sendText(t, service, chatID, "Здравствуйте")
	sendText(t, service, chatID, "мебель, цель продажи, срок через неделю")
	sendText(t, service, chatID, "сколько стоит стандарт?")

	if countMessagesContaining(sender.messages, "Спасибо за обращение") != 1 {
		t.Fatalf("opening count = %d, want 1: %#v", countMessagesContaining(sender.messages, "Спасибо за обращение"), sender.messages)
	}
	last := sender.messages[len(sender.messages)-1]
	if strings.Contains(last, "Спасибо за обращение") || !strings.Contains(last, "75 000") {
		t.Fatalf("existing client did not continue from packages state: %q", last)
	}
}

func TestOptOutStopsMarketingReplies(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestService(sender, store, PortfolioLinks{})
	chatID := "chat-optout"

	sendText(t, service, chatID, "Здравствуйте")
	sendText(t, service, chatID, "не интересно")
	sendText(t, service, chatID, "алло")

	if len(sender.messages) != 1 {
		t.Fatalf("sent messages after opt-out = %#v, want only opening", sender.messages)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateOptOut || !conversation.OptOut || !conversation.Stopped {
		t.Fatalf("opt-out state = stage=%q optout=%v stopped=%v", conversation.Stage, conversation.OptOut, conversation.Stopped)
	}
}

func TestMultipleQuickMessagesDoNotDuplicateBotResponses(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-quick"

	sendText(t, service, chatID, "Здравствуйте")

	var wg sync.WaitGroup
	for _, text := range []string{
		"салон красоты, цель заявки, срок на этой неделе",
		"салон красоты, цель заявки, срок на этой неделе",
	} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: chatID, Text: value}); err != nil {
				t.Errorf("HandleIncomingMessage() error = %v", err)
			}
		}(text)
	}
	wg.Wait()

	if countMessagesContaining(sender.messages, "Пакеты:") != 1 {
		t.Fatalf("package responses duplicated: %#v", sender.messages)
	}
}

func countMessagesContaining(messages []string, needle string) int {
	count := 0
	for _, message := range messages {
		if strings.Contains(message, needle) {
			count++
		}
	}
	return count
}
