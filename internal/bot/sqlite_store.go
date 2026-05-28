package bot

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type WhatsAppMessageLog struct {
	ChatID            string
	DisplayName       string
	GreenAPIMessageID string
	DedupeKey         string
	Direction         string
	MessageType       string
	Text              string
	RawPayloadJSON    string
	ProcessedAt       time.Time
}

func NewSQLiteConversationStore(ctx context.Context, path string) (*ConversationStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}
	if err := ensureSQLiteDir(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}
	db.SetMaxOpenConns(1)

	store := NewConversationStore()
	store.db = db
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.loadConversations(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *ConversationStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func ensureSQLiteDir(path string) error {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create sqlite directory: %w", err)
	}
	return nil
}

func (s *ConversationStore) migrate(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	statements := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA busy_timeout = 5000;`,
		`CREATE TABLE IF NOT EXISTS whatsapp_clients (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id TEXT NOT NULL UNIQUE,
			phone TEXT,
			display_name TEXT,
			first_seen_at TEXT,
			last_seen_at TEXT,
			last_incoming_at TEXT,
			last_outgoing_at TEXT,
			state TEXT NOT NULL DEFAULT 'neutral_new',
			lead_status TEXT NOT NULL DEFAULT 'neutral',
			language TEXT,
			niche TEXT,
			goal TEXT,
			deadline TEXT,
			selected_package TEXT,
			initial_message_sent INTEGER NOT NULL DEFAULT 0,
			portfolio_sent INTEGER NOT NULL DEFAULT 0,
			packages_sent INTEGER NOT NULL DEFAULT 0,
			questionnaire_offer_sent INTEGER NOT NULL DEFAULT 0,
			questionnaire_sent INTEGER NOT NULL DEFAULT 0,
			handed_off_to_owner INTEGER NOT NULL DEFAULT 0,
			wants_questionnaire INTEGER NOT NULL DEFAULT 0,
			automation_closed INTEGER NOT NULL DEFAULT 0,
			stopped INTEGER NOT NULL DEFAULT 0,
			opt_out INTEGER NOT NULL DEFAULT 0,
			last_processed_message_id TEXT,
			conversation_summary TEXT,
			missing_fields_json TEXT,
			transferred_at TEXT,
			conversation_json TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS whatsapp_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id TEXT NOT NULL,
			green_api_message_id TEXT,
			dedupe_key TEXT NOT NULL,
			direction TEXT NOT NULL,
			message_type TEXT,
			text TEXT,
			raw_payload_json TEXT,
			processed_at TEXT,
			created_at TEXT NOT NULL
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_whatsapp_messages_dedupe_key ON whatsapp_messages(dedupe_key);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_whatsapp_messages_green_api_message_id
			ON whatsapp_messages(green_api_message_id)
			WHERE green_api_message_id IS NOT NULL AND green_api_message_id <> '';`,
		`CREATE INDEX IF NOT EXISTS idx_whatsapp_messages_chat_created ON whatsapp_messages(chat_id, created_at);`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("run sqlite migration: %w", err)
		}
	}
	if err := s.ensureWhatsAppClientColumns(ctx); err != nil {
		return err
	}
	return nil
}

func (s *ConversationStore) ensureWhatsAppClientColumns(ctx context.Context) error {
	columns := map[string]string{
		"wants_questionnaire":  "INTEGER NOT NULL DEFAULT 0",
		"automation_closed":    "INTEGER NOT NULL DEFAULT 0",
		"conversation_summary": "TEXT",
		"missing_fields_json":  "TEXT",
		"transferred_at":       "TEXT",
	}

	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(whatsapp_clients)`)
	if err != nil {
		return fmt.Errorf("inspect whatsapp_clients columns: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	existing := make(map[string]bool, len(columns))
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan whatsapp_clients column: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate whatsapp_clients columns: %w", err)
	}

	for name, definition := range columns {
		if existing[name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE whatsapp_clients ADD COLUMN `+name+` `+definition); err != nil {
			return fmt.Errorf("add whatsapp_clients.%s column: %w", name, err)
		}
	}
	return nil
}

func (s *ConversationStore) loadConversations(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT chat_id, conversation_json FROM whatsapp_clients`)
	if err != nil {
		return fmt.Errorf("load conversations: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	for rows.Next() {
		var chatID string
		var raw sql.NullString
		if err := rows.Scan(&chatID, &raw); err != nil {
			return fmt.Errorf("scan conversation: %w", err)
		}
		chatID = strings.TrimSpace(chatID)
		if chatID == "" {
			continue
		}
		conversation := &Conversation{ChatID: chatID}
		if raw.Valid && strings.TrimSpace(raw.String) != "" {
			if err := json.Unmarshal([]byte(raw.String), conversation); err != nil {
				conversation = &Conversation{ChatID: chatID}
			}
		}
		conversation.ChatID = chatID
		if conversation.CreatedAt.IsZero() {
			conversation.CreatedAt = now
		}
		if conversation.UpdatedAt.IsZero() {
			conversation.UpdatedAt = conversation.CreatedAt
		}
		if conversation.LastUpdated.IsZero() {
			conversation.LastUpdated = conversation.UpdatedAt
		}
		ensureConversationMaps(conversation)
		if conversation.Phone == "" {
			conversation.Phone = phoneFromChatID(chatID)
		}
		if conversation.Stage == "" {
			conversation.Stage = ClientStateNeutralNew
		}
		s.conversations[chatID] = conversation
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate conversations: %w", err)
	}
	return nil
}

func (s *ConversationStore) persistConversationLocked(ctx context.Context, conversation *Conversation) error {
	if s == nil || s.db == nil || conversation == nil || strings.TrimSpace(conversation.ChatID) == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ensureConversationMaps(conversation)
	now := time.Now().UTC()
	if conversation.CreatedAt.IsZero() {
		conversation.CreatedAt = now
	}
	if conversation.UpdatedAt.IsZero() {
		conversation.UpdatedAt = now
	}
	if conversation.LastUpdated.IsZero() {
		conversation.LastUpdated = conversation.UpdatedAt
	}
	if conversation.Phone == "" {
		conversation.Phone = phoneFromChatID(conversation.ChatID)
	}

	state := clientStateForConversation(conversation)
	leadStatus := normalizeLeadStatus(conversation.LeadStatus)
	if leadStatus == "" {
		leadStatus = normalizeLeadStatus(conversation.Lead.LeadStatus)
	}
	if leadStatus == "" {
		leadStatus = LeadStatusNeutral
	}

	raw, err := json.Marshal(conversation)
	if err != nil {
		return fmt.Errorf("marshal conversation: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `INSERT INTO whatsapp_clients (
			chat_id, phone, display_name, first_seen_at, last_seen_at, last_incoming_at, last_outgoing_at,
			state, lead_status, language, niche, goal, deadline, selected_package,
			initial_message_sent, portfolio_sent, packages_sent, questionnaire_offer_sent, questionnaire_sent,
			handed_off_to_owner, wants_questionnaire, automation_closed, stopped, opt_out, last_processed_message_id,
			conversation_summary, missing_fields_json, transferred_at, conversation_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET
			phone = excluded.phone,
			display_name = excluded.display_name,
			last_seen_at = excluded.last_seen_at,
			last_incoming_at = excluded.last_incoming_at,
			last_outgoing_at = excluded.last_outgoing_at,
			state = excluded.state,
			lead_status = excluded.lead_status,
			language = excluded.language,
			niche = excluded.niche,
			goal = excluded.goal,
			deadline = excluded.deadline,
			selected_package = excluded.selected_package,
			initial_message_sent = excluded.initial_message_sent,
			portfolio_sent = excluded.portfolio_sent,
			packages_sent = excluded.packages_sent,
			questionnaire_offer_sent = excluded.questionnaire_offer_sent,
			questionnaire_sent = excluded.questionnaire_sent,
			handed_off_to_owner = excluded.handed_off_to_owner,
			wants_questionnaire = excluded.wants_questionnaire,
			automation_closed = excluded.automation_closed,
			stopped = excluded.stopped,
			opt_out = excluded.opt_out,
			last_processed_message_id = excluded.last_processed_message_id,
			conversation_summary = excluded.conversation_summary,
			missing_fields_json = excluded.missing_fields_json,
			transferred_at = excluded.transferred_at,
			conversation_json = excluded.conversation_json,
			updated_at = excluded.updated_at`,
		conversation.ChatID,
		conversation.Phone,
		strings.TrimSpace(conversation.DisplayName),
		timeText(conversation.CreatedAt),
		timeText(lastSeenAt(conversation)),
		timeText(conversation.LastIncomingAt),
		timeText(conversation.LastReplyAt),
		state,
		leadStatus,
		normalizeLanguageCode(conversation.Language),
		strings.TrimSpace(conversation.Lead.Niche),
		strings.TrimSpace(conversation.Lead.Goal),
		strings.TrimSpace(conversation.Lead.Deadline),
		strings.TrimSpace(conversation.Lead.SelectedPackage),
		boolInt(conversation.InitialMessageSent || conversation.Lead.HasBeenGreeted),
		boolInt(conversation.SentPortfolio || conversation.Lead.PortfolioSent),
		boolInt(conversation.PackagesSent || conversation.Lead.OfferSent),
		boolInt(conversation.QuestionnaireOfferSent),
		boolInt(conversation.QuestionnaireSent),
		boolInt(conversation.HandedOffToOwner || !conversation.AdminNotifiedAt.IsZero() || !conversation.AdminOperatorNotifiedAt.IsZero()),
		boolInt(conversation.WantsQuestionnaire || conversation.Lead.WantsQuestionnaire),
		boolInt(conversation.AutomationClosed),
		boolInt(conversation.Stopped),
		boolInt(conversation.OptOut),
		strings.TrimSpace(conversation.LastProcessedMessageID),
		strings.TrimSpace(conversation.ConversationSummary),
		missingFieldsJSONString(conversation.MissingFields),
		timeText(conversation.TransferredAt),
		string(raw),
		timeText(conversation.CreatedAt),
		timeText(conversation.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("persist conversation: %w", err)
	}
	return nil
}

func (s *ConversationStore) BeginIncomingMessageProcessing(ctx context.Context, log WhatsAppMessageLog) (MessageDedupeDecision, error) {
	log.DedupeKey = strings.TrimSpace(log.DedupeKey)
	if log.DedupeKey == "" {
		log.DedupeKey = strings.TrimSpace(log.GreenAPIMessageID)
	}
	if log.DedupeKey == "" {
		return MessageDedupeNoID, nil
	}
	if s.db == nil {
		return s.BeginMessageProcessing(ctx, log.DedupeKey)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	if _, exists := s.processed[log.DedupeKey]; exists {
		return MessageDedupeDuplicate, nil
	}
	if _, exists := s.processing[log.DedupeKey]; exists {
		return MessageDedupeInFlight, nil
	}

	processed, err := s.messageAlreadyProcessed(ctx, log)
	if err != nil {
		return "", err
	}
	if processed {
		s.processed[log.DedupeKey] = now
		return MessageDedupeDuplicate, nil
	}

	conversation := s.getOrCreateLocked(log.ChatID, now)
	if displayName := strings.TrimSpace(log.DisplayName); displayName != "" {
		conversation.DisplayName = displayName
	}
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	if err := s.persistConversationLocked(ctx, conversation); err != nil {
		return "", err
	}
	if err := s.upsertIncomingMessage(ctx, log, now); err != nil {
		return "", err
	}
	s.processing[log.DedupeKey] = now
	return MessageDedupeNew, nil
}

func (s *ConversationStore) messageAlreadyProcessed(ctx context.Context, log WhatsAppMessageLog) (bool, error) {
	var processedAt sql.NullString
	var queryErr error
	if strings.TrimSpace(log.GreenAPIMessageID) != "" {
		queryErr = s.db.QueryRowContext(ctx, `SELECT processed_at FROM whatsapp_messages
			WHERE dedupe_key = ? OR green_api_message_id = ?
			ORDER BY id DESC LIMIT 1`, log.DedupeKey, strings.TrimSpace(log.GreenAPIMessageID)).Scan(&processedAt)
	} else {
		queryErr = s.db.QueryRowContext(ctx, `SELECT processed_at FROM whatsapp_messages
			WHERE dedupe_key = ?
			ORDER BY id DESC LIMIT 1`, log.DedupeKey).Scan(&processedAt)
	}
	if queryErr == sql.ErrNoRows {
		return false, nil
	}
	if queryErr != nil {
		return false, fmt.Errorf("check message dedupe: %w", queryErr)
	}
	return processedAt.Valid && strings.TrimSpace(processedAt.String) != "", nil
}

func (s *ConversationStore) upsertIncomingMessage(ctx context.Context, log WhatsAppMessageLog, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO whatsapp_messages (
			chat_id, green_api_message_id, dedupe_key, direction, message_type, text, raw_payload_json, processed_at, created_at
		) VALUES (?, ?, ?, 'incoming', ?, ?, ?, NULL, ?)
		ON CONFLICT(dedupe_key) DO UPDATE SET
			chat_id = excluded.chat_id,
			green_api_message_id = excluded.green_api_message_id,
			message_type = excluded.message_type,
			text = excluded.text,
			raw_payload_json = excluded.raw_payload_json`,
		strings.TrimSpace(log.ChatID),
		strings.TrimSpace(log.GreenAPIMessageID),
		strings.TrimSpace(log.DedupeKey),
		strings.TrimSpace(log.MessageType),
		strings.TrimSpace(log.Text),
		strings.TrimSpace(log.RawPayloadJSON),
		timeText(now),
	)
	if err != nil {
		return fmt.Errorf("log incoming message: %w", err)
	}
	return nil
}

func (s *ConversationStore) markMessageProcessedLocked(ctx context.Context, dedupeKey string, processedAt time.Time) error {
	if s == nil || s.db == nil || strings.TrimSpace(dedupeKey) == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE whatsapp_messages SET processed_at = ? WHERE dedupe_key = ?`,
		timeText(processedAt), strings.TrimSpace(dedupeKey)); err != nil {
		return fmt.Errorf("mark message processed: %w", err)
	}
	return nil
}

func (s *ConversationStore) LogOutgoingMessage(ctx context.Context, chatID string, messageType string, text string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	chatID = strings.TrimSpace(chatID)
	text = strings.TrimSpace(text)
	if chatID == "" || text == "" {
		return nil
	}
	now := time.Now().UTC()
	dedupeKey := outgoingDedupeKey(chatID, messageType, text, now)
	_, err := s.db.ExecContext(ctx, `INSERT INTO whatsapp_messages (
			chat_id, green_api_message_id, dedupe_key, direction, message_type, text, raw_payload_json, processed_at, created_at
		) VALUES (?, '', ?, 'outgoing', ?, ?, '', ?, ?)`,
		chatID,
		dedupeKey,
		strings.TrimSpace(messageType),
		text,
		timeText(now),
		timeText(now),
	)
	if err != nil {
		return fmt.Errorf("log outgoing message: %w", err)
	}
	return nil
}

func clientStateForConversation(conversation *Conversation) string {
	if conversation == nil {
		return ClientStateNeutralNew
	}
	if conversation.OptOut {
		return ClientStateOptOut
	}
	state := strings.TrimSpace(conversation.Stage)
	switch state {
	case ClientStateNeutralNew,
		ClientStateAwaitingQualification,
		ClientStatePackagesPresented,
		ClientStateAwaitingQuestionnaireConfirm,
		ClientStateHandedOff,
		ClientStateStopped,
		ClientStateOptOut:
		return state
	}
	if conversation.AutomationClosed || conversation.HandedOffToOwner || !conversation.TransferredAt.IsZero() {
		return ClientStateHandedOff
	}
	if conversation.Stopped {
		return ClientStateStopped
	}
	if conversation.QuestionnaireOfferSent {
		return ClientStateAwaitingQuestionnaireConfirm
	}
	if conversation.PackagesSent || conversation.Lead.OfferSent || conversation.SentPortfolio || conversation.Lead.PortfolioSent {
		return ClientStatePackagesPresented
	}
	if conversation.InitialMessageSent || conversation.Lead.HasBeenGreeted {
		return ClientStateAwaitingQualification
	}
	return ClientStateNeutralNew
}

func lastSeenAt(conversation *Conversation) time.Time {
	if conversation == nil {
		return time.Time{}
	}
	last := conversation.UpdatedAt
	for _, candidate := range []time.Time{conversation.LastIncomingAt, conversation.LastReplyAt, conversation.LastUpdated} {
		if candidate.After(last) {
			last = candidate
		}
	}
	return last
}

func timeText(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func outgoingDedupeKey(chatID string, messageType string, text string, createdAt time.Time) string {
	sum := sha256.Sum256([]byte(chatID + "|" + messageType + "|" + text))
	return fmt.Sprintf("out|%s|%d|%s", chatID, createdAt.UnixNano(), hex.EncodeToString(sum[:])[:16])
}
