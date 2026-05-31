package bot

import (
	"context"
	"strings"
	"testing"
)

func TestProductNicheAnswerStoresNicheAndAsksOnlyMissingFields(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-bad-product"

	sendText(t, service, chatID, "Здравствуйте")
	sendText(t, service, chatID, "Бад для похудения продаю")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "бад для похудения" {
		t.Fatalf("niche = %q, want product niche", conversation.Lead.Niche)
	}
	last := sender.messages[len(sender.messages)-1]
	if strings.Contains(strings.ToLower(last), "ниш") {
		t.Fatalf("bot asked for already known niche: %q", last)
	}
	for _, want := range []string{"цель", "когда"} {
		if !strings.Contains(strings.ToLower(last), want) && !strings.Contains(strings.ToLower(last), "срок") {
			t.Fatalf("missing-field reply %q does not ask for goal/deadline", last)
		}
	}
}

func TestExplicitManagerRequestHandsOffAndStopsAutomation(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-manager-request"
	seedPresentedPackageMessages(store, chatID)

	sendText(t, service, chatID, "Давай отправь на менеджера. Формат ролика пусть он предложит то что продается")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateHandedOff || !conversation.HandedOffToOwner || !conversation.AutomationClosed || !conversation.Stopped {
		t.Fatalf("manager request did not close automation: stage=%q handed=%v closed=%v stopped=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.Stopped)
	}
	if conversation.Lead.SelectedPackage != packageNeedsManagerRecommendation {
		t.Fatalf("selected package = %q, want manager recommendation", conversation.Lead.SelectedPackage)
	}
	clientReplies := messagesToChat(sender, chatID)
	if len(clientReplies) != 1 {
		t.Fatalf("client replies = %#v, want one acknowledgement", clientReplies)
	}
	if strings.Contains(clientReplies[0], "Какой формат") || strings.Contains(clientReplies[0], "Какой пакет") {
		t.Fatalf("handoff acknowledgement asked a stale sales question: %q", clientReplies[0])
	}

	before := len(sender.messages)
	sendText(t, service, chatID, "Без ИИ консультанта")
	if len(sender.messages) != before {
		t.Fatalf("bot replied after manager handoff: %#v", sender.messages[before:])
	}
}

func TestYesAfterQuestionnaireOfferSendsBriefImmediately(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-questionnaire-yes"
	seedPresentedQualifiedLead(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQuestionnaireConfirm
		conversation.QuestionnaireOfferSent = true
		conversation.SelectedLevel = 3
		conversation.Lead.SelectedPackage = "standard"
	})

	sendText(t, service, chatID, "Да")

	if len(sender.messages) == 0 || sender.messages[len(sender.messages)-1] != BriefText("ru") {
		t.Fatalf("last reply = %#v, want questionnaire brief", sender.messages)
	}
	conversation := snapshotConversation(t, store, chatID)
	if !conversation.QuestionnaireSent || conversation.Stage != StageBriefRequested {
		t.Fatalf("questionnaire state = stage=%q sent=%v", conversation.Stage, conversation.QuestionnaireSent)
	}
}

func TestInstagramLinkAfterPackagesDoesNotAskFormat(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-link-after-packages"
	seedPresentedQualifiedLead(store, chatID)

	sendText(t, service, chatID, "https://www.instagram.com/stone.production")

	last := sender.messages[len(sender.messages)-1]
	if !strings.Contains(last, "Ссылку получил") {
		t.Fatalf("link was not acknowledged: %q", last)
	}
	if strings.Contains(last, "Какой формат вам понравился") {
		t.Fatalf("link fell through to format question: %q", last)
	}
}

func TestFormatAdviceQuestionGetsConsultativeAnswer(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))

	sendText(t, service, "chat-format-advice", "какой формат в рекламе лучше заходит?")

	last := sender.messages[len(sender.messages)-1]
	if !strings.Contains(last, "UGC") || !strings.Contains(last, "problem-solution") {
		t.Fatalf("format advice reply is not consultative: %q", last)
	}
	if strings.Contains(last, "Какой формат вам понравился") {
		t.Fatalf("format advice asked package fallback: %q", last)
	}
}

func TestMediaMessageGetsHelpfulFallbackAndHandoffStaysSilent(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-media"

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: chatID, TypeMessage: "imageMessage"}); err != nil {
		t.Fatalf("HandleIncomingMessage(media) error = %v", err)
	}
	if len(sender.messages) != 1 || !strings.Contains(sender.messages[0], "Материал получил") {
		t.Fatalf("media fallback = %#v", sender.messages)
	}

	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateHandedOff
		conversation.HandedOffToOwner = true
		conversation.AutomationClosed = true
		conversation.Stopped = true
	})
	before := len(sender.messages)
	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: chatID, TypeMessage: "voiceMessage"}); err != nil {
		t.Fatalf("HandleIncomingMessage(post-handoff media) error = %v", err)
	}
	if len(sender.messages) != before {
		t.Fatalf("post-handoff media got automation: %#v", sender.messages[before:])
	}
}

func TestGenericOkAfterQuestionnaireSentStaysQuiet(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-ok-after-brief"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = StageBriefRequested
		conversation.QuestionnaireSent = true
		conversation.Lead.BriefRequested = true
	})

	sendText(t, service, chatID, "Ок")

	if len(sender.messages) != 0 {
		t.Fatalf("generic acknowledgement after questionnaire should not get repeated prompt: %#v", sender.messages)
	}
}

func messagesToChat(sender *fakeSender, chatID string) []string {
	var result []string
	for i, id := range sender.chatIDs {
		if id == chatID {
			result = append(result, sender.messages[i])
		}
	}
	return result
}
