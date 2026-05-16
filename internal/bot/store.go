package bot

import (
	"context"
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

type LeadData = LeadState

type Conversation struct {
	ChatID              string
	Language            string
	Stage               string
	LeadStatus          string
	Lead                LeadData
	Messages            []ChatMessage
	ProcessedMessageIDs map[string]time.Time
	LastIncomingText    string
	LastIncomingAt      time.Time
	LastReplyText       string
	LastReplyAt         time.Time
	AskedFields         map[string]bool
	CompletedFields     map[string]bool
	SentVideos          map[int]bool
	SentVideoFiles      map[string]time.Time
	SentPortfolio       bool
	BriefAsked          bool
	BriefCollected      bool
	SelectedLevel       int
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LastUpdated         time.Time
}

type ConversationStore struct {
	mu              sync.RWMutex
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
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
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
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
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
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return nil
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
	return nil
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
	return nil
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

func (s *ConversationStore) UpdateState(ctx context.Context, chatID string, stage string, selectedLevel int) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(now)
	conversation := s.getOrCreateLocked(chatID, now)
	if stage != "" {
		conversation.Stage = stage
	}
	if stageSelectsPackage(stage) && selectedLevel > 0 && selectedLevel <= 3 {
		conversation.SelectedLevel = selectedLevel
		conversation.Lead.SelectedPackage = packageKey(selectedLevel)
	}
	conversation.Lead = updateLeadFlagsForStage(conversation.Lead, stage)
	switch stage {
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
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return nil
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
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return nil
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
	return nil
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
	return nil
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
	conversation.UpdatedAt = now
	conversation.LastUpdated = now
	return nil
}

func (s *ConversationStore) getOrCreateLocked(chatID string, now time.Time) *Conversation {
	conversation, exists := s.conversations[chatID]
	if !exists {
		conversation = &Conversation{
			ChatID:              chatID,
			LeadStatus:          LeadStatusNeutral,
			ProcessedMessageIDs: make(map[string]time.Time),
			AskedFields:         make(map[string]bool),
			CompletedFields:     make(map[string]bool),
			SentVideos:          make(map[int]bool),
			SentVideoFiles:      make(map[string]time.Time),
			SelectedLevel:       0,
			CreatedAt:           now,
			UpdatedAt:           now,
			LastUpdated:         now,
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
	if conversation.LeadStatus == "" {
		conversation.LeadStatus = normalizeLeadStatus(conversation.Lead.LeadStatus)
	}
	if conversation.LeadStatus == "" {
		conversation.LeadStatus = LeadStatusNeutral
	}
}

func completedFieldsForLead(lead LeadState) map[string]bool {
	completed := make(map[string]bool)
	if strings.TrimSpace(lead.Niche) != "" {
		completed[fieldNiche] = true
	}
	if strings.TrimSpace(lead.Goal) != "" {
		completed[fieldGoal] = true
	}
	if strings.TrimSpace(lead.Platform) != "" || len(lead.Platforms) > 0 {
		completed[fieldPlatform] = true
	}
	if strings.TrimSpace(lead.Deadline) != "" {
		completed[fieldDeadline] = true
	}
	if lead.PreviousAIAds != nil || strings.TrimSpace(lead.AIExperience) != "" {
		completed[fieldPreviousAIAds] = true
	}
	if strings.TrimSpace(lead.SelectedPackage) != "" {
		completed[fieldPackage] = true
	}
	if lead.BriefRequested {
		completed[fieldBrief] = true
	}
	if lead.BriefCompleted || lead.ContactBriefReady {
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
	case fieldNiche, fieldGoal, fieldPlatform, fieldDeadline, fieldPreviousAIAds, fieldPackage, fieldBrief:
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
	switch {
	case lead.BriefCompleted || lead.ContactBriefReady || stage == StageBriefCollected || stage == StageHandoffRequired:
		lead.BriefCompleted = true
		lead.ContactBriefReady = true
		lead.LeadStatus = LeadStatusHandoffRequired
	case strings.TrimSpace(lead.SelectedPackage) != "" || stage == StageBriefRequested || stage == StagePackageSelected:
		lead.LeadStatus = LeadStatusHot
	case lead.PortfolioSent || lead.OfferSent || lead.HasCoreFields() || lead.PreviousAIAds != nil ||
		stage == StagePackageSuggested || stage == StageOffer || stage == StagePortfolio || stage == StagePortfolioSent:
		lead.LeadStatus = LeadStatusWarm
	default:
		if currentStatus != "" {
			lead.LeadStatus = currentStatus
		} else {
			lead.LeadStatus = LeadStatusNew
		}
	}
	return lead
}

func stageSelectsPackage(stage string) bool {
	switch stage {
	case StagePackageSelected, StageBriefRequested, StageBriefCollected:
		return true
	default:
		return false
	}
}
