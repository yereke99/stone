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
	if countMessagesContaining(sender.messages, "Выберите подходящий формат:") != 1 {
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

func TestPackageSelectionSendsSelectedVideoWithCaptionWhenNotAlreadySent(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{
		TestURL:     "https://example.com/test",
		BasicURL:    "https://example.com/basic",
		StandardURL: "https://example.com/standard",
	}, testVideoDir(t), "77019519013@c.us")
	chatID := "77015550000@c.us"

	sendText(t, service, chatID, "Здравствуйте")
	sendText(t, service, chatID, "мебель, цель продажи, срок через неделю")
	if len(sender.files) != 0 {
		t.Fatalf("portfolio links should not upload videos yet: %#v", sender.files)
	}

	sendText(t, service, chatID, "берём basic")

	if len(sender.files) != 1 || filepath.Base(sender.files[0]) != VideoLevel2 {
		t.Fatalf("sent files = %#v, want selected basic video", sender.files)
	}
	if len(sender.captions) != 1 || !strings.Contains(sender.captions[0], "Базовый формат") || !strings.Contains(sender.captions[0], "50 000 тг") {
		t.Fatalf("unexpected selected video caption: %#v", sender.captions)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateHandedOff || !conversation.Stopped || !conversation.HandedOffToOwner || conversation.TransferredAt.IsZero() {
		t.Fatalf("handoff state not set after package selection: stage=%q stopped=%v handed=%v transferred=%v", conversation.Stage, conversation.Stopped, conversation.HandedOffToOwner, conversation.TransferredAt)
	}
	adminMessage := sender.messages[len(sender.messages)-1]
	if !strings.Contains(adminMessage, "New qualified WhatsApp lead") ||
		!strings.Contains(adminMessage, "Interested package: Basic / Базовый") ||
		strings.Contains(adminMessage, "\nNiche: -") ||
		strings.Contains(adminMessage, "\nGoal: -") {
		t.Fatalf("unexpected admin summary:\n%s", adminMessage)
	}
}

func TestDavaiteWithMissingFieldsAsksOnlyMissingFields(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "77010000000@c.us"

	store.Update(chatID, func(conversation *Conversation) {
		conversation.DisplayName = "Yerek"
		conversation.Stage = ClientStateAwaitingQualification
		conversation.Lead.Deadline = "в течение месяца"
	})

	sendText(t, service, chatID, "да давайте открывайте анкету")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.HandedOffToOwner || conversation.Stopped || conversation.QuestionnaireSent {
		t.Fatalf("lead was transferred too early: stage=%q stopped=%v handed=%v questionnaire=%v", conversation.Stage, conversation.Stopped, conversation.HandedOffToOwner, conversation.QuestionnaireSent)
	}
	if !conversation.WantsQuestionnaire || !conversation.Lead.WantsQuestionnaire {
		t.Fatalf("questionnaire intent was not saved: conversation=%v lead=%v", conversation.WantsQuestionnaire, conversation.Lead.WantsQuestionnaire)
	}
	if !sameFields(conversation.MissingFields, []string{fieldNiche, fieldGoal, fieldPackageInterest}) {
		t.Fatalf("missing fields = %#v, want niche/goal/package_interest", conversation.MissingFields)
	}
	if len(sender.chatIDs) > 0 && sender.chatIDs[len(sender.chatIDs)-1] == "77019519013@c.us" {
		t.Fatalf("admin was notified for incomplete lead: %#v", sender.messages)
	}
	last := sender.messages[len(sender.messages)-1]
	for _, want := range []string{"нишу", "цель", "пакет"} {
		if !strings.Contains(last, want) {
			t.Fatalf("missing-field reply %q does not mention %q", last, want)
		}
	}
	if strings.Contains(last, "срок") {
		t.Fatalf("reply asked for already saved deadline: %q", last)
	}
}

func TestBriefAnswerWithIncompleteLeadDoesNotCloseAutomation(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "77011110000@c.us"

	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = StageBriefRequested
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.Lead.BriefRequested = true
		conversation.Lead.Niche = "салон красоты"
		conversation.Lead.Deadline = "на этой неделе"
	})

	sendText(t, service, chatID, "аудитория женщины, оффер скидка, сайт example.kz")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage == ClientStateHandedOff || conversation.HandedOffToOwner || conversation.AutomationClosed || conversation.Stopped {
		t.Fatalf("incomplete brief answer closed automation: stage=%q handed=%v closed=%v stopped=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.Stopped)
	}
	if normalizeLeadStatus(conversation.LeadStatus) == LeadStatusHandoffRequired || normalizeLeadStatus(conversation.Lead.LeadStatus) == LeadStatusHandoffRequired {
		t.Fatalf("incomplete brief answer marked handoff required: conversation=%q lead=%q", conversation.LeadStatus, conversation.Lead.LeadStatus)
	}
	if conversation.CompletedFields[fieldBrief] {
		t.Fatalf("brief was marked completed before lead qualification: completed=%#v", conversation.CompletedFields)
	}
	if !sameFields(conversation.MissingFields, []string{fieldGoal, fieldPackageInterest}) {
		t.Fatalf("missing fields = %#v, want goal/package_interest", conversation.MissingFields)
	}
	last := sender.messages[len(sender.messages)-1]
	for _, want := range []string{"цель", "пакет"} {
		if !strings.Contains(last, want) {
			t.Fatalf("missing-field reply %q does not mention %q", last, want)
		}
	}
}

func TestIncompleteLeadCannotBeForcedIntoHandoffState(t *testing.T) {
	store := NewConversationStore()
	chatID := "77011112222@c.us"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Lead.Niche = "салон красоты"
		conversation.Lead.Deadline = "на этой неделе"
	})

	if err := store.UpdateState(context.Background(), chatID, ClientStateHandedOff, 0); err != nil {
		t.Fatalf("UpdateState() error = %v", err)
	}

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage == ClientStateHandedOff || conversation.HandedOffToOwner || conversation.AutomationClosed || conversation.Stopped {
		t.Fatalf("incomplete lead was forced into handoff: stage=%q handed=%v closed=%v stopped=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.Stopped)
	}
	if normalizeLeadStatus(conversation.LeadStatus) == LeadStatusHandoffRequired || normalizeLeadStatus(conversation.Lead.LeadStatus) == LeadStatusHandoffRequired {
		t.Fatalf("incomplete lead marked handoff required: conversation=%q lead=%q", conversation.LeadStatus, conversation.Lead.LeadStatus)
	}
	if !sameFields(conversation.MissingFields, []string{fieldGoal, fieldPackageInterest}) {
		t.Fatalf("missing fields = %#v, want goal/package_interest", conversation.MissingFields)
	}
}

func TestInvalidPersistedHandoffIsReopenedForQualification(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "77011113333@c.us"

	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateHandedOff
		conversation.HandedOffToOwner = true
		conversation.AutomationClosed = true
		conversation.Stopped = true
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.Lead.BriefRequested = true
		conversation.Lead.BriefCompleted = true
		conversation.Lead.ContactBriefReady = true
		conversation.Lead.Niche = "салон красоты"
		conversation.Lead.Deadline = "на этой неделе"
		conversation.Lead.LeadStatus = LeadStatusHandoffRequired
		conversation.LeadStatus = LeadStatusHandoffRequired
	})

	sendText(t, service, chatID, "алло")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage == ClientStateHandedOff || conversation.HandedOffToOwner || conversation.AutomationClosed || conversation.Stopped {
		t.Fatalf("invalid handoff was not reopened: stage=%q handed=%v closed=%v stopped=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.Stopped)
	}
	if len(sender.messages) == 0 {
		t.Fatal("reopened incomplete handoff did not produce a qualification reply")
	}
	if !sameFields(conversation.MissingFields, []string{fieldGoal, fieldPackageInterest}) {
		t.Fatalf("missing fields = %#v, want goal/package_interest", conversation.MissingFields)
	}
	last := sender.messages[len(sender.messages)-1]
	for _, want := range []string{"цель", "пакет"} {
		if !strings.Contains(last, want) {
			t.Fatalf("missing-field reply %q does not mention %q", last, want)
		}
	}
}

func TestOneLetterNicheIsRejected(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "77012220000@c.us"

	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQualification
		conversation.Lead.Goal = "получать заявки"
		conversation.Lead.Deadline = "на этой неделе"
	})

	sendText(t, service, chatID, "м")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "" {
		t.Fatalf("one-letter niche was saved: %#v", conversation.Lead.Niche)
	}
	if conversation.HandedOffToOwner || conversation.Stopped {
		t.Fatalf("invalid niche caused handoff: stage=%q stopped=%v handed=%v", conversation.Stage, conversation.Stopped, conversation.HandedOffToOwner)
	}
	last := sender.messages[len(sender.messages)-1]
	if !strings.Contains(last, "нишу") {
		t.Fatalf("bot did not clarify invalid niche: %q", last)
	}
}

func TestQualifiedLeadNotifiesAdminOnceAndClosesAutomation(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "77013330000@c.us"
	text := "У меня салон красоты, хочу ролики для инстаграма, клиентов көбейту керек, бір айдың ішінде керек, берем premium"

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		IDMessage:  "green-qualified-1",
		ChatID:     chatID,
		SenderName: "Yerek",
		Text:       text,
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateHandedOff || !conversation.Stopped || !conversation.HandedOffToOwner || !conversation.AutomationClosed || conversation.TransferredAt.IsZero() {
		t.Fatalf("qualified lead was not closed: stage=%q stopped=%v handed=%v closed=%v transferred=%v", conversation.Stage, conversation.Stopped, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.TransferredAt)
	}
	if conversation.Lead.Niche != "салон красоты" || conversation.Lead.Goal != "привлечь клиентов" || conversation.Lead.Deadline != "в течение месяца" || conversation.Lead.SelectedPackage != "standard" {
		t.Fatalf("lead fields = %#v", conversation.Lead)
	}
	if got := countMessagesContaining(sender.messages, "New qualified WhatsApp lead"); got != 1 {
		t.Fatalf("admin notifications = %d, want 1: %#v", got, sender.messages)
	}
	adminMessage := sender.messages[len(sender.messages)-1]
	for _, want := range []string{
		"Name: Yerek",
		"Phone: +7 701 333 00 00",
		"ChatID: " + chatID,
		"Niche: салон красоты",
		"Goal: привлечь клиентов",
		"Deadline: в течение месяца",
		"Interested package: Standard / Стандарт",
		"Client intent: wants to open questionnaire / ready to proceed",
		"Status: qualified, transferred to manager",
	} {
		if !strings.Contains(adminMessage, want) {
			t.Fatalf("admin message missing %q:\n%s", want, adminMessage)
		}
	}
	if strings.Contains(adminMessage, "-") && (strings.Contains(adminMessage, "Niche: -") || strings.Contains(adminMessage, "Goal: -") || strings.Contains(adminMessage, "Interested package: -")) {
		t.Fatalf("admin message contains placeholder:\n%s", adminMessage)
	}

	before := len(sender.messages)
	postHandoffText := "а сколько будет стоить?"
	sendText(t, service, chatID, postHandoffText)
	if len(sender.messages) != before {
		t.Fatalf("bot replied after handoff: %#v", sender.messages[before:])
	}
	afterHandoff := snapshotConversation(t, store, chatID)
	if afterHandoff.LastIncomingText != postHandoffText {
		t.Fatalf("post-handoff incoming was not saved: last=%q", afterHandoff.LastIncomingText)
	}
}

func TestDuplicateIncomingMessageIsProcessedOnce(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "77014440000@c.us"
	msg := IncomingMessage{
		IDMessage:  "green-duplicate-1",
		ChatID:     chatID,
		SenderName: "Yerek",
		Text:       "салон красоты, цель заявки, срок через неделю, берем standard",
	}

	if err := service.HandleIncomingMessage(context.Background(), msg); err != nil {
		t.Fatalf("first HandleIncomingMessage() error = %v", err)
	}
	if err := service.HandleIncomingMessage(context.Background(), msg); err != nil {
		t.Fatalf("duplicate HandleIncomingMessage() error = %v", err)
	}

	if got := countMessagesContaining(sender.messages, "New qualified WhatsApp lead"); got != 1 {
		t.Fatalf("admin notifications = %d, want 1: %#v", got, sender.messages)
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
	if conversation.HandedOffToOwner || conversation.Stopped {
		t.Fatalf("state after restart = %q stopped=%v handed=%v, want collecting missing package", conversation.Stage, conversation.Stopped, conversation.HandedOffToOwner)
	}
	if !sameFields(conversation.MissingFields, []string{fieldPackageInterest}) {
		t.Fatalf("missing fields after restart = %#v, want package_interest", conversation.MissingFields)
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

	if countMessagesContaining(sender.messages, "Выберите подходящий формат:") != 1 {
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
