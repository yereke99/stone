package bot

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yereke99/stone/internal/greenapi"
)

type fakeHistorySource struct {
	messages []greenapi.ChatHistoryMessage
	err      error
	calls    int
	chatIDs  []string
	counts   []int
}

func (s *fakeHistorySource) GetChatHistory(ctx context.Context, chatID string, count int) ([]greenapi.ChatHistoryMessage, error) {
	s.calls++
	s.chatIDs = append(s.chatIDs, chatID)
	s.counts = append(s.counts, count)
	if s.err != nil {
		return nil, s.err
	}
	return append([]greenapi.ChatHistoryMessage(nil), s.messages...), nil
}

func newHistoryGuardTestService(sender *fakeSender, store *ConversationStore, history *fakeHistorySource, videoDir string) *Service {
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, videoDir)
	service.SetHistoryGuard(history, HistoryGuardOptions{
		Enabled:       true,
		LookbackCount: 10,
		Timeout:       time.Second,
		FailClosed:    true,
	})
	return service
}

func incomingGuardMessage(chatID string, id string, text string, at time.Time) IncomingMessage {
	return IncomingMessage{
		IDMessage: id,
		ChatID:    chatID,
		Text:      text,
		Timestamp: at,
	}
}

func TestHistoryGuardUnknownChatNoPriorHistoryStartsNewClientFunnel(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	history := &fakeHistorySource{}
	service := newHistoryGuardTestService(sender, store, history, testVideoDir(t))
	chatID := "77070000001@c.us"

	if err := service.HandleIncomingMessage(context.Background(), incomingGuardMessage(chatID, "current-new", "Здравствуйте", time.Now().UTC())); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if history.calls != 1 {
		t.Fatalf("history calls = %d, want 1", history.calls)
	}
	if len(sender.messages) != 1 || sender.messages[0] != QualificationGreetingText("ru") {
		t.Fatalf("messages = %#v, want qualification greeting", sender.messages)
	}
	if len(sender.files) != 0 {
		t.Fatalf("files sent = %#v, want none", sender.files)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.HistoryClassification != HistoryClassificationNewClient || conversation.HistoryDetected {
		t.Fatalf("history classification = %q detected=%v, want new_client false", conversation.HistoryClassification, conversation.HistoryDetected)
	}
	if conversation.Stage != ClientStateAwaitingQualification || !conversation.InitialMessageSent {
		t.Fatalf("state = %q initial=%v, want awaiting qualification", conversation.Stage, conversation.InitialMessageSent)
	}
}

func TestHistoryGuardUnknownChatPriorHistorySkipsColdGreeting(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	now := time.Now().UTC()
	history := &fakeHistorySource{messages: []greenapi.ChatHistoryMessage{
		{
			Type:        "incoming",
			IDMessage:   "old-1",
			Timestamp:   now.Add(-24 * time.Hour).Unix(),
			TypeMessage: greenapi.TypeMessageText,
			TextMessage: "Здравствуйте, интересовался роликами",
		},
	}}
	service := newHistoryGuardTestService(sender, store, history, testVideoDir(t))
	chatID := "77070000002@c.us"

	if err := service.HandleIncomingMessage(context.Background(), incomingGuardMessage(chatID, "current-old", "Добрый день", now)); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(sender.messages) != 0 {
		t.Fatalf("bot sent cold greeting to legacy chat: %#v", sender.messages)
	}
	if len(sender.files) != 0 {
		t.Fatalf("bot sent videos to legacy chat: %#v", sender.files)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.HistoryClassification != HistoryClassificationLegacyExisting || !conversation.DoNotAutoStart || !conversation.LegacyExisting {
		t.Fatalf("conversation history flags = classification=%q do_not=%v legacy=%v", conversation.HistoryClassification, conversation.DoNotAutoStart, conversation.LegacyExisting)
	}
	if conversation.Stage != ClientStateLegacyExisting {
		t.Fatalf("state = %q, want legacy_existing", conversation.Stage)
	}
}

func TestHistoryGuardPriorHistoryWithNewOrderSendsSoftClarificationOnly(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	now := time.Now().UTC()
	history := &fakeHistorySource{messages: []greenapi.ChatHistoryMessage{
		{
			Type:        "outgoing",
			IDMessage:   "old-out",
			Timestamp:   now.Add(-30 * 24 * time.Hour).Unix(),
			TypeMessage: greenapi.TypeMessageText,
			TextMessage: "Передаю менеджеру Stone production",
		},
	}}
	service := newHistoryGuardTestService(sender, store, history, testVideoDir(t))
	chatID := "77070000003@c.us"

	if err := service.HandleIncomingMessage(context.Background(), incomingGuardMessage(chatID, "current-reengage", "Нужно новое видео для нового проекта", now)); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(sender.messages) != 1 {
		t.Fatalf("messages = %#v, want one soft clarification", sender.messages)
	}
	if sender.messages[0] == QualificationGreetingText("ru") || !strings.Contains(sender.messages[0], "для какого проекта") {
		t.Fatalf("unexpected clarification: %q", sender.messages[0])
	}
	if len(sender.files) != 0 {
		t.Fatalf("reengagement sent package videos: %#v", sender.files)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.HistoryClassification != HistoryClassificationLegacyReengagement || !conversation.LegacyReengagement {
		t.Fatalf("history classification = %q reengagement=%v", conversation.HistoryClassification, conversation.LegacyReengagement)
	}
	if conversation.Stage != ClientStateAwaitingQualification {
		t.Fatalf("stage = %q, want awaiting qualification after clarification", conversation.Stage)
	}
}

func TestHistoryGuardFetchErrorFailClosedDoesNotAutoReply(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	history := &fakeHistorySource{err: errors.New("greenapi unavailable")}
	service := newHistoryGuardTestService(sender, store, history, testVideoDir(t))
	chatID := "77070000004@c.us"

	if err := service.HandleIncomingMessage(context.Background(), incomingGuardMessage(chatID, "current-fail", "Здравствуйте", time.Now().UTC())); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("bot replied after failed history check: messages=%#v files=%#v", sender.messages, sender.files)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.HistoryClassification != HistoryClassificationHistoryCheckFailed || !conversation.DoNotAutoStart {
		t.Fatalf("classification=%q do_not=%v, want history_check_failed fail closed", conversation.HistoryClassification, conversation.DoNotAutoStart)
	}
}

func TestStoredLegacyProcessedCanReengageOnClearNewRequest(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	history := &fakeHistorySource{}
	service := newHistoryGuardTestService(sender, store, history, testVideoDir(t))
	chatID := "770700000045@c.us"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateLegacyProcessed
		conversation.HistoryCheckedAt = time.Now().UTC().Add(-time.Hour)
		conversation.HistoryDetected = true
		conversation.HistoryMessageCount = 4
		conversation.HistoryClassification = HistoryClassificationLegacyProcessed
		conversation.DoNotAutoStart = true
		conversation.LegacyProcessed = true
	})

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		IDMessage:      "stored-reengage",
		ChatID:         chatID,
		Text:           "Сколько стоит новое видео?",
		Timestamp:      time.Now().UTC(),
		LocalChatKnown: true,
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if history.calls != 0 {
		t.Fatalf("history guard fetched history again for stored legacy chat: %d", history.calls)
	}
	if len(sender.messages) != 1 || !strings.Contains(sender.messages[0], "для какого проекта") {
		t.Fatalf("messages = %#v, want one soft clarification", sender.messages)
	}
	if len(sender.files) != 0 {
		t.Fatalf("stored reengagement sent package videos: %#v", sender.files)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.HistoryClassification != HistoryClassificationLegacyReengagement || !conversation.LegacyReengagement || conversation.DoNotAutoStart {
		t.Fatalf("history flags = classification=%q reengagement=%v do_not=%v", conversation.HistoryClassification, conversation.LegacyReengagement, conversation.DoNotAutoStart)
	}
}

func TestKnownLocalChatSkipsHistoryGuardAndContinuesStateMachine(t *testing.T) {
	sender := &fakeSender{fileMessageIDs: []string{"test-video-id", "basic-video-id", "standard-video-id"}}
	store := NewConversationStore()
	now := time.Now().UTC()
	chatID := "77070000005@c.us"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
		conversation.LastReplyAt = now.Add(-time.Minute)
	})
	history := &fakeHistorySource{messages: []greenapi.ChatHistoryMessage{
		{Type: "incoming", IDMessage: "old", Timestamp: now.Add(-24 * time.Hour).Unix(), TextMessage: "старый чат"},
	}}
	service := newHistoryGuardTestService(sender, store, history, testVideoDir(t))

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		IDMessage:      "known-local",
		ChatID:         chatID,
		Text:           "салон красоты, цель заявки, срок через неделю",
		Timestamp:      now,
		LocalChatKnown: true,
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if history.calls != 0 {
		t.Fatalf("history guard was called for known local chat: %d", history.calls)
	}
	if len(sender.files) != 3 {
		t.Fatalf("known chat did not continue existing flow, files=%#v", sender.files)
	}
}

func TestDelayedPackagesSendOnceForUnansweredNewClient(t *testing.T) {
	sender := &fakeSender{fileMessageIDs: []string{"test-video-id", "basic-video-id", "standard-video-id"}}
	store := NewConversationStore()
	history := &fakeHistorySource{}
	service := newHistoryGuardTestService(sender, store, history, testVideoDir(t))
	service.SetDelayedPackageOptions(DelayedPackageOptions{Enabled: true, After: 15 * time.Minute})
	chatID := "77070000006@c.us"
	now := time.Now().UTC()

	if err := service.HandleIncomingMessage(context.Background(), incomingGuardMessage(chatID, "new-delayed", "Здравствуйте", now)); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}
	store.Update(chatID, func(conversation *Conversation) {
		conversation.InitialGreetingSentAt = now.Add(-16 * time.Minute)
		conversation.LastReplyAt = now.Add(-16 * time.Minute)
		conversation.LastIncomingAt = now.Add(-17 * time.Minute)
	})

	if err := service.ProcessDueDelayedPackages(context.Background(), now); err != nil {
		t.Fatalf("ProcessDueDelayedPackages() error = %v", err)
	}
	if len(sender.files) != 3 {
		t.Fatalf("files sent = %#v, want three package videos", sender.files)
	}
	for i, want := range []string{VideoLevel1, VideoLevel2, VideoLevel3} {
		if filepath.Base(sender.files[i]) != want {
			t.Fatalf("file[%d] = %q, want %q", i, sender.files[i], want)
		}
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.AutoPackagesSentAt.IsZero() || !conversation.PackagesSent {
		t.Fatalf("auto package flags not persisted: sent_at=%v packages=%v", conversation.AutoPackagesSentAt, conversation.PackagesSent)
	}

	if err := service.ProcessDueDelayedPackages(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("second ProcessDueDelayedPackages() error = %v", err)
	}
	if len(sender.files) != 3 {
		t.Fatalf("delayed packages were repeated: %#v", sender.files)
	}
}

func TestDelayedPackagesSkipWhenNewClientRepliedBeforeDue(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	history := &fakeHistorySource{}
	service := newHistoryGuardTestService(sender, store, history, testVideoDir(t))
	service.SetDelayedPackageOptions(DelayedPackageOptions{Enabled: true, After: 15 * time.Minute})
	chatID := "77070000007@c.us"
	now := time.Now().UTC()

	if err := service.HandleIncomingMessage(context.Background(), incomingGuardMessage(chatID, "new-replied", "Здравствуйте", now)); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}
	store.Update(chatID, func(conversation *Conversation) {
		conversation.InitialGreetingSentAt = now.Add(-16 * time.Minute)
		conversation.LastIncomingAt = now.Add(-5 * time.Minute)
		conversation.LastReplyAt = now.Add(-4 * time.Minute)
	})

	if err := service.ProcessDueDelayedPackages(context.Background(), now); err != nil {
		t.Fatalf("ProcessDueDelayedPackages() error = %v", err)
	}
	if len(sender.files) != 0 {
		t.Fatalf("files sent after client replied: %#v", sender.files)
	}
}

func TestDelayedPackagesSkipLegacyExisting(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	now := time.Now().UTC()
	history := &fakeHistorySource{messages: []greenapi.ChatHistoryMessage{
		{Type: "incoming", IDMessage: "old-legacy", Timestamp: now.Add(-24 * time.Hour).Unix(), TextMessage: "старый чат"},
	}}
	service := newHistoryGuardTestService(sender, store, history, testVideoDir(t))
	service.SetDelayedPackageOptions(DelayedPackageOptions{Enabled: true, After: 15 * time.Minute})
	chatID := "77070000008@c.us"

	if err := service.HandleIncomingMessage(context.Background(), incomingGuardMessage(chatID, "legacy-delayed", "Добрый день", now)); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}
	if err := service.ProcessDueDelayedPackages(context.Background(), now.Add(20*time.Minute)); err != nil {
		t.Fatalf("ProcessDueDelayedPackages() error = %v", err)
	}
	if len(sender.files) != 0 || len(sender.messages) != 0 {
		t.Fatalf("legacy chat received automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
}

func TestDelayedPackagesSkipClosedOrStoppedChats(t *testing.T) {
	tests := []struct {
		name string
		mark func(*Conversation)
	}{
		{name: "handed off", mark: func(c *Conversation) {
			c.Stage = ClientStateHandedOff
			c.HandedOffToOwner = true
			c.AutomationClosed = true
			c.TransferredAt = time.Now().UTC()
		}},
		{name: "stopped", mark: func(c *Conversation) {
			c.Stage = ClientStateStopped
			c.Stopped = true
		}},
		{name: "opt out", mark: func(c *Conversation) {
			c.Stage = ClientStateOptOut
			c.OptOut = true
			c.Stopped = true
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakeSender{}
			store := NewConversationStore()
			service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
			service.SetDelayedPackageOptions(DelayedPackageOptions{Enabled: true, After: 15 * time.Minute})
			chatID := "77070000009-" + strings.ReplaceAll(tt.name, " ", "-") + "@c.us"
			now := time.Now().UTC()
			store.Update(chatID, func(conversation *Conversation) {
				conversation.HistoryCheckedAt = now.Add(-20 * time.Minute)
				conversation.HistoryClassification = HistoryClassificationNewClient
				conversation.Stage = ClientStateAwaitingQualification
				conversation.InitialMessageSent = true
				conversation.InitialGreetingSentAt = now.Add(-16 * time.Minute)
				conversation.LastReplyAt = now.Add(-16 * time.Minute)
				conversation.LastIncomingAt = now.Add(-17 * time.Minute)
				tt.mark(conversation)
			})

			if err := service.ProcessDueDelayedPackages(context.Background(), now); err != nil {
				t.Fatalf("ProcessDueDelayedPackages() error = %v", err)
			}
			if len(sender.files) != 0 {
				t.Fatalf("closed/stopped chat got files: %#v", sender.files)
			}
		})
	}
}
