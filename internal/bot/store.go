package bot

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultConversationTTL = 0
	defaultMessageTTL      = 24 * time.Hour
	defaultHistoryLimit    = 10
	outgoingRepeatWindow   = 2 * time.Minute
)

type MessageDedupeDecision string

const (
	MessageDedupeNoID      MessageDedupeDecision = "no_id"
	MessageDedupeNew       MessageDedupeDecision = "new"
	MessageDedupeDuplicate MessageDedupeDecision = "duplicate"
	MessageDedupeInFlight  MessageDedupeDecision = "in_flight"
)

type ChatMessage struct {
	Role      string
	Content   string
	CreatedAt time.Time
}

type OutgoingPackageMessage struct {
	PackageKey string
	FileName   string
	Caption    string
	SentAt     time.Time
}

type LeadData = LeadState

type Conversation struct {
	ChatID                  string
	Phone                   string
	DisplayName             string
	Language                string
	Stage                   string
	LeadStatus              string
	Lead                    LeadData
	Messages                []ChatMessage
	ProcessedMessageIDs     map[string]time.Time
	LastIncomingText        string
	LastIncomingAt          time.Time
	LastReplyText           string
	LastReplyAt             time.Time
	AskedFields             map[string]bool
	CompletedFields         map[string]bool
	SentVideos              map[int]bool
	SentVideoFiles          map[string]time.Time
	OutgoingPackageMessages map[string]OutgoingPackageMessage
	SentPortfolio           bool
	BriefAsked              bool
	BriefCollected          bool
	InitialMessageSent      bool
	InitialGreetingSentAt   time.Time
	PackagesSent            bool
	QuestionnaireOfferSent  bool
	QuestionnaireSent       bool
	HandedOffToOwner        bool
	WantsQuestionnaire      bool
	AutomationClosed        bool
	Stopped                 bool
	OptOut                  bool
	StoppedAt               time.Time
	StoppedBy               string
	StopReason              string
	StopMessageID           string
	LastProcessedMessageID  string
	AdminNotifiedAt         time.Time
	AdminOperatorNotifiedAt time.Time
	TransferredAt           time.Time
	ConversationSummary     string
	MissingFields           []string
	SelectedLevel           int
	HistoryCheckedAt        time.Time
	HistoryDetected         bool
	HistoryMessageCount     int
	HistoryClassification   string
	HistorySummary          string
	DoNotAutoStart          bool
	LegacyExisting          bool
	LegacyProcessed         bool
	LegacyReengagement      bool
	AutoPackagesSentAt      time.Time
	NextFollowupAt          time.Time
	FollowupStage           string
	FollowupReferenceAt     time.Time
	LastFollowupSentAt      time.Time
	CreatedAt               time.Time
	UpdatedAt               time.Time
	LastUpdated             time.Time
}

type ConversationStore struct {
	mu              sync.RWMutex
	db              *sql.DB
	conversations   map[string]*Conversation
	processed       map[string]time.Time
	processing      map[string]time.Time
	conversationTTL time.Duration
	messageTTL      time.Duration
	historyLimit    int
	lastCleanupTime time.Time
}

func NewConversationStore() *ConversationStore {
	return &ConversationStore{
		conversations:   make(map[string]*Conversation),
		processed:       make(map[string]time.Time),
		processing:      make(map[string]time.Time),
		conversationTTL: defaultConversationTTL,
		messageTTL:      defaultMessageTTL,
		historyLimit:    defaultHistoryLimit,
	}
}

func (s *ConversationStore) BeginMessageProcessing(ctx context.Context, messageID string) (MessageDedupeDecision, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if messageID == "" {
		return MessageDedupeNoID, nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	if _, exists := s.processed[messageID]; exists {
		return MessageDedupeDuplicate, nil
	}
	if _, exists := s.processing[messageID]; exists {
		return MessageDedupeInFlight, nil
	}
	s.processing[messageID] = now
	return MessageDedupeNew, nil
}

func (s *ConversationStore) FinishMessageProcessing(ctx context.Context, messageID string, success bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if messageID == "" {
		return nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	delete(s.processing, messageID)
	if success {
		s.processed[messageID] = now
		if err := s.markMessageProcessedLocked(ctx, messageID, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationStore) MarkProcessed(ctx context.Context, messageID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if messageID == "" {
		return true, nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	if _, exists := s.processed[messageID]; exists {
		return false, nil
	}
	if _, exists := s.processing[messageID]; exists {
		return false, nil
	}
	s.processed[messageID] = now
	return true, nil
}

func (s *ConversationStore) ConversationExists(ctx context.Context, chatID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.conversations[chatID]
	return exists, nil
}

func (s *ConversationStore) GetOrCreate(chatID string) *Conversation {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	return cloneConversationPtr(s.getOrCreateLocked(chatID, now))
}

func (s *ConversationStore) Update(chatID string, fn func(*Conversation)) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" || fn == nil {
		return
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	fn(conversation)
	conversation.Lead = refreshLeadStatus(conversation.Lead, conversation.Stage)
	conversation.LeadStatus = conversation.Lead.LeadStatus
	conversation.CompletedFields = mergeCompletedFields(conversation.CompletedFields, completedFieldsForLead(conversation.Lead))
	conversation.SentPortfolio = conversation.Lead.PortfolioSent
	conversation.BriefAsked = conversation.Lead.BriefRequested
	conversation.BriefCollected = conversation.Lead.BriefCompleted
	refreshConversationDerivedState(conversation)
	disableGroupConversationAutomation(conversation)
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	_ = s.persistConversationLocked(context.Background(), conversation)
}

func (s *ConversationStore) MarkProcessedMessage(chatID, messageID string) bool {
	chatID = strings.TrimSpace(chatID)
	messageID = strings.TrimSpace(messageID)
	if chatID == "" || messageID == "" {
		return true
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	if _, exists := conversation.ProcessedMessageIDs[messageID]; exists {
		return false
	}
	conversation.ProcessedMessageIDs[messageID] = now
	s.processed[messageID] = now
	conversation.LastProcessedMessageID = messageID
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	_ = s.persistConversationLocked(context.Background(), conversation)
	return true
}

func (s *ConversationStore) IsDuplicateReply(chatID, reply string) bool {
	duplicate, err := s.RecentlySentReply(context.Background(), chatID, strings.TrimSpace(reply), outgoingRepeatWindow)
	return err == nil && duplicate
}

func (s *ConversationStore) MarkIncoming(ctx context.Context, chatID string, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chatID = strings.TrimSpace(chatID)
	text = strings.TrimSpace(text)
	if chatID == "" || text == "" {
		return nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	conversation.LastIncomingText = text
	conversation.LastIncomingAt = now
	conversation.Stopped = conversation.Stopped || conversation.Stage == ClientStateStopped || conversation.Stage == ClientStateOptOut
	refreshConversationDerivedState(conversation)
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) ReopenIncompleteHandoff(ctx context.Context, chatID string) (bool, []string, error) {
	if err := ctx.Err(); err != nil {
		return false, nil, err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false, nil, nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	if conversation.OptOut {
		return false, append([]string(nil), conversation.MissingFields...), nil
	}
	if !hasHandoffClosure(*conversation) {
		return false, append([]string(nil), conversation.MissingFields...), nil
	}
	qualification := managerQualificationForConversation(*conversation)
	if qualification.Ready {
		return false, append([]string(nil), conversation.MissingFields...), nil
	}

	conversation.MissingFields = qualification.Missing
	conversation.HandedOffToOwner = false
	conversation.AutomationClosed = false
	conversation.Stopped = false
	conversation.TransferredAt = time.Time{}
	conversation.Lead.BriefCompleted = false
	conversation.Lead.ContactBriefReady = false
	if normalizeLeadStatus(conversation.LeadStatus) == LeadStatusHandoffRequired {
		conversation.LeadStatus = LeadStatusHot
	}
	if normalizeLeadStatus(conversation.Lead.LeadStatus) == LeadStatusHandoffRequired {
		conversation.Lead.LeadStatus = LeadStatusHot
	}
	conversation.Stage = activeStageForIncompleteHandoff(*conversation)
	refreshConversationDerivedState(conversation)
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	if err := s.persistConversationLocked(ctx, conversation); err != nil {
		return false, nil, err
	}
	return true, append([]string(nil), qualification.Missing...), nil
}

func (s *ConversationStore) MarkAskedFields(ctx context.Context, chatID string, fields []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" || len(fields) == 0 {
		return nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	for _, field := range fields {
		field = normalizeFieldName(field)
		if field != "" {
			conversation.AskedFields[field] = true
		}
	}
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) AdminNotificationSent(ctx context.Context, chatID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false, nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	return !conversation.AdminNotifiedAt.IsZero(), nil
}

func (s *ConversationStore) MarkAdminNotified(ctx context.Context, chatID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	if qualification := managerQualificationForConversation(*conversation); !qualification.Ready {
		conversation.MissingFields = qualification.Missing
		conversation.UpdatedAt = now
		conversation.LastUpdated = now
		return s.persistConversationLocked(ctx, conversation)
	}
	conversation.AdminNotifiedAt = now
	conversation.HandedOffToOwner = true
	conversation.TransferredAt = now
	conversation.AutomationClosed = true
	conversation.Stage = ClientStateHandedOff
	conversation.Stopped = true
	conversation.NextFollowupAt = time.Time{}
	conversation.FollowupStage = ""
	conversation.FollowupReferenceAt = time.Time{}
	conversation.Lead.ContactBriefReady = true
	conversation.Lead.LeadStatus = LeadStatusHandoffRequired
	conversation.LeadStatus = LeadStatusHandoffRequired
	refreshConversationDerivedState(conversation)
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) AdminOperatorNotificationSent(ctx context.Context, chatID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false, nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	return !conversation.AdminOperatorNotifiedAt.IsZero(), nil
}

func (s *ConversationStore) MarkAdminOperatorNotified(ctx context.Context, chatID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	if qualification := managerQualificationForConversation(*conversation); !qualification.Ready {
		conversation.MissingFields = qualification.Missing
		conversation.UpdatedAt = now
		conversation.LastUpdated = now
		return s.persistConversationLocked(ctx, conversation)
	}
	conversation.AdminOperatorNotifiedAt = now
	conversation.HandedOffToOwner = true
	conversation.TransferredAt = now
	conversation.AutomationClosed = true
	conversation.Stage = ClientStateHandedOff
	conversation.Stopped = true
	conversation.NextFollowupAt = time.Time{}
	conversation.FollowupStage = ""
	conversation.FollowupReferenceAt = time.Time{}
	conversation.Lead.ContactBriefReady = true
	conversation.Lead.LeadStatus = LeadStatusHandoffRequired
	conversation.LeadStatus = LeadStatusHandoffRequired
	refreshConversationDerivedState(conversation)
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) AppendMessage(ctx context.Context, chatID string, role string, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chatID == "" || content == "" {
		return nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	conversation.Messages = append(conversation.Messages, ChatMessage{
		Role:      role,
		Content:   content,
		CreatedAt: now,
	})
	if len(conversation.Messages) > s.historyLimit {
		conversation.Messages = conversation.Messages[len(conversation.Messages)-s.historyLimit:]
	}
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) Snapshot(ctx context.Context, chatID string) (Conversation, error) {
	if err := ctx.Err(); err != nil {
		return Conversation{}, err
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	return cloneConversation(*conversation), nil
}

func (s *ConversationStore) ApplyHistoryGuardDecision(ctx context.Context, chatID string, decision HistoryGuardDecision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}

	now := time.Now().UTC()
	if decision.CheckedAt.IsZero() {
		decision.CheckedAt = now
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	classification := normalizeHistoryClassification(decision.Classification)
	conversation.HistoryCheckedAt = decision.CheckedAt.UTC()
	conversation.HistoryDetected = decision.HistoryDetected
	conversation.HistoryMessageCount = decision.HistoryMessageCount
	conversation.HistoryClassification = classification
	conversation.HistorySummary = strings.TrimSpace(decision.Summary)
	conversation.DoNotAutoStart = decision.DoNotAutoStart
	conversation.LegacyExisting = classification == HistoryClassificationLegacyExisting
	conversation.LegacyProcessed = classification == HistoryClassificationLegacyProcessed
	conversation.LegacyReengagement = classification == HistoryClassificationLegacyReengagement
	switch classification {
	case HistoryClassificationLegacyExisting:
		conversation.Stage = ClientStateLegacyExisting
	case HistoryClassificationLegacyProcessed:
		conversation.Stage = ClientStateLegacyProcessed
	case HistoryClassificationHistoryCheckFailed:
		conversation.Stage = ClientStateHistoryCheckFailed
	}
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) MarkAutoPackagesSent(ctx context.Context, chatID string, sentAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	if isConversationClosedForAutomation(*conversation) {
		conversation.NextFollowupAt = time.Time{}
		conversation.FollowupStage = ""
		conversation.FollowupReferenceAt = time.Time{}
		conversation.UpdatedAt = now
		conversation.LastUpdated = now
		return s.persistConversationLocked(ctx, conversation)
	}
	conversation.AutoPackagesSentAt = sentAt.UTC()
	conversation.PackagesSent = true
	conversation.SentPortfolio = true
	conversation.Lead.OfferSent = true
	conversation.Lead.PortfolioSent = true
	conversation.Stage = ClientStatePackagesPresented
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) DelayedPackageCandidates(ctx context.Context, now time.Time, after time.Duration, limit int) ([]Conversation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if after <= 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(now)

	candidates := make([]Conversation, 0)
	for _, conversation := range s.conversations {
		if conversation == nil || isUnsafeCustomerWhatsAppChatID(conversation.ChatID) || !shouldSendDelayedPackages(*conversation, now, after) {
			continue
		}
		candidates = append(candidates, cloneConversation(*conversation))
		if len(candidates) >= limit {
			break
		}
	}
	return candidates, nil
}

func (s *ConversationStore) DueFollowupCandidates(ctx context.Context, now time.Time, limit int) ([]Conversation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if limit <= 0 {
		limit = 100
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(now)

	candidates := make([]Conversation, 0)
	for _, conversation := range s.conversations {
		if conversation == nil || isUnsafeCustomerWhatsAppChatID(conversation.ChatID) || strings.TrimSpace(conversation.FollowupStage) == "" || conversation.NextFollowupAt.IsZero() {
			continue
		}
		if conversation.NextFollowupAt.After(now) {
			continue
		}
		candidates = append(candidates, cloneConversation(*conversation))
		if len(candidates) >= limit {
			break
		}
	}
	return candidates, nil
}

func (s *ConversationStore) ScheduleFollowup(ctx context.Context, chatID string, stage string, dueAt time.Time, referenceAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chatID = strings.TrimSpace(chatID)
	stage = strings.TrimSpace(stage)
	if chatID == "" || stage == "" || dueAt.IsZero() {
		return nil
	}
	if isUnsafeCustomerWhatsAppChatID(chatID) {
		return nil
	}
	if referenceAt.IsZero() {
		referenceAt = time.Now().UTC()
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	if isConversationClosedForAutomation(*conversation) {
		conversation.NextFollowupAt = time.Time{}
		conversation.FollowupStage = ""
		conversation.FollowupReferenceAt = time.Time{}
		conversation.UpdatedAt = now
		conversation.LastUpdated = now
		return s.persistConversationLocked(ctx, conversation)
	}
	conversation.FollowupStage = stage
	conversation.NextFollowupAt = dueAt.UTC()
	conversation.FollowupReferenceAt = referenceAt.UTC()
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) CancelFollowup(ctx context.Context, chatID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	conversation.NextFollowupAt = time.Time{}
	conversation.FollowupStage = ""
	conversation.FollowupReferenceAt = time.Time{}
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) MarkManualStop(ctx context.Context, chatID string, messageID string, stoppedAt time.Time, stoppedBy string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}
	if stoppedAt.IsZero() {
		stoppedAt = time.Now().UTC()
	}
	stoppedBy = strings.TrimSpace(stoppedBy)
	if stoppedBy == "" {
		stoppedBy = StoppedByManualAdmin
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	conversation.Stage = ClientStateStopped
	conversation.Stopped = true
	conversation.AutomationClosed = true
	conversation.StoppedAt = stoppedAt.UTC()
	conversation.StoppedBy = stoppedBy
	conversation.StopReason = StopReasonManualAdminStop
	conversation.StopMessageID = strings.TrimSpace(messageID)
	conversation.NextFollowupAt = time.Time{}
	conversation.FollowupStage = ""
	conversation.FollowupReferenceAt = time.Time{}
	conversation.Lead.LeadStatus = LeadStatusMuted
	conversation.LeadStatus = LeadStatusMuted
	refreshConversationDerivedState(conversation)
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) MarkFollowupSent(ctx context.Context, chatID string, sentStage string, sentAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	conversation.LastFollowupSentAt = sentAt.UTC()
	conversation.FollowupStage = strings.TrimSpace(sentStage)
	conversation.NextFollowupAt = time.Time{}
	conversation.FollowupReferenceAt = sentAt.UTC()
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) UpdateState(ctx context.Context, chatID string, stage string, selectedLevel int) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	requestedStage := strings.TrimSpace(stage)
	if requestedStage != "" {
		conversation.Stage = requestedStage
	}
	if requestedStage == ClientStateAwaitingQualification && conversation.InitialGreetingSentAt.IsZero() {
		conversation.InitialGreetingSentAt = now
	}
	if stageSelectsPackage(requestedStage) && selectedLevel > 0 && selectedLevel <= 3 {
		conversation.SelectedLevel = selectedLevel
		conversation.Lead.SelectedPackage = packageKey(selectedLevel)
	}
	if isHandoffClosingStage(requestedStage) {
		if qualification := managerQualificationForConversation(*conversation); !qualification.Ready {
			conversation.MissingFields = qualification.Missing
			conversation.HandedOffToOwner = false
			conversation.AutomationClosed = false
			conversation.Stopped = false
			conversation.Lead.BriefCompleted = false
			conversation.Lead.ContactBriefReady = false
			if normalizeLeadStatus(conversation.LeadStatus) == LeadStatusHandoffRequired {
				conversation.LeadStatus = LeadStatusHot
			}
			if normalizeLeadStatus(conversation.Lead.LeadStatus) == LeadStatusHandoffRequired {
				conversation.Lead.LeadStatus = LeadStatusHot
			}
			requestedStage = activeStageForIncompleteHandoff(*conversation)
			conversation.Stage = requestedStage
		}
	}
	if isClosingOrMutedStage(requestedStage) {
		conversation.NextFollowupAt = time.Time{}
		conversation.FollowupStage = ""
		conversation.FollowupReferenceAt = time.Time{}
	}
	conversation.Lead = updateLeadFlagsForStage(conversation.Lead, requestedStage)
	updateConversationFlagsForStage(conversation, requestedStage)
	switch requestedStage {
	case StagePortfolioSent, StagePortfolio:
		conversation.Lead.PortfolioSent = true
	case StageOffer, StagePackageSuggested, StageAIExperienceChecked:
		conversation.Lead.OfferSent = true
	case StageBriefRequested:
		conversation.Lead.BriefRequested = true
	case StageBriefCollected:
		conversation.Lead.BriefRequested = true
		conversation.Lead.BriefCompleted = true
		conversation.Lead.ContactBriefReady = true
	}
	conversation.Lead = refreshLeadStatus(conversation.Lead, conversation.Stage)
	conversation.LeadStatus = conversation.Lead.LeadStatus
	conversation.CompletedFields = mergeCompletedFields(conversation.CompletedFields, completedFieldsForLead(conversation.Lead))
	conversation.SentPortfolio = conversation.Lead.PortfolioSent
	conversation.BriefAsked = conversation.Lead.BriefRequested
	conversation.BriefCollected = conversation.Lead.BriefCompleted
	refreshConversationDerivedState(conversation)
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) UpdateLead(ctx context.Context, chatID string, lead LeadState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chatID == "" {
		return nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	conversation.Lead = refreshLeadStatus(cloneLeadState(lead), conversation.Stage)
	conversation.LeadStatus = conversation.Lead.LeadStatus
	conversation.CompletedFields = mergeCompletedFields(conversation.CompletedFields, completedFieldsForLead(conversation.Lead))
	conversation.SentPortfolio = conversation.Lead.PortfolioSent
	conversation.BriefAsked = conversation.Lead.BriefRequested
	conversation.BriefCollected = conversation.Lead.BriefCompleted
	refreshConversationDerivedState(conversation)
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) UpdateLanguage(ctx context.Context, chatID string, language string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chatID == "" || (language != "ru" && language != "kk" && language != "en") {
		return nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	if conversation.Language != "" {
		return nil
	}
	conversation.Language = language
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) RecentlySentReply(ctx context.Context, chatID string, message string, window time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if chatID == "" || message == "" || window <= 0 {
		return false, nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	return conversation.LastReplyText == message && now.Sub(conversation.LastReplyAt) <= window, nil
}

func (s *ConversationStore) MarkReplySent(ctx context.Context, chatID string, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chatID == "" || message == "" {
		return nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	conversation.LastReplyText = message
	conversation.LastReplyAt = now
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) ShouldSendVideo(ctx context.Context, chatID string, fileName string, allowRepeat bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if allowRepeat || chatID == "" || fileName == "" {
		return true, nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	_, exists := conversation.SentVideoFiles[fileName]
	if !exists {
		return true, nil
	}

	return false, nil
}

func (s *ConversationStore) MarkVideoSent(ctx context.Context, chatID string, fileName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chatID == "" || fileName == "" {
		return nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	conversation.SentVideoFiles[fileName] = now
	if level := levelByVideoFile(fileName); level > 0 {
		conversation.SentVideos[level] = true
	}
	conversation.Lead.PortfolioSent = true
	conversation.SentPortfolio = true
	conversation.CompletedFields = completedFieldsForLead(conversation.Lead)
	refreshConversationDerivedState(conversation)
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) RecordOutgoingPackageMessage(ctx context.Context, chatID string, messageID string, packageKeyValue string, fileName string, caption string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chatID = strings.TrimSpace(chatID)
	messageID = strings.TrimSpace(messageID)
	packageKeyValue = normalizePackageInterest(packageKeyValue)
	fileName = strings.TrimSpace(filepath.Base(fileName))
	caption = strings.TrimSpace(caption)
	if chatID == "" || messageID == "" || !isPackageSelectionKey(packageKeyValue) {
		return nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	conversation.OutgoingPackageMessages[messageID] = OutgoingPackageMessage{
		PackageKey: packageKeyValue,
		FileName:   fileName,
		Caption:    caption,
		SentAt:     now,
	}
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return s.persistConversationLocked(ctx, conversation)
}

func (s *ConversationStore) getOrCreateLocked(chatID string, now time.Time) *Conversation {
	conversation, exists := s.conversations[chatID]
	if !exists {
		conversation = &Conversation{
			ChatID:                  chatID,
			Phone:                   phoneFromChatID(chatID),
			Stage:                   ClientStateNeutralNew,
			LeadStatus:              LeadStatusNeutral,
			ProcessedMessageIDs:     make(map[string]time.Time),
			AskedFields:             make(map[string]bool),
			CompletedFields:         make(map[string]bool),
			SentVideos:              make(map[int]bool),
			SentVideoFiles:          make(map[string]time.Time),
			OutgoingPackageMessages: make(map[string]OutgoingPackageMessage),
			SelectedLevel:           0,
			CreatedAt:               now,
			UpdatedAt:               now,
			LastUpdated:             now,
		}
		s.conversations[chatID] = conversation
	}
	ensureConversationMaps(conversation)
	if conversation.CreatedAt.IsZero() {
		conversation.CreatedAt = now
	}
	if conversation.UpdatedAt.IsZero() {
		conversation.UpdatedAt = conversation.LastUpdated
	}
	if conversation.Phone == "" {
		conversation.Phone = phoneFromChatID(chatID)
	}
	if conversation.Stage == "" {
		conversation.Stage = ClientStateNeutralNew
	}
	disableGroupConversationAutomation(conversation)
	return conversation
}

func (s *ConversationStore) cleanupLocked(now time.Time) {
	if now.Sub(s.lastCleanupTime) < time.Minute {
		return
	}
	s.lastCleanupTime = now

	if s.conversationTTL > 0 {
		cutoff := now.Add(-s.conversationTTL)
		for chatID, conversation := range s.conversations {
			if conversation == nil || conversation.LastUpdated.Before(cutoff) {
				delete(s.conversations, chatID)
			}
		}
	}
	cutoff := now.Add(-s.messageTTL)
	for messageID, processedAt := range s.processed {
		if processedAt.Before(cutoff) {
			delete(s.processed, messageID)
		}
	}
	for messageID, processingAt := range s.processing {
		if processingAt.Before(cutoff) {
			delete(s.processing, messageID)
		}
	}
}

func cloneConversation(conversation Conversation) Conversation {
	clone := conversation
	clone.Messages = append([]ChatMessage(nil), conversation.Messages...)
	clone.Lead = cloneLeadState(conversation.Lead)
	clone.ProcessedMessageIDs = make(map[string]time.Time, len(conversation.ProcessedMessageIDs))
	for messageID, processedAt := range conversation.ProcessedMessageIDs {
		clone.ProcessedMessageIDs[messageID] = processedAt
	}
	clone.AskedFields = make(map[string]bool, len(conversation.AskedFields))
	for field, asked := range conversation.AskedFields {
		clone.AskedFields[field] = asked
	}
	clone.CompletedFields = make(map[string]bool, len(conversation.CompletedFields))
	for field, completed := range conversation.CompletedFields {
		clone.CompletedFields[field] = completed
	}
	clone.SentVideos = make(map[int]bool, len(conversation.SentVideos))
	for level, sent := range conversation.SentVideos {
		clone.SentVideos[level] = sent
	}
	clone.SentVideoFiles = make(map[string]time.Time, len(conversation.SentVideoFiles))
	for fileName, sentAt := range conversation.SentVideoFiles {
		clone.SentVideoFiles[fileName] = sentAt
	}
	clone.OutgoingPackageMessages = make(map[string]OutgoingPackageMessage, len(conversation.OutgoingPackageMessages))
	for messageID, metadata := range conversation.OutgoingPackageMessages {
		clone.OutgoingPackageMessages[messageID] = metadata
	}
	clone.MissingFields = append([]string(nil), conversation.MissingFields...)
	return clone
}

func cloneConversationPtr(conversation *Conversation) *Conversation {
	if conversation == nil {
		return nil
	}
	clone := cloneConversation(*conversation)
	return &clone
}

func cloneLeadState(lead LeadState) LeadState {
	clone := lead
	clone.Platforms = append([]string(nil), lead.Platforms...)
	if lead.PreviousAIAds != nil {
		value := *lead.PreviousAIAds
		clone.PreviousAIAds = &value
	}
	return clone
}

func ensureConversationMaps(conversation *Conversation) {
	if conversation.ProcessedMessageIDs == nil {
		conversation.ProcessedMessageIDs = make(map[string]time.Time)
	}
	if conversation.AskedFields == nil {
		conversation.AskedFields = make(map[string]bool)
	}
	if conversation.CompletedFields == nil {
		conversation.CompletedFields = completedFieldsForLead(conversation.Lead)
	}
	if conversation.SentVideos == nil {
		conversation.SentVideos = make(map[int]bool)
	}
	if conversation.SentVideoFiles == nil {
		conversation.SentVideoFiles = make(map[string]time.Time)
	}
	if conversation.OutgoingPackageMessages == nil {
		conversation.OutgoingPackageMessages = make(map[string]OutgoingPackageMessage)
	}
	if conversation.LeadStatus == "" {
		conversation.LeadStatus = normalizeLeadStatus(conversation.Lead.LeadStatus)
	}
	if conversation.LeadStatus == "" {
		conversation.LeadStatus = LeadStatusNeutral
	}
	refreshConversationDerivedState(conversation)
}

func completedFieldsForLead(lead LeadState) map[string]bool {
	completed := make(map[string]bool)
	if isValidNiche(lead.Niche) {
		completed[fieldNiche] = true
	}
	if strings.TrimSpace(lead.City) != "" {
		completed[fieldCity] = true
	}
	if isValidGoal(lead.Goal) {
		completed[fieldGoal] = true
	}
	if strings.TrimSpace(lead.Platform) != "" || len(lead.Platforms) > 0 {
		completed[fieldPlatform] = true
	}
	if isValidDeadline(lead.Deadline) {
		completed[fieldDeadline] = true
	}
	if strings.TrimSpace(lead.VideoQuantity) != "" {
		completed[fieldVideoQuantity] = true
	}
	if strings.TrimSpace(lead.ProductOrService) != "" {
		completed[fieldProductService] = true
	}
	if strings.TrimSpace(lead.TargetAudience) != "" {
		completed[fieldTargetAudience] = true
	}
	if len(lead.ReferenceLinks) > 0 || strings.TrimSpace(lead.WebsiteOrInstagram) != "" {
		completed[fieldReferenceLinks] = true
	}
	if len(lead.LikedFormats) > 0 {
		completed[fieldLikedFormats] = true
	}
	if strings.TrimSpace(lead.VoicePreference) != "" {
		completed[fieldVoicePreference] = true
	}
	if lead.PreviousAIAds != nil || strings.TrimSpace(lead.AIExperience) != "" {
		completed[fieldPreviousAIAds] = true
	}
	if isValidPackageInterest(lead.SelectedPackage) {
		completed[fieldPackageInterest] = true
	}
	if (lead.BriefCompleted || lead.ContactBriefReady) && leadHasRequiredManagerFields(lead) {
		completed[fieldBrief] = true
	}
	return completed
}

func mergeCompletedFields(existing map[string]bool, derived map[string]bool) map[string]bool {
	merged := make(map[string]bool, len(existing)+len(derived))
	for field, completed := range existing {
		field = normalizeFieldName(field)
		if field != "" && completed {
			merged[field] = true
		}
	}
	for field, completed := range derived {
		field = normalizeFieldName(field)
		if field != "" && completed {
			merged[field] = true
		}
	}
	return merged
}

func normalizeFieldName(field string) string {
	switch strings.TrimSpace(field) {
	case "platforms":
		return fieldPlatform
	case "previous_ai_ads":
		return fieldPreviousAIAds
	case fieldPackage:
		return fieldPackageInterest
	case fieldNiche, fieldCity, fieldGoal, fieldPlatform, fieldDeadline, fieldPreviousAIAds,
		fieldPackageInterest, fieldVideoQuantity, fieldProductService, fieldTargetAudience,
		fieldReferenceLinks, fieldLikedFormats, fieldVoicePreference, fieldBrief:
		return strings.TrimSpace(field)
	default:
		return ""
	}
}

func levelByVideoFile(fileName string) int {
	switch strings.TrimSpace(fileName) {
	case VideoLevel1:
		return 1
	case VideoLevel2:
		return 2
	case VideoLevel3:
		return 3
	default:
		return 0
	}
}

func updateLeadFlagsForStage(lead LeadState, stage string) LeadState {
	switch stage {
	case StageQualification:
		lead.HasBeenGreeted = true
	}
	return lead
}

func refreshLeadStatus(lead LeadState, stage string) LeadState {
	if strings.TrimSpace(lead.Platform) == "" && len(lead.Platforms) > 0 {
		lead.Platform = strings.Join(lead.Platforms, ", ")
	}
	currentStatus := normalizeLeadStatus(lead.LeadStatus)
	if currentStatus == LeadStatusMuted || stage == StageMuted {
		lead.LeadStatus = LeadStatusMuted
		return lead
	}
	if currentStatus == LeadStatusClosed {
		lead.LeadStatus = LeadStatusClosed
		return lead
	}
	managerReady := leadHasRequiredManagerFields(lead)
	if !managerReady {
		lead.BriefCompleted = false
		lead.ContactBriefReady = false
		if currentStatus == LeadStatusHandoffRequired {
			currentStatus = ""
		}
	}
	handoffRequested := lead.BriefCompleted || lead.ContactBriefReady || stage == StageBriefCollected || stage == StageHandoffRequired
	switch {
	case managerReady && handoffRequested:
		lead.BriefCompleted = true
		lead.ContactBriefReady = true
		lead.LeadStatus = LeadStatusHandoffRequired
	case strings.TrimSpace(lead.SelectedPackage) != "" || lead.WantsQuestionnaire || stage == StageBriefRequested || stage == StagePackageSelected:
		lead.LeadStatus = LeadStatusHot
	case lead.PortfolioSent || lead.OfferSent || lead.HasCoreFields() || lead.PreviousAIAds != nil ||
		stage == StagePackageSuggested || stage == StageOffer || stage == StagePortfolio || stage == StagePortfolioSent:
		lead.LeadStatus = LeadStatusWarm
	default:
		if currentStatus != "" && currentStatus != LeadStatusHandoffRequired {
			lead.LeadStatus = currentStatus
		} else {
			lead.LeadStatus = LeadStatusNew
		}
	}
	return lead
}

func stageSelectsPackage(stage string) bool {
	switch stage {
	case StagePackageSelected, StageBriefRequested, StageBriefCollected, ClientStateAwaitingQuestionnaireConfirm, ClientStateHandedOff:
		return true
	default:
		return false
	}
}

func isHandoffClosingStage(stage string) bool {
	switch strings.TrimSpace(stage) {
	case ClientStateHandedOff, StageHandoffRequired, StageBriefCollected:
		return true
	default:
		return false
	}
}

func isClosingOrMutedStage(stage string) bool {
	switch strings.TrimSpace(stage) {
	case ClientStateHandedOff,
		ClientStateStopped,
		ClientStateOptOut,
		StageHandoffRequired,
		StageBriefCollected,
		StageMuted:
		return true
	default:
		return false
	}
}

func activeStageForIncompleteHandoff(conversation Conversation) string {
	if conversation.QuestionnaireOfferSent {
		return ClientStateAwaitingQuestionnaireConfirm
	}
	if conversation.PackagesSent || conversation.Lead.OfferSent || conversation.SentPortfolio || conversation.Lead.PortfolioSent {
		return ClientStatePackagesPresented
	}
	return ClientStateAwaitingQualification
}

func hasHandoffClosure(conversation Conversation) bool {
	return conversation.Stage == ClientStateHandedOff ||
		conversation.HandedOffToOwner ||
		conversation.AutomationClosed ||
		!conversation.TransferredAt.IsZero() ||
		normalizeLeadStatus(conversation.LeadStatus) == LeadStatusHandoffRequired ||
		normalizeLeadStatus(conversation.Lead.LeadStatus) == LeadStatusHandoffRequired
}

func updateConversationFlagsForStage(conversation *Conversation, stage string) {
	if conversation == nil {
		return
	}
	switch stage {
	case ClientStateAwaitingQualification:
		conversation.InitialMessageSent = true
		conversation.Lead.HasBeenGreeted = true
	case ClientStatePackagesPresented:
		conversation.InitialMessageSent = true
		conversation.PackagesSent = true
		conversation.SentPortfolio = true
		conversation.Lead.PortfolioSent = true
		conversation.Lead.OfferSent = true
	case ClientStateAwaitingQuestionnaireConfirm:
		conversation.QuestionnaireOfferSent = true
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
	case ClientStateHandedOff:
		conversation.QuestionnaireOfferSent = true
		conversation.HandedOffToOwner = true
		conversation.AutomationClosed = true
		conversation.Stopped = true
		conversation.NextFollowupAt = time.Time{}
		conversation.FollowupStage = ""
		conversation.FollowupReferenceAt = time.Time{}
		conversation.Lead.BriefRequested = true
		conversation.Lead.ContactBriefReady = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.Lead.LeadStatus = LeadStatusHandoffRequired
	case ClientStateStopped:
		conversation.Stopped = true
		conversation.NextFollowupAt = time.Time{}
		conversation.FollowupStage = ""
		conversation.FollowupReferenceAt = time.Time{}
	case ClientStateOptOut:
		conversation.OptOut = true
		conversation.Stopped = true
		conversation.NextFollowupAt = time.Time{}
		conversation.FollowupStage = ""
		conversation.FollowupReferenceAt = time.Time{}
		conversation.Lead.LeadStatus = LeadStatusMuted
	case StageQualification:
		conversation.InitialMessageSent = true
	case StagePortfolioSent, StagePortfolio:
		conversation.SentPortfolio = true
	case StageOffer, StagePackageSuggested, StageAIExperienceChecked:
		conversation.PackagesSent = true
	case StageBriefRequested:
		conversation.QuestionnaireSent = true
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
	case StageBriefCollected, StageHandoffRequired:
		conversation.QuestionnaireSent = true
		conversation.HandedOffToOwner = true
		conversation.AutomationClosed = true
		conversation.Stopped = true
		conversation.NextFollowupAt = time.Time{}
		conversation.FollowupStage = ""
		conversation.FollowupReferenceAt = time.Time{}
	}
}

func phoneFromChatID(chatID string) string {
	phone, _, _ := strings.Cut(strings.TrimSpace(chatID), "@")
	return strings.TrimSpace(phone)
}
