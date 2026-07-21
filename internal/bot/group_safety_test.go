package bot

import (
	"context"
	"testing"
	"time"
)

func TestIncomingWhatsAppGroupMessageDoesNotSendOrAnalyze(t *testing.T) {
	sender := &fakeSender{}
	ai := &fakeAI{}
	store := NewConversationStore()
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	groupChatID := "120363123456789@g.us"

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		IDMessage: "group-incoming",
		ChatID:    groupChatID,
		Text:      "Здравствуйте, хочу заказать ролик",
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("group chat got automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
	if ai.analysisCalled {
		t.Fatal("group chat reached AnalyzeCustomerMessage")
	}
	if exists, err := store.ConversationExists(context.Background(), groupChatID); err != nil || exists {
		t.Fatalf("group conversation exists=%v err=%v, want no customer lead", exists, err)
	}
}

func TestPrivateWhatsAppCustomerChatStillStartsNormalFlow(t *testing.T) {
	sender := &fakeSender{}
	ai := &fakeAI{}
	service := NewService(sender, ai, NewConversationStore(), testVideoDir(t), PortfolioLinks{}, "auto", nil)

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		ChatID: "77012345678@c.us",
		Text:   "Здравствуйте",
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if ai.analysisCalled {
		t.Fatal("private first greeting should not depend on AnalyzeCustomerMessage")
	}
	if len(sender.messages) != 1 || sender.messages[0] != FirstContactWelcomeText("ru") || len(sender.files) != 3 {
		t.Fatalf("private chat flow changed: %#v", sender.messages)
	}
}

func TestCustomerOutgoingGuardsBlockGroupTextAndVideo(t *testing.T) {
	sender := &fakeSender{}
	service := newTestServiceWithVideoDir(sender, NewConversationStore(), PortfolioLinks{}, testVideoDir(t))
	groupChatID := "120363123456789@g.us"

	if err := service.sendAndRemember(context.Background(), groupChatID, QualificationGreetingText("ru"), ClientStateAwaitingQualification, 0); err != nil {
		t.Fatalf("sendAndRemember() error = %v", err)
	}
	if err := service.sendVideos(context.Background(), groupChatID, []string{VideoLevel1}, "ru", false); err != nil {
		t.Fatalf("sendVideos() error = %v", err)
	}

	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("group outbound was not blocked: messages=%#v files=%#v", sender.messages, sender.files)
	}
}

func TestDelayedSchedulerSkipsGroupConversation(t *testing.T) {
	sender := &fakeSender{fileMessageIDs: []string{"test-video-id", "basic-video-id", "standard-video-id"}}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	service.SetDelayedPackageOptions(DelayedPackageOptions{Enabled: true, After: 15 * time.Minute})
	groupChatID := "120363123456789@g.us"
	now := time.Now().UTC()

	store.Update(groupChatID, func(conversation *Conversation) {
		conversation.Language = "ru"
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
		conversation.InitialGreetingSentAt = now.Add(-20 * time.Minute)
		conversation.FollowupStage = followupStageSendPackages
		conversation.FollowupReferenceAt = now.Add(-20 * time.Minute)
		conversation.NextFollowupAt = now.Add(-time.Minute)
	})

	if err := service.ProcessDueFollowups(context.Background(), now); err != nil {
		t.Fatalf("ProcessDueFollowups() error = %v", err)
	}
	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("group scheduler sent automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
}

func TestManagerNotificationAllowsExplicitAdminGroup(t *testing.T) {
	adminGroupID := "120363123456789@g.us"
	sender := &fakeSender{}
	service := newTestServiceWithVideoDir(sender, NewConversationStore(), PortfolioLinks{}, testVideoDir(t), adminGroupID)

	if err := service.sendManagerWhatsAppMessage(context.Background(), adminGroupID, "Новый квалифицированный лид WhatsApp"); err != nil {
		t.Fatalf("sendManagerWhatsAppMessage() error = %v", err)
	}

	if len(sender.messages) != 1 || sender.chatIDs[0] != adminGroupID {
		t.Fatalf("manager group notification not sent to explicit allowlist: chatIDs=%#v messages=%#v", sender.chatIDs, sender.messages)
	}
}
