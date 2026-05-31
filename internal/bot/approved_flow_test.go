package bot

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFAQBeforeQualificationAnswersAndAsksQualification(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))

	sendText(t, service, "chat-faq-new", "Это настоящая съемка или AI?")

	if len(sender.messages) != 1 {
		t.Fatalf("messages = %#v, want one FAQ reply", sender.messages)
	}
	got := sender.messages[0]
	if !strings.Contains(got, FAQAnswerText(faqAIProduction, "ru")) || !strings.Contains(got, QualificationQuestionsText("ru")) {
		t.Fatalf("FAQ before qualification did not answer and continue correctly:\n%s", got)
	}
	if len(sender.files) != 0 {
		t.Fatalf("FAQ before qualification sent videos: %#v", sender.files)
	}
}

func TestFAQAfterPackagesReturnsToFormatQuestion(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-faq-packages"
	seedPresentedQualifiedLead(store, chatID)

	sendText(t, service, chatID, "Можно правки?")

	got := sender.messages[len(sender.messages)-1]
	if !strings.Contains(got, FAQAnswerText(faqRevisions, "ru")) || !strings.Contains(got, FormatQuestionText("ru")) {
		t.Fatalf("FAQ after packages reply mismatch:\n%s", got)
	}
}

func TestFAQAfterQuestionnaireOfferReturnsToOfferConfirmation(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-faq-questionnaire"
	seedPresentedQualifiedLead(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQuestionnaireConfirm
		conversation.QuestionnaireOfferSent = true
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.Lead.BriefRequested = false
	})

	sendText(t, service, chatID, "Это точно подойдет под рекламу?")

	got := sender.messages[len(sender.messages)-1]
	if !strings.Contains(got, FAQAnswerText(faqAds, "ru")) || !strings.Contains(got, "Отправить анкету?") {
		t.Fatalf("FAQ after questionnaire offer reply mismatch:\n%s", got)
	}
}

func TestQuestionnairePositiveSendsApprovedQuestionnaire(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-questionnaire-yes"
	seedPresentedQualifiedLead(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQuestionnaireConfirm
		conversation.QuestionnaireOfferSent = true
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
	})

	sendText(t, service, chatID, "да, отправьте")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != StageBriefRequested || !conversation.QuestionnaireSent || !conversation.Lead.BriefRequested {
		t.Fatalf("questionnaire state = stage=%q sent=%v brief=%v", conversation.Stage, conversation.QuestionnaireSent, conversation.Lead.BriefRequested)
	}
	if got := sender.messages[len(sender.messages)-1]; got != BriefText("ru") {
		t.Fatalf("questionnaire text mismatch:\n%s", got)
	}
}

func TestBriefRequestedPartialAnswerIsSavedWithoutRestart(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-partial-brief"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = StageBriefRequested
		conversation.QuestionnaireSent = true
		conversation.Lead.BriefRequested = true
		conversation.Lead.Niche = "мебель"
		conversation.Lead.Goal = "получать заявки"
		conversation.Lead.Deadline = "на этой неделе"
		conversation.Lead.SelectedPackage = "basic"
		conversation.SelectedLevel = 2
	})

	sendText(t, service, chatID, "Продаем мебель")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != StageBriefRequested || conversation.HandedOffToOwner || conversation.AutomationClosed {
		t.Fatalf("partial brief changed state incorrectly: stage=%q handed=%v closed=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed)
	}
	if !strings.Contains(conversation.Lead.FreeText, "Продаем мебель") {
		t.Fatalf("partial brief was not saved: %#v", conversation.Lead.FreeText)
	}
	if got := sender.messages[len(sender.messages)-1]; strings.Contains(got, "Какой формат") || strings.Contains(got, "В какой нише") {
		t.Fatalf("brief state restarted funnel: %q", got)
	}
}

func TestBriefRequestedFAQStaysInBriefState(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-brief-faq"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = StageBriefRequested
		conversation.QuestionnaireSent = true
		conversation.Lead.BriefRequested = true
	})

	sendText(t, service, chatID, "А можно полностью онлайн?")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != StageBriefRequested {
		t.Fatalf("brief FAQ changed state to %q", conversation.Stage)
	}
	got := sender.messages[len(sender.messages)-1]
	if !strings.Contains(got, FAQAnswerText(faqOnline, "ru")) || !strings.Contains(got, BriefContextReturnText("ru")) {
		t.Fatalf("brief FAQ reply mismatch:\n%s", got)
	}
	if len(sender.files) != 0 || strings.Contains(got, FormatQuestionText("ru")) || strings.Contains(got, QualificationGreetingText("ru")) {
		t.Fatalf("brief FAQ restarted package/qualification flow: messages=%#v files=%#v", sender.messages, sender.files)
	}
}

func TestBriefRequestedCompleteAnswerHandsOff(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-complete-brief"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = StageBriefRequested
		conversation.QuestionnaireSent = true
		conversation.Lead.BriefRequested = true
		conversation.Lead.Niche = "мебель"
		conversation.Lead.Goal = "получать заявки"
		conversation.Lead.Deadline = "на этой неделе"
		conversation.Lead.SelectedPackage = "standard"
		conversation.SelectedLevel = 3
	})

	sendText(t, service, chatID, "Продаем мебель. Сильная сторона — делаем на заказ. Клиенты — семьи и новые квартиры. Сейчас скидка 10%, Instagram @stone")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateHandedOff || !conversation.HandedOffToOwner || !conversation.AutomationClosed || !conversation.NextFollowupAt.IsZero() {
		t.Fatalf("complete brief did not hand off cleanly: stage=%q handed=%v closed=%v next=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.NextFollowupAt)
	}
	if got := countMessagesContaining(sender.messages, "Новый квалифицированный лид WhatsApp"); got != 1 {
		t.Fatalf("admin notifications = %d, want 1: %#v", got, sender.messages)
	}
}

func TestBriefRequestedObservedAnswerWithNetuHandsOff(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-brief-netu"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = StageBriefRequested
		conversation.QuestionnaireSent = true
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.Lead.BriefRequested = true
		conversation.Lead.Niche = "обувь"
		conversation.Lead.Goal = "получать заявки"
		conversation.Lead.Deadline = "на этой неделе"
		conversation.Lead.SelectedPackage = "standard"
		conversation.SelectedLevel = 3
		conversation.FollowupStage = followupStageQuestionnaireReminder
		conversation.FollowupReferenceAt = time.Now().UTC().Add(-25 * time.Hour)
		conversation.NextFollowupAt = time.Now().UTC().Add(-time.Minute)
	})

	sendText(t, service, chatID, "Обувь\nУ нас премильная обувь\nПредприниматели, муж/жен, высокий чек\nНету")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateHandedOff || conversation.Stage == ClientStateStopped || conversation.OptOut || conversation.SelectedLevel != 3 {
		t.Fatalf("brief with netu was not handed off correctly: stage=%q optout=%v level=%d", conversation.Stage, conversation.OptOut, conversation.SelectedLevel)
	}
	if normalizeLeadStatus(conversation.LeadStatus) == LeadStatusClosed || normalizeLeadStatus(conversation.Lead.LeadStatus) == LeadStatusClosed {
		t.Fatalf("brief with netu was classified as closed: conversation=%q lead=%q", conversation.LeadStatus, conversation.Lead.LeadStatus)
	}
	if !strings.Contains(conversation.Lead.FreeText, "Нету") {
		t.Fatalf("brief was not saved: %#v", conversation.Lead.FreeText)
	}
	if got := countMessagesContaining(sender.messages, "Новый квалифицированный лид WhatsApp"); got != 1 {
		t.Fatalf("admin notifications = %d, want 1: %#v", got, sender.messages)
	}
	if !conversation.NextFollowupAt.IsZero() || conversation.FollowupStage != "" {
		t.Fatalf("follow-up was not cancelled after handoff: next=%v stage=%q", conversation.NextFollowupAt, conversation.FollowupStage)
	}

	beforeMessages := len(sender.messages)
	beforeFiles := len(sender.files)
	sendText(t, service, chatID, "Спасибо, жду")
	if len(sender.messages) != beforeMessages || len(sender.files) != beforeFiles {
		t.Fatalf("handoff did not stay silent: new messages=%#v new files=%#v", sender.messages[beforeMessages:], sender.files[beforeFiles:])
	}
	if got := countMessagesContaining(sender.messages, "Новый квалифицированный лид WhatsApp"); got != 1 {
		t.Fatalf("admin notification repeated after handoff: %d messages=%#v", got, sender.messages)
	}
}

func TestBriefRequestedAkciiNetIsBriefAnswer(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-brief-akcii-net"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = StageBriefRequested
		conversation.QuestionnaireSent = true
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.Lead.BriefRequested = true
		conversation.Lead.Niche = "мебель"
		conversation.Lead.Goal = "получать заявки"
		conversation.Lead.Deadline = "на этой неделе"
		conversation.Lead.SelectedPackage = "basic"
		conversation.SelectedLevel = 2
	})

	sendText(t, service, chatID, "Продаем мебель. Сильная сторона — качество. Клиенты — семьи. Акции нет.")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage == ClientStateStopped || conversation.OptOut {
		t.Fatalf("акции нет was treated as stop: stage=%q optout=%v", conversation.Stage, conversation.OptOut)
	}
	if normalizeLeadStatus(conversation.LeadStatus) == LeadStatusClosed || normalizeLeadStatus(conversation.Lead.LeadStatus) == LeadStatusClosed {
		t.Fatalf("акции нет was classified as closed: conversation=%q lead=%q", conversation.LeadStatus, conversation.Lead.LeadStatus)
	}
	if !strings.Contains(conversation.Lead.FreeText, "Акции нет") {
		t.Fatalf("brief was not saved: %#v", conversation.Lead.FreeText)
	}
}

func TestBriefRequestedRealOptOutStopsWithoutHandoff(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-brief-real-optout"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = StageBriefRequested
		conversation.QuestionnaireSent = true
		conversation.Lead.BriefRequested = true
		conversation.Lead.Niche = "мебель"
		conversation.Lead.Goal = "получать заявки"
		conversation.Lead.Deadline = "на этой неделе"
		conversation.Lead.SelectedPackage = "standard"
		conversation.SelectedLevel = 3
		conversation.FollowupStage = followupStageQuestionnaireReminder
		conversation.FollowupReferenceAt = time.Now().UTC().Add(-25 * time.Hour)
		conversation.NextFollowupAt = time.Now().UTC().Add(-time.Minute)
	})

	sendText(t, service, chatID, "Не интересно, больше не пишите")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateHandedOff || !conversation.HandedOffToOwner || !conversation.AutomationClosed || !conversation.Stopped {
		t.Fatalf("real opt-out handoff state = stage=%q handed=%v closed=%v stopped=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.Stopped)
	}
	if got := countMessagesContaining(sender.messages, "Новый квалифицированный лид WhatsApp"); got != 1 {
		t.Fatalf("admin notification count for opt-out = %d messages=%#v", got, sender.messages)
	}
	if got := countMessagesContaining(sender.messages, "неправильно понял"); got != 1 {
		t.Fatalf("recovery apology count = %d messages=%#v", got, sender.messages)
	}
	if !conversation.NextFollowupAt.IsZero() || conversation.FollowupStage != "" {
		t.Fatalf("follow-up was not cancelled after opt-out: next=%v stage=%q", conversation.NextFollowupAt, conversation.FollowupStage)
	}
}

func TestBriefRequestedPartialProductIsSavedWithoutRestart(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-brief-partial-shoes"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = StageBriefRequested
		conversation.QuestionnaireSent = true
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.Lead.BriefRequested = true
		conversation.Lead.Niche = "обувь"
		conversation.Lead.Goal = "получать заявки"
		conversation.Lead.Deadline = "на этой неделе"
		conversation.Lead.SelectedPackage = "standard"
		conversation.SelectedLevel = 3
	})

	sendText(t, service, chatID, "Продаем обувь")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != StageBriefRequested || conversation.HandedOffToOwner || conversation.AutomationClosed || conversation.OptOut {
		t.Fatalf("partial brief changed state incorrectly: stage=%q handed=%v closed=%v optout=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.OptOut)
	}
	if !strings.Contains(conversation.Lead.FreeText, "Продаем обувь") {
		t.Fatalf("partial brief was not saved: %#v", conversation.Lead.FreeText)
	}
	if len(sender.files) != 0 {
		t.Fatalf("partial brief resent videos: %#v", sender.files)
	}
	got := sender.messages[len(sender.messages)-1]
	if strings.Contains(got, FormatQuestionText("ru")) || strings.Contains(got, QualificationGreetingText("ru")) {
		t.Fatalf("partial brief restarted funnel: %q", got)
	}
}

func TestBriefRequestedNoOfferOnlyIsSavedWithoutStop(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-brief-netu-only"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = StageBriefRequested
		conversation.QuestionnaireSent = true
		conversation.Lead.BriefRequested = true
	})

	sendText(t, service, chatID, "Нету")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != StageBriefRequested || conversation.Stopped || conversation.OptOut || conversation.HandedOffToOwner {
		t.Fatalf("no-offer brief answer stopped or handed off incorrectly: stage=%q stopped=%v optout=%v handed=%v", conversation.Stage, conversation.Stopped, conversation.OptOut, conversation.HandedOffToOwner)
	}
	if !strings.Contains(conversation.Lead.FreeText, "Нету") {
		t.Fatalf("no-offer answer was not saved: %#v", conversation.Lead.FreeText)
	}
	if got := countMessagesContaining(sender.messages, "Новый квалифицированный лид WhatsApp"); got != 0 {
		t.Fatalf("admin was notified for no-offer-only partial: %d messages=%#v", got, sender.messages)
	}
}

func TestBriefHandoffSkipsStaleFollowup(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	service.SetDelayedPackageOptions(DelayedPackageOptions{Enabled: true, After: 15 * time.Minute})
	chatID := "chat-brief-stale-followup"
	now := time.Now().UTC()
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = StageBriefRequested
		conversation.QuestionnaireSent = true
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.Lead.BriefRequested = true
		conversation.Lead.Niche = "мебель"
		conversation.Lead.Goal = "получать заявки"
		conversation.Lead.Deadline = "на этой неделе"
		conversation.Lead.SelectedPackage = "standard"
		conversation.SelectedLevel = 3
		conversation.FollowupStage = followupStageQuestionnaireReminder
		conversation.FollowupReferenceAt = now.Add(-25 * time.Hour)
		conversation.NextFollowupAt = now.Add(-time.Minute)
		conversation.LastIncomingAt = now.Add(-26 * time.Hour)
	})

	sendText(t, service, chatID, "Продаем мебель. Сильная сторона — качество. Клиенты — семьи. Акции нет.")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateHandedOff || !conversation.NextFollowupAt.IsZero() || conversation.FollowupStage != "" {
		t.Fatalf("brief handoff did not cancel stale follow-up: stage=%q next=%v followup=%q", conversation.Stage, conversation.NextFollowupAt, conversation.FollowupStage)
	}
	beforeMessages := len(sender.messages)
	beforeFiles := len(sender.files)
	if err := service.ProcessDueFollowups(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("ProcessDueFollowups() error = %v", err)
	}
	if len(sender.messages) != beforeMessages || len(sender.files) != beforeFiles {
		t.Fatalf("stale follow-up sent after handoff: messages=%#v files=%#v", sender.messages[beforeMessages:], sender.files[beforeFiles:])
	}
}

func TestOneHourFollowupSendsQuestionnaireOffer(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	service.SetDelayedPackageOptions(DelayedPackageOptions{Enabled: true, After: 15 * time.Minute})
	chatID := "chat-followup-offer"
	now := time.Now().UTC()
	seedPresentedQualifiedLead(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.FollowupStage = followupStageQuestionnaireOffer
		conversation.FollowupReferenceAt = now.Add(-time.Hour)
		conversation.NextFollowupAt = now.Add(-time.Minute)
		conversation.LastIncomingAt = now.Add(-2 * time.Hour)
	})

	if err := service.ProcessDueFollowups(context.Background(), now); err != nil {
		t.Fatalf("ProcessDueFollowups() error = %v", err)
	}
	if got := sender.messages[len(sender.messages)-1]; got != QuestionnaireOfferText("ru") {
		t.Fatalf("questionnaire offer follow-up mismatch:\n%s", got)
	}
}

func TestTwentyFourHourFollowupSendsReminderOnce(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	service.SetDelayedPackageOptions(DelayedPackageOptions{Enabled: true, After: 15 * time.Minute})
	chatID := "chat-followup-reminder"
	now := time.Now().UTC()
	seedPresentedQualifiedLead(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQuestionnaireConfirm
		conversation.QuestionnaireOfferSent = true
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.FollowupStage = followupStageQuestionnaireReminder
		conversation.FollowupReferenceAt = now.Add(-25 * time.Hour)
		conversation.NextFollowupAt = now.Add(-time.Minute)
		conversation.LastIncomingAt = now.Add(-26 * time.Hour)
	})

	if err := service.ProcessDueFollowups(context.Background(), now); err != nil {
		t.Fatalf("ProcessDueFollowups() error = %v", err)
	}
	if got := sender.messages[len(sender.messages)-1]; got != QuestionnaireReminderText("ru") {
		t.Fatalf("questionnaire reminder mismatch:\n%s", got)
	}
	before := len(sender.messages)
	if err := service.ProcessDueFollowups(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("second ProcessDueFollowups() error = %v", err)
	}
	if len(sender.messages) != before {
		t.Fatalf("24h reminder repeated: %#v", sender.messages[before:])
	}
}

func TestWeeklyFollowupSendsDiscountOnce(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	service.SetDelayedPackageOptions(DelayedPackageOptions{Enabled: true, After: 15 * time.Minute})
	chatID := "chat-followup-weekly"
	now := time.Now().UTC()
	seedPresentedQualifiedLead(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.QuestionnaireOfferSent = true
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.SentVideoFiles[VideoLevel3] = now.Add(-8 * 24 * time.Hour)
		conversation.FollowupStage = followupStageWeeklyDiscount
		conversation.FollowupReferenceAt = now.Add(-8 * 24 * time.Hour)
		conversation.NextFollowupAt = now.Add(-time.Minute)
		conversation.LastIncomingAt = now.Add(-9 * 24 * time.Hour)
	})

	if err := service.ProcessDueFollowups(context.Background(), now); err != nil {
		t.Fatalf("ProcessDueFollowups() error = %v", err)
	}
	if got := sender.messages[len(sender.messages)-1]; got != WeeklyDiscountFollowupText("ru") {
		t.Fatalf("weekly discount mismatch:\n%s", got)
	}
	before := len(sender.messages)
	if err := service.ProcessDueFollowups(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("second ProcessDueFollowups() error = %v", err)
	}
	if len(sender.messages) != before {
		t.Fatalf("weekly discount repeated: %#v", sender.messages[before:])
	}
}

func TestUserReplyAfterWeeklyFollowupHandsOff(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-weekly-reply"
	now := time.Now().UTC()
	seedPresentedQualifiedLead(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.FollowupStage = followupStageWeeklyDiscountSent
		conversation.LastFollowupSentAt = now.Add(-time.Hour)
		conversation.FollowupReferenceAt = now.Add(-time.Hour)
		conversation.NextFollowupAt = time.Time{}
	})

	sendText(t, service, chatID, "да интересно")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateHandedOff || !conversation.HandedOffToOwner || !conversation.AutomationClosed {
		t.Fatalf("weekly reply did not hand off: stage=%q handed=%v closed=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed)
	}
	if got := countMessagesContaining(sender.messages, "Новый квалифицированный лид WhatsApp"); got != 1 {
		t.Fatalf("admin notification count = %d, messages=%#v", got, sender.messages)
	}
}

func TestOfftopicQuestionDoesNotRestartOrSendPackages(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-offtopic"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
	})

	sendText(t, service, chatID, "Какая погода?")

	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("offtopic message got automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
}

func TestSQLiteDueFollowupSurvivesRestartAndSendsOnce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stone.sqlite3")
	chatID := "77029990000@c.us"
	now := time.Now().UTC()

	store1, err := NewSQLiteConversationStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore() error = %v", err)
	}
	store1.Update(chatID, func(conversation *Conversation) {
		conversation.Language = "ru"
		conversation.Stage = ClientStatePackagesPresented
		conversation.InitialMessageSent = true
		conversation.PackagesSent = true
		conversation.SentPortfolio = true
		conversation.Lead.PortfolioSent = true
		conversation.Lead.OfferSent = true
		conversation.Lead.Niche = "мебель"
		conversation.Lead.Goal = "получать заявки"
		conversation.Lead.Deadline = "на этой неделе"
		conversation.FollowupStage = followupStageQuestionnaireOffer
		conversation.FollowupReferenceAt = now.Add(-2 * time.Hour)
		conversation.NextFollowupAt = now.Add(-time.Minute)
		conversation.LastIncomingAt = now.Add(-3 * time.Hour)
	})
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
	sender := &fakeSender{}
	service := newTestServiceWithVideoDir(sender, store2, PortfolioLinks{}, testVideoDir(t))
	service.SetDelayedPackageOptions(DelayedPackageOptions{Enabled: true, After: 15 * time.Minute})

	if err := service.ProcessDueFollowups(context.Background(), now); err != nil {
		t.Fatalf("ProcessDueFollowups() error = %v", err)
	}
	if got := countMessagesContaining(sender.messages, QuestionnaireOfferText("ru")); got != 1 {
		t.Fatalf("questionnaire offer count = %d, messages=%#v", got, sender.messages)
	}
	if err := service.ProcessDueFollowups(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("second ProcessDueFollowups() error = %v", err)
	}
	if got := countMessagesContaining(sender.messages, QuestionnaireOfferText("ru")); got != 1 {
		t.Fatalf("questionnaire offer repeated after restart: %d messages=%#v", got, sender.messages)
	}
}
