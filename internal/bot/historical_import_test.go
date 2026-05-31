package bot

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeHistoricalWhatsAppPhone(t *testing.T) {
	tests := []struct {
		raw      string
		wantChat string
	}{
		{raw: "+7 702 639 4092", wantChat: "77026394092@c.us"},
		{raw: "87026394092", wantChat: "77026394092@c.us"},
		{raw: "7 705 435 3684", wantChat: "77054353684@c.us"},
		{raw: "7077300095", wantChat: "77077300095@c.us"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			_, chatID, ok := NormalizeHistoricalWhatsAppPhone(tt.raw)
			if !ok || chatID != tt.wantChat {
				t.Fatalf("NormalizeHistoricalWhatsAppPhone(%q) = %q ok=%v, want %q", tt.raw, chatID, ok, tt.wantChat)
			}
		})
	}
}

func TestHistoricalContactsCSVFromSheetExport(t *testing.T) {
	result, err := LoadHistoricalClosedContactsCSV(filepath.Join("..", "..", "imports", "historical_closed_contacts.csv"))
	if err != nil {
		t.Fatalf("LoadHistoricalClosedContactsCSV() error = %v", err)
	}
	if len(result.Contacts) != 32 {
		t.Fatalf("contacts = %d, want 32", len(result.Contacts))
	}
	if len(result.Invalid) != 0 || len(result.Duplicates) != 0 {
		t.Fatalf("invalid=%#v duplicates=%#v, want none", result.Invalid, result.Duplicates)
	}
}

func TestHistoricalImportDuplicatesOnlyCreateOneClient(t *testing.T) {
	csvText := "phone_raw\n+7 702 639 4092\n87026394092\n"
	result, err := ReadHistoricalClosedContactsCSV(strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("ReadHistoricalClosedContactsCSV() error = %v", err)
	}
	if len(result.Contacts) != 1 || len(result.Duplicates) != 1 {
		t.Fatalf("contacts=%d duplicates=%d, want 1/1", len(result.Contacts), len(result.Duplicates))
	}

	store := newSQLiteStoreForImportTest(t)
	summary, err := store.ImportHistoricalClosedContacts(context.Background(), result.Contacts, HistoricalClosedImportSource)
	if err != nil {
		t.Fatalf("ImportHistoricalClosedContacts() error = %v", err)
	}
	if summary.Inserted != 1 {
		t.Fatalf("inserted = %d, want 1", summary.Inserted)
	}
	if got := clientCount(t, store.db, "77026394092@c.us"); got != 1 {
		t.Fatalf("client rows = %d, want 1", got)
	}
}

func TestImportedHistoricalClosedContactStaysSilent(t *testing.T) {
	store := newSQLiteStoreForImportTest(t)
	contacts := []HistoricalClosedContact{mustHistoricalContact(t, "+7 702 639 4092")}
	if _, err := store.ImportHistoricalClosedContacts(context.Background(), contacts, HistoricalClosedImportSource); err != nil {
		t.Fatalf("ImportHistoricalClosedContacts() error = %v", err)
	}
	sender := &fakeSender{}
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))

	sendText(t, service, "77026394092@c.us", "Здравствуйте")
	sendText(t, service, "77026394092@c.us", "Хочу заказать ролик")

	if len(sender.messages) != 0 {
		t.Fatalf("imported closed contact received automation: %#v", sender.messages)
	}
	conversation := snapshotConversation(t, store, "77026394092@c.us")
	if conversation.LastIncomingText != "Хочу заказать ролик" {
		t.Fatalf("incoming history was not saved: %q", conversation.LastIncomingText)
	}
}

func TestUnknownContactStillStartsNormalFlowAfterHistoricalImport(t *testing.T) {
	store := newSQLiteStoreForImportTest(t)
	contacts := []HistoricalClosedContact{mustHistoricalContact(t, "+7 702 639 4092")}
	if _, err := store.ImportHistoricalClosedContacts(context.Background(), contacts, HistoricalClosedImportSource); err != nil {
		t.Fatalf("ImportHistoricalClosedContacts() error = %v", err)
	}
	sender := &fakeSender{}
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))

	sendText(t, service, "77029990000@c.us", "Здравствуйте")

	if len(sender.messages) != 1 || sender.messages[0] != QualificationGreetingText("ru") {
		t.Fatalf("new contact flow changed: %#v", sender.messages)
	}
}

func TestHistoricalImportUpdatesExistingNeutralClientWithoutDuplicate(t *testing.T) {
	store := newSQLiteStoreForImportTest(t)
	chatID := "77026394092@c.us"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.DisplayName = "Existing"
	})

	summary, err := store.ImportHistoricalClosedContacts(context.Background(), []HistoricalClosedContact{mustHistoricalContact(t, "+7 702 639 4092")}, HistoricalClosedImportSource)
	if err != nil {
		t.Fatalf("ImportHistoricalClosedContacts() error = %v", err)
	}
	if summary.Updated != 1 || summary.Inserted != 0 {
		t.Fatalf("summary inserted=%d updated=%d, want 0/1", summary.Inserted, summary.Updated)
	}
	if got := clientCount(t, store.db, chatID); got != 1 {
		t.Fatalf("client rows = %d, want 1", got)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateLegacyProcessed || !conversation.DoNotAutoStart || !conversation.AutomationClosed {
		t.Fatalf("existing neutral client not tagged as historical closed: stage=%q do_not=%v closed=%v", conversation.Stage, conversation.DoNotAutoStart, conversation.AutomationClosed)
	}
}

func TestHistoricalImportPreservesExistingOptOut(t *testing.T) {
	store := newSQLiteStoreForImportTest(t)
	chatID := "77026394092@c.us"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateOptOut
		conversation.OptOut = true
		conversation.Stopped = true
		conversation.Lead.LeadStatus = LeadStatusMuted
		conversation.LeadStatus = LeadStatusMuted
	})

	summary, err := store.ImportHistoricalClosedContacts(context.Background(), []HistoricalClosedContact{mustHistoricalContact(t, "+7 702 639 4092")}, HistoricalClosedImportSource)
	if err != nil {
		t.Fatalf("ImportHistoricalClosedContacts() error = %v", err)
	}
	if summary.PreservedOptOut != 1 {
		t.Fatalf("preserved opt-out = %d, want 1", summary.PreservedOptOut)
	}
	conversation := snapshotConversation(t, store, chatID)
	if !conversation.OptOut || conversation.Stage != ClientStateOptOut {
		t.Fatalf("opt-out was not preserved: stage=%q optout=%v", conversation.Stage, conversation.OptOut)
	}
}

func newSQLiteStoreForImportTest(t *testing.T) *ConversationStore {
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

func mustHistoricalContact(t *testing.T, raw string) HistoricalClosedContact {
	t.Helper()
	phone, chatID, ok := NormalizeHistoricalWhatsAppPhone(raw)
	if !ok {
		t.Fatalf("invalid test phone %q", raw)
	}
	return HistoricalClosedContact{Row: 2, RawPhone: raw, Phone: phone, ChatID: chatID}
}

func clientCount(t *testing.T, db *sql.DB, chatID string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM whatsapp_clients WHERE chat_id = ?`, chatID).Scan(&count); err != nil {
		t.Fatalf("count clients: %v", err)
	}
	return count
}
