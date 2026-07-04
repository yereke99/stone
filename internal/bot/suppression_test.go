package bot

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "chat id", raw: "77012357383@c.us", want: "77012357383"},
		{name: "formatted kz phone", raw: "+7 (701) 235-73-83", want: "77012357383"},
		{name: "leading eight eleven digits", raw: "87012357383", want: "77012357383"},
		{name: "leading eight ten digits", raw: "8708988877", want: "7708988877"},
		{name: "protected formatted", raw: "+7 776 600 1170", want: "77766001170"},
		{name: "protected local eight", raw: "8 776 600 1170", want: "77766001170"},
		{name: "protected normalized", raw: "77766001170", want: "77766001170"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizePhone(tt.raw); got != tt.want {
				t.Fatalf("NormalizePhone(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestSuppressionMigrationSeedsManualWhatsAppContacts(t *testing.T) {
	store := newSQLiteStoreForSuppressionTest(t)

	tests := []struct {
		name   string
		chatID string
		phone  string
	}{
		{name: "first chat id", chatID: "77012357383@c.us"},
		{name: "second chat id", chatID: "7708988877@c.us"},
		{name: "third chat id", chatID: "77773000200@c.us"},
		{name: "protected first", chatID: "77766001170@c.us"},
		{name: "protected second", chatID: "77054353684@c.us"},
		{name: "protected third", chatID: "77787888325@c.us"},
		{name: "protected fourth", chatID: "77054103913@c.us"},
		{name: "protected fifth", chatID: "77776602066@c.us"},
		{name: "first raw eight", phone: "87012357383"},
		{name: "second raw eight", phone: "8708988877"},
		{name: "third raw eight", phone: "87773000200"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !store.IsSuppressedPhoneOrChatID(tt.chatID, tt.phone) {
				t.Fatalf("IsSuppressedPhoneOrChatID(%q, %q) = false, want true", tt.chatID, tt.phone)
			}
		})
	}
}

func TestSuppressionMigrationIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stone.sqlite3")
	store1, err := NewSQLiteConversationStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first NewSQLiteConversationStore() error = %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	store2, err := NewSQLiteConversationStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second NewSQLiteConversationStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store2.Close()
	})

	var count int
	if err := store2.db.QueryRow(`SELECT COUNT(*) FROM whatsapp_automation_suppression`).Scan(&count); err != nil {
		t.Fatalf("count suppression rows: %v", err)
	}
	if count != 8 {
		t.Fatalf("suppression row count = %d, want 8", count)
	}
}

func TestSuppressedIncomingMessagesDoNotSendBotMessages(t *testing.T) {
	tests := []struct {
		name   string
		chatID string
	}{
		{name: "first chat id", chatID: "77012357383@c.us"},
		{name: "second chat id", chatID: "7708988877@c.us"},
		{name: "third chat id", chatID: "77773000200@c.us"},
		{name: "protected first", chatID: "77766001170@c.us"},
		{name: "protected local first", chatID: "87766001170"},
		{name: "first raw eight", chatID: "87012357383"},
		{name: "second raw eight", chatID: "8708988877"},
		{name: "third raw eight", chatID: "87773000200"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newSQLiteStoreForSuppressionTest(t)
			sender := &fakeSender{}
			ai := &fakeAI{}
			service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil, "77019519013@c.us")
			service.llmReply.Enabled = true

			err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
				IDMessage: "suppressed-" + tt.name,
				ChatID:    tt.chatID,
				Text:      "Здравствуйте, хочу заказать ролик, нужен менеджер",
				Timestamp: time.Now().UTC(),
			})
			if err != nil {
				t.Fatalf("HandleIncomingMessage() error = %v", err)
			}
			if len(sender.messages) != 0 || len(sender.files) != 0 {
				t.Fatalf("suppressed chat got automation: messages=%#v files=%#v", sender.messages, sender.files)
			}
			if ai.analysisCalled {
				t.Fatal("suppressed chat reached AnalyzeCustomerMessage")
			}
			if ai.called {
				t.Fatal("suppressed chat reached GenerateSalesReply")
			}
		})
	}
}

func TestSuppressedFollowupDoesNotSendBotMessages(t *testing.T) {
	store := newSQLiteStoreForSuppressionTest(t)
	sender := &fakeSender{fileMessageIDs: []string{"test-video-id", "basic-video-id", "standard-video-id"}}
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	service.SetDelayedPackageOptions(DelayedPackageOptions{Enabled: true, After: 15 * time.Minute})
	chatID := "77012357383@c.us"
	now := time.Now().UTC()

	store.Update(chatID, func(conversation *Conversation) {
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
		t.Fatalf("suppressed follow-up got automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.FollowupStage != "" || !conversation.NextFollowupAt.IsZero() {
		t.Fatalf("suppressed follow-up was not cleared: stage=%q next=%v", conversation.FollowupStage, conversation.NextFollowupAt)
	}
}

func newSQLiteStoreForSuppressionTest(t *testing.T) *ConversationStore {
	t.Helper()
	store, err := NewSQLiteConversationStore(context.Background(), filepath.Join(t.TempDir(), "stone.sqlite3"))
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}
