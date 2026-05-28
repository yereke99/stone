package bot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

func seedPresentedPackageMessages(store *ConversationStore, chatID string) {
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Language = "ru"
		conversation.Stage = ClientStatePackagesPresented
		conversation.InitialMessageSent = true
		conversation.SentPortfolio = true
		conversation.PackagesSent = true
		conversation.Lead.PortfolioSent = true
		conversation.Lead.OfferSent = true
		conversation.SentVideoFiles[VideoLevel1] = time.Now().UTC()
		conversation.SentVideoFiles[VideoLevel2] = time.Now().UTC()
		conversation.SentVideoFiles[VideoLevel3] = time.Now().UTC()
		conversation.SentVideos[1] = true
		conversation.SentVideos[2] = true
		conversation.SentVideos[3] = true
		conversation.OutgoingPackageMessages["test-video-id"] = OutgoingPackageMessage{
			PackageKey: "test",
			FileName:   VideoLevel1,
			Caption:    OfferCaptionByVideo(VideoLevel1, "ru"),
			SentAt:     time.Now().UTC(),
		}
		conversation.OutgoingPackageMessages["basic-video-id"] = OutgoingPackageMessage{
			PackageKey: "basic",
			FileName:   VideoLevel2,
			Caption:    OfferCaptionByVideo(VideoLevel2, "ru"),
			SentAt:     time.Now().UTC(),
		}
		conversation.OutgoingPackageMessages["standard-video-id"] = OutgoingPackageMessage{
			PackageKey: "standard",
			FileName:   VideoLevel3,
			Caption:    OfferCaptionByVideo(VideoLevel3, "ru"),
			SentAt:     time.Now().UTC(),
		}
	})
}

func seedPresentedQualifiedLead(store *ConversationStore, chatID string) {
	seedPresentedPackageMessages(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Lead.Niche = "салон красоты"
		conversation.Lead.Goal = "получать заявки"
		conversation.Lead.Deadline = "в течение месяца"
	})
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
	sender := &fakeSender{fileMessageIDs: []string{"test-video-id", "basic-video-id", "standard-video-id"}}
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
	if countMessagesContaining(sender.messages, "Выберите подходящий формат:") != 0 {
		t.Fatalf("standalone package options should not be sent: %#v", sender.messages)
	}
	if len(sender.files) != 3 {
		t.Fatalf("sent files = %#v, want all package examples", sender.files)
	}
	for _, tt := range []struct {
		messageID string
		want      string
	}{
		{messageID: "test-video-id", want: "test"},
		{messageID: "basic-video-id", want: "basic"},
		{messageID: "standard-video-id", want: "standard"},
	} {
		metadata, ok := conversation.OutgoingPackageMessages[tt.messageID]
		if !ok || metadata.PackageKey != tt.want {
			t.Fatalf("outgoing package metadata[%q] = %#v, ok=%v, want %q", tt.messageID, metadata, ok, tt.want)
		}
	}
}

func TestReplyToPackageVideoSelectsPackageByQuotedID(t *testing.T) {
	tests := []struct {
		name      string
		quotedID  string
		replyText string
		want      string
	}{
		{name: "test", quotedID: "test-video-id", replyText: "Вот этот", want: "test"},
		{name: "basic", quotedID: "basic-video-id", replyText: "Этот", want: "basic"},
		{name: "standard", quotedID: "standard-video-id", replyText: ".", want: "standard"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakeSender{}
			store := NewConversationStore()
			service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
			chatID := "chat-reply-id-" + tt.name
			seedPresentedQualifiedLead(store, chatID)

			if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
				IDMessage:       "reply-" + tt.name,
				ChatID:          chatID,
				Text:            tt.replyText,
				QuotedMessageID: tt.quotedID,
				QuotedType:      "videoMessage",
			}); err != nil {
				t.Fatalf("HandleIncomingMessage() error = %v", err)
			}

			conversation := snapshotConversation(t, store, chatID)
			if conversation.Lead.SelectedPackage != tt.want {
				t.Fatalf("selected package = %q, want %q", conversation.Lead.SelectedPackage, tt.want)
			}
			if !conversation.CompletedFields[fieldPackageInterest] {
				t.Fatalf("package field was not completed: %#v", conversation.CompletedFields)
			}
			if conversation.Stage != ClientStatePackagesPresented || conversation.HandedOffToOwner || conversation.AutomationClosed || conversation.Stopped {
				t.Fatalf("unexpected state after reply selection: stage=%q handed=%v closed=%v stopped=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.Stopped)
			}
			if got := countMessagesContaining(sender.messages, "Короткий бриф"); got != 0 {
				t.Fatalf("brief was opened immediately after package selection: %d messages=%#v", got, sender.messages)
			}
			if got := countMessagesContaining(sender.messages, "формат выбрали"); got != 1 {
				t.Fatalf("package selection acknowledgements = %d, want 1: %#v", got, sender.messages)
			}
			if len(sender.files) != 0 {
				t.Fatalf("selected package video was resent: %#v", sender.files)
			}
		})
	}
}

func TestReplyToPackageVideoSelectsPackageFromQuotedCaption(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-reply-caption"
	seedPresentedQualifiedLead(store, chatID)

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		IDMessage:      "reply-caption-standard",
		ChatID:         chatID,
		Text:           ".",
		QuotedCaption:  OfferCaptionByVideo(VideoLevel3, "ru"),
		QuotedType:     "videoMessage",
		QuotedFileName: "",
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.SelectedPackage != "standard" {
		t.Fatalf("selected package = %q, want standard", conversation.Lead.SelectedPackage)
	}
	if got := countMessagesContaining(sender.messages, "Короткий бриф"); got != 0 {
		t.Fatalf("brief was opened immediately after package selection: %d messages=%#v", got, sender.messages)
	}
	if got := countMessagesContaining(sender.messages, "формат выбрали"); got != 1 {
		t.Fatalf("package selection acknowledgements = %d, want 1: %#v", got, sender.messages)
	}
}

func TestPackageReplyWithMissingFieldsAsksOnlyMissingFields(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-reply-incomplete"
	seedPresentedPackageMessages(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Lead.Deadline = "в течение месяца"
	})

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		IDMessage:       "reply-incomplete-standard",
		ChatID:          chatID,
		Text:            "Вот этот",
		QuotedMessageID: "standard-video-id",
		QuotedType:      "videoMessage",
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.SelectedPackage != "standard" {
		t.Fatalf("selected package = %q, want standard", conversation.Lead.SelectedPackage)
	}
	if conversation.HandedOffToOwner || conversation.AutomationClosed || conversation.Stopped {
		t.Fatalf("incomplete lead was handed off: stage=%q handed=%v closed=%v stopped=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.Stopped)
	}
	if !sameFields(conversation.MissingFields, []string{fieldNiche, fieldGoal}) {
		t.Fatalf("missing fields = %#v, want niche/goal", conversation.MissingFields)
	}
	last := sender.messages[len(sender.messages)-1]
	if strings.Contains(last, "пакет") {
		t.Fatalf("reply asked for already selected package: %q", last)
	}
	for _, want := range []string{"нишу", "цель"} {
		if !strings.Contains(last, want) {
			t.Fatalf("missing-field reply %q does not mention %q", last, want)
		}
	}
}

func TestQuestionnaireRequestAfterPackageVideosAsksPackageFirst(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-questionnaire-before-package"
	seedPresentedPackageMessages(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Lead.Niche = "салон красоты"
		conversation.Lead.Goal = "получать заявки"
		conversation.Lead.Deadline = "на этой неделе"
	})

	sendText(t, service, chatID, "давайте анкету")

	conversation := snapshotConversation(t, store, chatID)
	if !sameFields(conversation.MissingFields, []string{fieldPackageInterest}) {
		t.Fatalf("missing fields = %#v, want package_interest", conversation.MissingFields)
	}
	last := sender.messages[len(sender.messages)-1]
	if strings.Contains(last, "анкету откроем") {
		t.Fatalf("questionnaire was opened before package selection: %q", last)
	}
	if !strings.Contains(last, "Какой формат") {
		t.Fatalf("reply did not ask for package format first: %q", last)
	}
	for _, unwanted := range []string{"нишу", "цель"} {
		if strings.Contains(last, unwanted) {
			t.Fatalf("reply asked for %q before package choice: %q", unwanted, last)
		}
	}
}

func TestPendingQuestionnairePackageSelectionDoesNotOpenBriefBeforeQualification(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-pending-questionnaire-package"
	seedPresentedPackageMessages(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.Lead.Deadline = "на этой неделе"
	})

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		IDMessage:       "reply-standard-after-questionnaire",
		ChatID:          chatID,
		Text:            "Вот этот",
		QuotedMessageID: "standard-video-id",
		QuotedType:      "videoMessage",
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.SelectedPackage != "standard" {
		t.Fatalf("selected package = %q, want standard", conversation.Lead.SelectedPackage)
	}
	if conversation.QuestionnaireSent || conversation.Stage == StageBriefRequested {
		t.Fatalf("brief was opened before qualification: stage=%q questionnaire=%v", conversation.Stage, conversation.QuestionnaireSent)
	}
	last := sender.messages[len(sender.messages)-1]
	if strings.Contains(last, "анкету откроем") || strings.Contains(last, "пакет") {
		t.Fatalf("unexpected questionnaire/package prompt after selected package: %q", last)
	}
	for _, want := range []string{"нишу", "цель"} {
		if !strings.Contains(last, want) {
			t.Fatalf("missing-field reply %q does not mention %q", last, want)
		}
	}
}

func TestMissingReplyContextFallsBackToNormalTextAnalysis(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-no-reply-context"
	seedPresentedQualifiedLead(store, chatID)

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		IDMessage: "normal-basic",
		ChatID:    chatID,
		Text:      "берём basic",
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.SelectedPackage != "basic" {
		t.Fatalf("selected package = %q, want basic", conversation.Lead.SelectedPackage)
	}
}

func TestHandedOffReplyContextIsSavedWithoutAutoReply(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-handed-reply"
	seedPresentedQualifiedLead(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateHandedOff
		conversation.HandedOffToOwner = true
		conversation.AutomationClosed = true
		conversation.Stopped = true
		conversation.TransferredAt = time.Now().UTC()
		conversation.QuestionnaireSent = true
		conversation.Lead.SelectedPackage = "standard"
		conversation.Lead.BriefRequested = true
		conversation.Lead.BriefCompleted = true
		conversation.Lead.ContactBriefReady = true
		conversation.Lead.LeadStatus = LeadStatusHandoffRequired
		conversation.LeadStatus = LeadStatusHandoffRequired
	})

	text := "Вот этот"
	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		IDMessage:       "reply-after-handoff",
		ChatID:          chatID,
		Text:            text,
		QuotedMessageID: "standard-video-id",
		QuotedType:      "videoMessage",
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(sender.messages) != 0 {
		t.Fatalf("bot replied after handoff: %#v", sender.messages)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.LastIncomingText != text {
		t.Fatalf("post-handoff incoming was not saved: %q", conversation.LastIncomingText)
	}
}

func TestDuplicateIncomingPackageReplyIsProcessedOnce(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-duplicate-package-reply"
	seedPresentedQualifiedLead(store, chatID)
	msg := IncomingMessage{
		IDMessage:       "reply-duplicate-package",
		ChatID:          chatID,
		Text:            "Вот этот",
		QuotedMessageID: "standard-video-id",
		QuotedType:      "videoMessage",
	}

	if err := service.HandleIncomingMessage(context.Background(), msg); err != nil {
		t.Fatalf("first HandleIncomingMessage() error = %v", err)
	}
	if err := service.HandleIncomingMessage(context.Background(), msg); err != nil {
		t.Fatalf("duplicate HandleIncomingMessage() error = %v", err)
	}

	if got := countMessagesContaining(sender.messages, "Короткий бриф"); got != 0 {
		t.Fatalf("brief was opened immediately after duplicate package reply: %d messages=%#v", got, sender.messages)
	}
	if got := countMessagesContaining(sender.messages, "формат выбрали"); got != 1 {
		t.Fatalf("package selection acknowledgements = %d, want 1: %#v", got, sender.messages)
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
	sendText(t, service, chatID, "ниша мебель, цель получать заявки, срок через неделю")
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
	if conversation.Stage == StageBriefRequested || conversation.Stopped || conversation.HandedOffToOwner || conversation.AutomationClosed {
		t.Fatalf("package selection opened brief too early: stage=%q stopped=%v handed=%v closed=%v", conversation.Stage, conversation.Stopped, conversation.HandedOffToOwner, conversation.AutomationClosed)
	}
	if got := countMessagesContaining(sender.messages, "Новый квалифицированный лид WhatsApp"); got != 0 {
		t.Fatalf("admin was notified before brief answer: %d messages=%#v", got, sender.messages)
	}
	if got := countMessagesContaining(sender.messages, "Короткий бриф"); got != 0 {
		t.Fatalf("brief was sent immediately after selected video: %d messages=%#v", got, sender.messages)
	}

	sendText(t, service, chatID, "анкета")
	sendText(t, service, chatID, "1) рекламируем мебель 2) хотят красивый интерьер 3) скидка на заказ")

	conversation = snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateHandedOff || !conversation.Stopped || !conversation.HandedOffToOwner || conversation.TransferredAt.IsZero() {
		t.Fatalf("handoff state not set after brief answer: stage=%q stopped=%v handed=%v transferred=%v", conversation.Stage, conversation.Stopped, conversation.HandedOffToOwner, conversation.TransferredAt)
	}
	if got := countMessagesContaining(sender.messages, BriefCollectedText("ru")); got != 1 {
		t.Fatalf("brief acknowledgement count = %d, messages=%#v", got, sender.messages)
	}
	adminMessage := sender.messages[len(sender.messages)-1]
	if !strings.Contains(adminMessage, "Новый квалифицированный лид WhatsApp") ||
		!strings.Contains(adminMessage, "Пакет: Basic / Базовый") ||
		strings.Contains(adminMessage, "\nНиша: -") ||
		strings.Contains(adminMessage, "\nЦель: -") {
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

func TestWeakBriefAnswerDoesNotCloseQualifiedLead(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "77011115555@c.us"

	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = StageBriefRequested
		conversation.QuestionnaireSent = true
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.Lead.BriefRequested = true
		conversation.Lead.Niche = "бизнесмены и блогеры"
		conversation.Lead.Goal = "получать заявки"
		conversation.Lead.Deadline = "на этой неделе"
		conversation.Lead.SelectedPackage = "standard"
		conversation.SelectedLevel = 3
	})

	sendText(t, service, chatID, "цель у меня чтобы мои клиенты поняли почему именно я?")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage == ClientStateHandedOff || conversation.HandedOffToOwner || conversation.AutomationClosed || conversation.Stopped {
		t.Fatalf("weak brief answer closed automation: stage=%q handed=%v closed=%v stopped=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.Stopped)
	}
	if got := countMessagesContaining(sender.messages, BriefCollectedText("ru")); got != 0 {
		t.Fatalf("weak brief was accepted as complete: %d messages=%#v", got, sender.messages)
	}
	if len(sender.messages) == 0 || !strings.Contains(sender.messages[len(sender.messages)-1], "Короткий бриф") {
		t.Fatalf("weak brief did not get a brief reminder: %#v", sender.messages)
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

func TestPrematurePersistedHandoffBriefAnswerGetsAcknowledgement(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "77011114444@c.us"

	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateHandedOff
		conversation.HandedOffToOwner = true
		conversation.AutomationClosed = true
		conversation.Stopped = true
		conversation.QuestionnaireSent = true
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.Lead.BriefRequested = true
		conversation.Lead.Niche = "спорт"
		conversation.Lead.Goal = "рост продаж"
		conversation.Lead.Deadline = "в течение месяца"
		conversation.Lead.SelectedPackage = "standard"
		conversation.LastReplyText = BriefTextForPackage("ru", 3)
	})

	sendText(t, service, chatID, "1) оригинальные шиповки 2) сомневаются почему дешево 3) разные модели в наличии")

	if len(sender.messages) != 1 || sender.messages[0] != BriefCollectedText("ru") {
		t.Fatalf("post-handoff brief acknowledgement = %#v, want one thank-you", sender.messages)
	}
	conversation := snapshotConversation(t, store, chatID)
	if !conversation.HandedOffToOwner || !conversation.AutomationClosed || !conversation.Stopped {
		t.Fatalf("valid handoff should remain closed after acknowledgement: stage=%q handed=%v closed=%v stopped=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.Stopped)
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
	if conversation.Stage == StageBriefRequested || conversation.Stopped || conversation.HandedOffToOwner || conversation.AutomationClosed {
		t.Fatalf("qualified package selection opened brief too early: stage=%q stopped=%v handed=%v closed=%v", conversation.Stage, conversation.Stopped, conversation.HandedOffToOwner, conversation.AutomationClosed)
	}
	if conversation.Lead.Niche != "салон красоты" || conversation.Lead.Goal != "привлечь клиентов" || conversation.Lead.Deadline != "в течение месяца" || conversation.Lead.SelectedPackage != "standard" {
		t.Fatalf("lead fields = %#v", conversation.Lead)
	}
	if got := countMessagesContaining(sender.messages, "Новый квалифицированный лид WhatsApp"); got != 0 {
		t.Fatalf("admin notifications before brief = %d, want 0: %#v", got, sender.messages)
	}
	if got := countMessagesContaining(sender.messages, "Короткий бриф"); got != 0 {
		t.Fatalf("brief was sent immediately after package selection: %d messages=%#v", got, sender.messages)
	}

	sendText(t, service, chatID, "анкета")
	sendText(t, service, chatID, "1) рекламируем абонементы 2) хотят форму к лету 3) пробное занятие")

	conversation = snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateHandedOff || !conversation.Stopped || !conversation.HandedOffToOwner || !conversation.AutomationClosed || conversation.TransferredAt.IsZero() {
		t.Fatalf("qualified lead was not closed after brief: stage=%q stopped=%v handed=%v closed=%v transferred=%v", conversation.Stage, conversation.Stopped, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.TransferredAt)
	}
	if got := countMessagesContaining(sender.messages, "Новый квалифицированный лид WhatsApp"); got != 1 {
		t.Fatalf("admin notifications = %d, want 1: %#v", got, sender.messages)
	}
	adminMessage := sender.messages[len(sender.messages)-1]
	for _, want := range []string{
		"Имя: Yerek",
		"Телефон: +7 701 333 00 00",
		"ChatID: " + chatID,
		"Ниша: салон красоты",
		"Цель: привлечь клиентов",
		"Срок: в течение месяца",
		"Пакет: Standard / Стандарт",
		"Намерение клиента: хочет продолжить / готов к обработке заявки",
		"Статус: квалифицирован, передан менеджеру",
	} {
		if !strings.Contains(adminMessage, want) {
			t.Fatalf("admin message missing %q:\n%s", want, adminMessage)
		}
	}
	if strings.Contains(adminMessage, "-") && (strings.Contains(adminMessage, "Ниша: -") || strings.Contains(adminMessage, "Цель: -") || strings.Contains(adminMessage, "Пакет: -")) {
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

	if got := countMessagesContaining(sender.messages, "Короткий бриф"); got != 0 {
		t.Fatalf("brief was sent immediately after duplicate package selection: %d messages=%#v", got, sender.messages)
	}
	if len(sender.files) != 1 {
		t.Fatalf("selected package videos = %d, want 1: %#v", len(sender.files), sender.files)
	}
	if got := countMessagesContaining(sender.messages, "Новый квалифицированный лид WhatsApp"); got != 0 {
		t.Fatalf("admin notifications before brief = %d, want 0: %#v", got, sender.messages)
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

	if countMessagesContaining(sender.messages, "Выберите подходящий формат:") != 0 {
		t.Fatalf("standalone package options should not be sent: %#v", sender.messages)
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
