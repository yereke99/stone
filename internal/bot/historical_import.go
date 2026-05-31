package bot

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const HistoricalClosedImportSource = "google_sheet_historical_closed_2026_05_31"

type HistoricalClosedContact struct {
	Row      int
	RawPhone string
	Phone    string
	ChatID   string
	Name     string
	Status   string
	Comment  string
	Metadata map[string]string
}

type HistoricalClosedInvalidRow struct {
	Row      int
	RawPhone string
	Reason   string
}

type HistoricalClosedDuplicate struct {
	Row      int
	FirstRow int
	RawPhone string
	ChatID   string
}

type HistoricalClosedContactsParseResult struct {
	TotalRows  int
	Contacts   []HistoricalClosedContact
	Invalid    []HistoricalClosedInvalidRow
	Duplicates []HistoricalClosedDuplicate
}

type HistoricalClosedImportSummary struct {
	Source          string
	TotalRows       int
	UniqueContacts  int
	Inserted        int
	Updated         int
	SkippedActive   int
	PreservedOptOut int
	Invalid         []HistoricalClosedInvalidRow
	Duplicates      []HistoricalClosedDuplicate
}

type historicalExistingClient struct {
	ChatID                    string
	Phone                     string
	DisplayName               string
	FirstSeenAt               string
	CreatedAt                 string
	State                     string
	LeadStatus                string
	InitialMessageSent        bool
	PackagesSent              bool
	QuestionnaireOfferSent    bool
	QuestionnaireSent         bool
	HandedOffToOwner          bool
	AutomationClosed          bool
	Stopped                   bool
	OptOut                    bool
	DoNotAutoStart            bool
	LegacyProcessed           bool
	ConversationJSON          string
	HasMeaningfulConversation bool
}

func LoadHistoricalClosedContactsCSV(path string) (HistoricalClosedContactsParseResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return HistoricalClosedContactsParseResult{}, err
	}
	defer func() {
		_ = file.Close()
	}()
	return ReadHistoricalClosedContactsCSV(file)
}

func ReadHistoricalClosedContactsCSV(reader io.Reader) (HistoricalClosedContactsParseResult, error) {
	csvReader := csv.NewReader(reader)
	csvReader.FieldsPerRecord = -1
	csvReader.TrimLeadingSpace = true

	header, err := csvReader.Read()
	if err != nil {
		if err == io.EOF {
			return HistoricalClosedContactsParseResult{}, nil
		}
		return HistoricalClosedContactsParseResult{}, err
	}
	phoneIndex := historicalPhoneColumnIndex(header)
	nameIndex := historicalColumnIndex(header, []string{"name", "имя", "аты"})
	statusIndex := historicalColumnIndex(header, []string{"status", "статус"})
	commentIndex := historicalColumnIndex(header, []string{"comment", "note", "коммент", "замет", "примеч"})

	result := HistoricalClosedContactsParseResult{}
	seen := make(map[string]HistoricalClosedContact)
	rowNumber := 1
	for {
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, err
		}
		rowNumber++
		if emptyCSVRecord(record) {
			continue
		}
		result.TotalRows++
		rawPhone := csvValue(record, phoneIndex)
		phone, chatID, ok := NormalizeHistoricalWhatsAppPhone(rawPhone)
		if !ok {
			result.Invalid = append(result.Invalid, HistoricalClosedInvalidRow{
				Row:      rowNumber,
				RawPhone: rawPhone,
				Reason:   "invalid_phone",
			})
			continue
		}
		if first, exists := seen[chatID]; exists {
			result.Duplicates = append(result.Duplicates, HistoricalClosedDuplicate{
				Row:      rowNumber,
				FirstRow: first.Row,
				RawPhone: rawPhone,
				ChatID:   chatID,
			})
			continue
		}
		contact := HistoricalClosedContact{
			Row:      rowNumber,
			RawPhone: rawPhone,
			Phone:    phone,
			ChatID:   chatID,
			Name:     csvValue(record, nameIndex),
			Status:   csvValue(record, statusIndex),
			Comment:  csvValue(record, commentIndex),
			Metadata: historicalMetadata(header, record),
		}
		seen[chatID] = contact
		result.Contacts = append(result.Contacts, contact)
	}
	return result, nil
}

func NormalizeHistoricalWhatsAppPhone(value string) (phone string, chatID string, ok bool) {
	var digits strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	phone = digits.String()
	switch {
	case len(phone) == 10:
		phone = "7" + phone
	case len(phone) == 11 && strings.HasPrefix(phone, "8"):
		phone = "7" + phone[1:]
	case len(phone) == 11 && strings.HasPrefix(phone, "7"):
	default:
		return "", "", false
	}
	if len(phone) != 11 || !strings.HasPrefix(phone, "7") {
		return "", "", false
	}
	return phone, phone + "@c.us", true
}

func (s *ConversationStore) ImportHistoricalClosedContacts(ctx context.Context, contacts []HistoricalClosedContact, source string) (HistoricalClosedImportSummary, error) {
	if source = strings.TrimSpace(source); source == "" {
		source = HistoricalClosedImportSource
	}
	summary := HistoricalClosedImportSummary{
		Source:         source,
		TotalRows:      len(contacts),
		UniqueContacts: len(contacts),
	}
	if len(contacts) == 0 {
		return summary, nil
	}
	if s == nil || s.db == nil {
		return s.importHistoricalClosedContactsInMemory(contacts, source, summary), nil
	}
	if err := ctx.Err(); err != nil {
		return summary, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return summary, fmt.Errorf("begin historical import transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	now := time.Now().UTC()
	for _, contact := range contacts {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		existing, exists, err := loadHistoricalExistingClient(ctx, tx, contact.ChatID)
		if err != nil {
			return summary, err
		}
		if exists && shouldSkipHistoricalImportUpdate(existing) {
			summary.SkippedActive++
			continue
		}
		if existing.OptOut {
			summary.PreservedOptOut++
		}
		conversation, err := historicalClosedConversation(contact, source, now, existing, exists)
		if err != nil {
			return summary, err
		}
		rawConversation, err := json.Marshal(conversation)
		if err != nil {
			return summary, fmt.Errorf("marshal imported conversation %s: %w", contact.ChatID, err)
		}
		if exists {
			if err := updateHistoricalClosedClient(ctx, tx, contact, source, now, conversation, string(rawConversation), existing); err != nil {
				return summary, err
			}
			summary.Updated++
			continue
		}
		if err := insertHistoricalClosedClient(ctx, tx, contact, source, now, conversation, string(rawConversation)); err != nil {
			return summary, err
		}
		summary.Inserted++
	}

	if err := tx.Commit(); err != nil {
		return summary, fmt.Errorf("commit historical import transaction: %w", err)
	}
	if err := s.loadConversations(ctx); err != nil {
		return summary, err
	}
	return summary, nil
}

func (s *ConversationStore) importHistoricalClosedContactsInMemory(contacts []HistoricalClosedContact, source string, summary HistoricalClosedImportSummary) HistoricalClosedImportSummary {
	now := time.Now().UTC()
	for _, contact := range contacts {
		existing, exists := s.conversations[contact.ChatID]
		if exists && shouldSkipHistoricalImportUpdate(historicalExistingFromConversation(*existing)) {
			summary.SkippedActive++
			continue
		}
		if exists && existing.OptOut {
			summary.PreservedOptOut++
		}
		conversation, err := historicalClosedConversation(contact, source, now, historicalExistingFromConversationPtr(existing), exists)
		if err != nil {
			continue
		}
		s.conversations[contact.ChatID] = &conversation
		if exists {
			summary.Updated++
		} else {
			summary.Inserted++
		}
	}
	return summary
}

func loadHistoricalExistingClient(ctx context.Context, tx *sql.Tx, chatID string) (historicalExistingClient, bool, error) {
	row := tx.QueryRowContext(ctx, `SELECT
		chat_id, phone, display_name, first_seen_at, created_at, state, lead_status,
		initial_message_sent, packages_sent, questionnaire_offer_sent, questionnaire_sent,
		handed_off_to_owner, automation_closed, stopped, opt_out, do_not_auto_start,
		legacy_processed, conversation_json
		FROM whatsapp_clients WHERE chat_id = ?`, chatID)
	var existing historicalExistingClient
	var phone, displayName, firstSeenAt, createdAt, state, leadStatus, rawJSON sql.NullString
	var initialSent, packagesSent, questionnaireOfferSent, questionnaireSent int
	var handedOff, automationClosed, stopped, optOut, doNotAutoStart, legacyProcessed int
	if err := row.Scan(
		&existing.ChatID, &phone, &displayName, &firstSeenAt, &createdAt, &state, &leadStatus,
		&initialSent, &packagesSent, &questionnaireOfferSent, &questionnaireSent,
		&handedOff, &automationClosed, &stopped, &optOut, &doNotAutoStart,
		&legacyProcessed, &rawJSON,
	); err != nil {
		if err == sql.ErrNoRows {
			return historicalExistingClient{}, false, nil
		}
		return historicalExistingClient{}, false, fmt.Errorf("load existing historical client %s: %w", chatID, err)
	}
	existing.Phone = nullString(phone)
	existing.DisplayName = nullString(displayName)
	existing.FirstSeenAt = nullString(firstSeenAt)
	existing.CreatedAt = nullString(createdAt)
	existing.State = nullString(state)
	existing.LeadStatus = nullString(leadStatus)
	existing.InitialMessageSent = initialSent != 0
	existing.PackagesSent = packagesSent != 0
	existing.QuestionnaireOfferSent = questionnaireOfferSent != 0
	existing.QuestionnaireSent = questionnaireSent != 0
	existing.HandedOffToOwner = handedOff != 0
	existing.AutomationClosed = automationClosed != 0
	existing.Stopped = stopped != 0
	existing.OptOut = optOut != 0
	existing.DoNotAutoStart = doNotAutoStart != 0
	existing.LegacyProcessed = legacyProcessed != 0
	existing.ConversationJSON = nullString(rawJSON)
	existing.HasMeaningfulConversation = existing.InitialMessageSent || existing.PackagesSent ||
		existing.QuestionnaireOfferSent || existing.QuestionnaireSent
	return existing, true, nil
}

func shouldSkipHistoricalImportUpdate(existing historicalExistingClient) bool {
	if existing.OptOut || existing.Stopped || existing.AutomationClosed || existing.HandedOffToOwner ||
		existing.DoNotAutoStart || existing.LegacyProcessed {
		return false
	}
	if normalizeLeadStatus(existing.LeadStatus) == LeadStatusClosed ||
		normalizeLeadStatus(existing.LeadStatus) == LeadStatusMuted ||
		normalizeLeadStatus(existing.LeadStatus) == LeadStatusHandoffRequired {
		return false
	}
	switch strings.TrimSpace(existing.State) {
	case "", ClientStateNeutralNew, ClientStateLegacyExisting, ClientStateLegacyProcessed, ClientStateHistoryCheckFailed:
		return false
	case ClientStateHandedOff, ClientStateStopped, ClientStateOptOut:
		return false
	}
	if existing.HasMeaningfulConversation {
		return true
	}
	status := normalizeLeadStatus(existing.LeadStatus)
	return status == LeadStatusWarm || status == LeadStatusHot
}

func historicalClosedConversation(contact HistoricalClosedContact, source string, now time.Time, existing historicalExistingClient, exists bool) (Conversation, error) {
	createdAt := now
	if exists {
		if parsed, ok := parseHistoricalImportTime(existing.CreatedAt); ok {
			createdAt = parsed
		}
	}
	displayName := strings.TrimSpace(contact.Name)
	if displayName == "" && exists {
		displayName = strings.TrimSpace(existing.DisplayName)
	}
	phone := strings.TrimSpace(contact.Phone)
	if phone == "" {
		phone, _, _ = NormalizeHistoricalWhatsAppPhone(contact.RawPhone)
	}
	summary := "Imported from " + source + " as historical processed/closed WhatsApp contact; automation disabled."
	metadataNote := historicalContactMetadataNote(contact, source)
	stage := ClientStateLegacyProcessed
	leadStatus := LeadStatusClosed
	optOut := exists && existing.OptOut
	if optOut {
		stage = ClientStateOptOut
		leadStatus = LeadStatusMuted
	}
	conversation := Conversation{
		ChatID:                contact.ChatID,
		Phone:                 phone,
		DisplayName:           displayName,
		Language:              "ru",
		Stage:                 stage,
		LeadStatus:            leadStatus,
		Lead:                  LeadState{ClientName: displayName, Notes: metadataNote, LeadStatus: leadStatus, HasBeenGreeted: true},
		InitialMessageSent:    true,
		HandedOffToOwner:      !optOut,
		AutomationClosed:      true,
		Stopped:               true,
		OptOut:                optOut,
		DoNotAutoStart:        true,
		LegacyExisting:        true,
		LegacyProcessed:       true,
		HistoryCheckedAt:      now,
		HistoryDetected:       true,
		HistoryClassification: HistoryClassificationLegacyProcessed,
		HistorySummary:        summary,
		ConversationSummary:   summary + " Raw phone: " + strings.TrimSpace(contact.RawPhone),
		CreatedAt:             createdAt,
		UpdatedAt:             now,
		LastUpdated:           now,
	}
	if !optOut {
		conversation.TransferredAt = now
	}
	ensureConversationMaps(&conversation)
	conversation.ConversationSummary = summary + " Raw phone: " + strings.TrimSpace(contact.RawPhone)
	conversation.HistorySummary = summary
	conversation.MissingFields = []string{}
	return conversation, nil
}

func insertHistoricalClosedClient(ctx context.Context, tx *sql.Tx, contact HistoricalClosedContact, source string, now time.Time, conversation Conversation, rawConversation string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO whatsapp_clients (
		chat_id, phone, display_name, first_seen_at, last_seen_at,
		state, lead_status, language, initial_message_sent, portfolio_sent, packages_sent,
		questionnaire_offer_sent, questionnaire_sent, handed_off_to_owner, wants_questionnaire,
		automation_closed, stopped, opt_out, conversation_summary, missing_fields_json,
		transferred_at, history_checked_at, history_detected, history_message_count,
		history_classification, history_summary, do_not_auto_start, legacy_existing,
		legacy_processed, legacy_reengagement, conversation_json, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 0, 0, 0, 0, ?, 0, 1, 1, ?, ?, '[]', ?, ?, 1, 0, ?, ?, 1, 1, 1, 0, ?, ?, ?)`,
		contact.ChatID,
		conversation.Phone,
		conversation.DisplayName,
		timeText(conversation.CreatedAt),
		timeText(now),
		conversation.Stage,
		conversation.LeadStatus,
		conversation.Language,
		boolInt(conversation.HandedOffToOwner),
		boolInt(conversation.OptOut),
		conversation.ConversationSummary,
		timeText(conversation.TransferredAt),
		timeText(now),
		HistoryClassificationLegacyProcessed,
		conversation.HistorySummary,
		rawConversation,
		timeText(conversation.CreatedAt),
		timeText(now),
	)
	if err != nil {
		return fmt.Errorf("insert historical closed client %s: %w", contact.ChatID, err)
	}
	_ = source
	return nil
}

func updateHistoricalClosedClient(ctx context.Context, tx *sql.Tx, contact HistoricalClosedContact, source string, now time.Time, conversation Conversation, rawConversation string, existing historicalExistingClient) error {
	firstSeenAt := existing.FirstSeenAt
	if strings.TrimSpace(firstSeenAt) == "" {
		firstSeenAt = timeText(conversation.CreatedAt)
	}
	displayName := conversation.DisplayName
	if displayName == "" {
		displayName = existing.DisplayName
	}
	_, err := tx.ExecContext(ctx, `UPDATE whatsapp_clients SET
		phone = ?,
		display_name = ?,
		first_seen_at = ?,
		last_seen_at = ?,
		state = ?,
		lead_status = ?,
		language = COALESCE(NULLIF(language, ''), ?),
		initial_message_sent = 1,
		handed_off_to_owner = ?,
		automation_closed = 1,
		stopped = 1,
		opt_out = CASE WHEN opt_out = 1 THEN 1 ELSE ? END,
		conversation_summary = ?,
		missing_fields_json = '[]',
		transferred_at = ?,
		history_checked_at = ?,
		history_detected = 1,
		history_classification = ?,
		history_summary = ?,
		do_not_auto_start = 1,
		legacy_existing = 1,
		legacy_processed = 1,
		legacy_reengagement = 0,
		conversation_json = ?,
		updated_at = ?
		WHERE chat_id = ?`,
		conversation.Phone,
		displayName,
		firstSeenAt,
		timeText(now),
		conversation.Stage,
		conversation.LeadStatus,
		conversation.Language,
		boolInt(conversation.HandedOffToOwner),
		boolInt(conversation.OptOut),
		conversation.ConversationSummary,
		timeText(conversation.TransferredAt),
		timeText(now),
		HistoryClassificationLegacyProcessed,
		conversation.HistorySummary,
		rawConversation,
		timeText(now),
		contact.ChatID,
	)
	if err != nil {
		return fmt.Errorf("update historical closed client %s: %w", contact.ChatID, err)
	}
	_ = source
	return nil
}

func historicalPhoneColumnIndex(header []string) int {
	index := historicalColumnIndex(header, []string{"phone", "тел", "номер", "номера", "whatsapp", "wa"})
	if index >= 0 {
		return index
	}
	return 0
}

func historicalColumnIndex(header []string, needles []string) int {
	for i, value := range header {
		normalized := normalizeForAnalysis(value)
		for _, needle := range needles {
			if strings.Contains(normalized, normalizeForAnalysis(needle)) {
				return i
			}
		}
	}
	return -1
}

func historicalMetadata(header []string, record []string) map[string]string {
	metadata := make(map[string]string)
	for i, name := range header {
		name = strings.TrimSpace(name)
		if name == "" {
			name = fmt.Sprintf("column_%d", i+1)
		}
		if value := csvValue(record, i); value != "" {
			metadata[name] = value
		}
	}
	return metadata
}

func historicalContactMetadataNote(contact HistoricalClosedContact, source string) string {
	parts := []string{"import_source=" + source, "phone_raw=" + strings.TrimSpace(contact.RawPhone)}
	if contact.Status != "" {
		parts = append(parts, "status="+contact.Status)
	}
	if contact.Comment != "" {
		parts = append(parts, "comment="+contact.Comment)
	}
	return strings.Join(parts, "; ")
}

func historicalExistingFromConversation(conversation Conversation) historicalExistingClient {
	return historicalExistingClient{
		ChatID:                 conversation.ChatID,
		Phone:                  conversation.Phone,
		DisplayName:            conversation.DisplayName,
		State:                  conversation.Stage,
		LeadStatus:             conversation.LeadStatus,
		InitialMessageSent:     conversation.InitialMessageSent,
		PackagesSent:           conversation.PackagesSent,
		QuestionnaireOfferSent: conversation.QuestionnaireOfferSent,
		QuestionnaireSent:      conversation.QuestionnaireSent,
		HandedOffToOwner:       conversation.HandedOffToOwner,
		AutomationClosed:       conversation.AutomationClosed,
		Stopped:                conversation.Stopped,
		OptOut:                 conversation.OptOut,
		DoNotAutoStart:         conversation.DoNotAutoStart,
		LegacyProcessed:        conversation.LegacyProcessed,
		HasMeaningfulConversation: conversation.InitialMessageSent || conversation.PackagesSent ||
			conversation.QuestionnaireOfferSent || conversation.QuestionnaireSent,
	}
}

func historicalExistingFromConversationPtr(conversation *Conversation) historicalExistingClient {
	if conversation == nil {
		return historicalExistingClient{}
	}
	return historicalExistingFromConversation(*conversation)
}

func parseHistoricalImportTime(value string) (time.Time, bool) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	return parsed, err == nil
}

func emptyCSVRecord(record []string) bool {
	for _, value := range record {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}

func csvValue(record []string, index int) string {
	if index < 0 || index >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[index])
}

func nullString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return strings.TrimSpace(value.String)
}
