package bot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/yereke99/stone/internal/openai"
	"go.uber.org/zap"
)

const videoSendDelay = 1500 * time.Millisecond

type outgoingCounterKey struct{}

type outgoingCounter struct {
	count int
}

type IncomingMessage struct {
	IDMessage   string
	ChatID      string
	SenderName  string
	TypeMessage string
	Text        string
}

type GreenSender interface {
	SendMessage(ctx context.Context, chatID string, message string) error
	SendFileByUpload(ctx context.Context, chatID string, filePath string, caption string) error
}

type SalesAI interface {
	GenerateSalesReply(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.SalesResponse, error)
}

type Service struct {
	sender       GreenSender
	ai           SalesAI
	store        *ConversationStore
	videoDir     string
	portfolio    PortfolioLinks
	languageMode string
	adminChatIDs []string
	logger       *zap.Logger
	chatLocks    sync.Map
}

func NewService(sender GreenSender, ai SalesAI, store *ConversationStore, videoDir string, portfolio PortfolioLinks, languageMode string, logger *zap.Logger, adminChatIDs ...string) *Service {
	return &Service{
		sender:       sender,
		ai:           ai,
		store:        store,
		videoDir:     videoDir,
		portfolio:    portfolio,
		languageMode: languageMode,
		adminChatIDs: normalizeAdminChatIDs(adminChatIDs),
		logger:       logger,
	}
}

func (s *Service) HandleIncomingMessage(ctx context.Context, msg IncomingMessage) (err error) {
	chatID := strings.TrimSpace(msg.ChatID)
	if chatID == "" {
		return fmt.Errorf("chat id is required")
	}
	unlock, err := s.lockChat(ctx, chatID)
	if err != nil {
		return err
	}
	defer unlock()

	counter := &outgoingCounter{}
	ctx = context.WithValue(ctx, outgoingCounterKey{}, counter)

	text := strings.TrimSpace(msg.Text)

	conversation, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	stateBefore := conversation.Stage
	leadStatusBefore := conversation.LeadStatus
	selectedBefore := conversation.SelectedLevel
	defer func() {
		after, snapshotErr := s.store.Snapshot(context.Background(), chatID)
		fields := []zap.Field{
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
			zap.String("state_before", stateBefore),
			zap.String("state_after", after.Stage),
			zap.String("lead_status_before", leadStatusBefore),
			zap.String("lead_status_after", after.LeadStatus),
			zap.Int("selected_level_before", selectedBefore),
			zap.Int("selected_level_after", after.SelectedLevel),
			zap.Strings("completed_fields", mapKeys(after.CompletedFields)),
			zap.Strings("asked_fields", mapKeys(after.AskedFields)),
			zap.Int("outgoing_count", counter.count),
		}
		if snapshotErr != nil {
			fields = append(fields, zap.Error(snapshotErr))
		}
		if err != nil {
			fields = append(fields, zap.Error(err))
			s.warn("incoming message processing completed with error", fields...)
			return
		}
		s.info("incoming message processing completed", fields...)
	}()
	hadLanguage := conversation.Language != ""
	language := conversation.Language

	if language == "" {
		language = s.detectLanguage(text)
		if language == "" {
			language = "ru"
		}

		if err := s.store.UpdateLanguage(ctx, chatID, language); err != nil {
			return err
		}
	}

	if text == "" {
		return s.sendAndRemember(ctx, chatID, fallbackForLead(language, conversation.Lead), StageDiagnosis, 0)
	}

	if err := s.store.AppendMessage(ctx, chatID, "user", text); err != nil {
		return err
	}
	if err := s.store.MarkIncoming(ctx, chatID, text); err != nil {
		return err
	}

	analysis := AnalyzeCustomerMessage(text, conversation.Lead, language)
	if isBriefAnswerForConversation(text, analysis, conversation) {
		analysis.Intent = IntentBriefAnswer
	}
	lead := conversation.Lead
	lead.ApplyAnalysis(analysis)
	if err := s.store.UpdateLead(ctx, chatID, lead); err != nil {
		return err
	}
	conversation, err = s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	s.info("incoming text analyzed",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("language", language),
		zap.String("intent", analysis.Intent),
		zap.Int("selected_level", analysis.SelectedLevel),
		zap.String("state_before", conversation.Stage),
		zap.String("lead_status", conversation.LeadStatus),
		zap.Strings("missing_fields", analysis.MissingFields),
	)

	handled, err := s.handleLocalCommand(ctx, chatID, text, language, conversation, analysis)
	if handled || err != nil {
		return err
	}

	if !hadLanguage && !analysis.HasBusinessSignal() && (analysis.Intent == IntentOther || analysis.Intent == IntentGreeting) {
		return s.sendAndRemember(ctx, chatID, QualificationGreetingText(language), StageQualification, 0)
	}

	if reply, ok := buildReturningLeadReply(language, conversation, analysis); ok {
		return s.sendAndRemember(ctx, chatID, reply.text, reply.stage, reply.level)
	}

	if reply, ok := buildLeadReply(language, lead, analysis, conversation); ok {
		return s.sendAndRemember(ctx, chatID, reply.text, reply.stage, reply.level, reply.askedFields...)
	}

	conversation, err = s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}

	s.info("openai called",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("stage", conversation.Stage),
		zap.String("lead_status", conversation.LeadStatus),
	)
	response, err := s.ai.GenerateSalesReply(ctx, systemPromptForLanguage(language, conversation), toOpenAIMessages(conversation.Messages, analysis))
	if err != nil {
		s.warn("openai response unavailable", zap.Error(err))
		return s.sendAndRemember(ctx, chatID, fallbackForLead(language, conversation.Lead), StageDiagnosis, 0)
	}

	response = normalizeAIResponse(response, language)
	if response.AskBrief {
		response.Reply = BriefText(response.Language)
		response.Stage = StageBriefRequested
	}
	if response.Reply == "" {
		response.Reply = fallbackForLead(response.Language, conversation.Lead)
	}

	if err := s.applyAIState(ctx, chatID, response); err != nil {
		return err
	}

	if err := s.sendAndRemember(ctx, chatID, response.Reply, response.Stage, response.RecommendedLevel, response.AskedFields...); err != nil {
		return err
	}

	explicitVideoRequest := isExplicitVideoRequest(text)
	allowVideoRepeat := isExplicitVideoRepeatRequest(text)
	videoFiles := response.SendVideos
	if explicitVideoRequest && response.RecommendedLevel > 0 && len(videoFiles) == 0 {
		if offer, ok := OfferByLevel(response.RecommendedLevel); ok {
			videoFiles = []string{offer.FileName}
		}
	}
	if !explicitVideoRequest && response.Stage != StagePortfolioSent && response.Stage != StagePortfolio {
		videoFiles = nil
	}

	if err := s.sendVideos(ctx, chatID, videoFiles, response.Language, allowVideoRepeat); err != nil {
		return err
	}

	return nil
}

func (s *Service) HandleNonTextMessage(ctx context.Context, chatID string, language string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return fmt.Errorf("chat id is required")
	}
	unlock, err := s.lockChat(ctx, chatID)
	if err != nil {
		return err
	}
	defer unlock()

	if language != "ru" && language != "kk" && language != "en" {
		conversation, err := s.store.Snapshot(ctx, chatID)
		if err != nil {
			return err
		}
		language = conversation.Language
	}
	if language != "ru" && language != "kk" && language != "en" {
		language = "ru"
	}
	return s.sendAndRemember(ctx, chatID, NonTextFallbackText(language), StageDiagnosis, 0)
}

func (s *Service) handleLocalCommand(ctx context.Context, chatID string, text string, language string, conversation Conversation, analysis CustomerAnalysis) (bool, error) {
	normalized := normalizeText(text)

	if analysis.Intent == IntentMute || normalizeLeadStatus(conversation.LeadStatus) == LeadStatusMuted || normalizeLeadStatus(conversation.Lead.LeadStatus) == LeadStatusMuted {
		s.info("local rule used",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("rule", "mute"),
		)
		return true, s.store.UpdateState(ctx, chatID, StageMuted, conversation.SelectedLevel)
	}

	if analysis.Intent == IntentBriefAnswer {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "brief_answer"))
		return true, s.sendAndRemember(ctx, chatID, BriefCollectedText(language), StageHandoffRequired, conversation.SelectedLevel)
	}

	if normalizeLeadStatus(conversation.LeadStatus) == LeadStatusHandoffRequired || normalizeLeadStatus(conversation.Lead.LeadStatus) == LeadStatusHandoffRequired || conversation.Stage == StageHandoffRequired {
		s.info("local rule used",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("rule", "handoff_status"),
		)
		return true, s.sendAndRemember(ctx, chatID, handoffStatusText(language), StageHandoffRequired, conversation.SelectedLevel)
	}

	if analysis.Intent == IntentNegativeReaction {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "negative_reaction"))
		field := firstAskableMissingField(conversation.Lead, conversation)
		return true, s.sendAndRemember(ctx, chatID, negativeMissingReply(language, conversation.Lead, field), StageDiagnosis, 0, field)
	}

	if analysis.Intent == IntentRefusal {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "refusal"))
		return true, s.sendAndRemember(ctx, chatID, refusalText(language), StageClosing, 0)
	}

	if analysis.Intent == IntentPackageQuestion {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "package_question"), zap.Int("selected_level", analysis.SelectedLevel))
		return true, s.sendAndRemember(ctx, chatID, packageDetailText(language, analysis.SelectedLevel), StagePackageSuggested, 0)
	}

	if analysis.Intent == IntentPackageSelection {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "package_selection"), zap.Int("selected_level", analysis.SelectedLevel))
		if err := s.sendBriefForPackage(ctx, chatID, analysis.SelectedLevel, language); err != nil {
			return true, err
		}
		if offer, ok := OfferByLevel(analysis.SelectedLevel); ok {
			return true, s.sendVideos(ctx, chatID, []string{offer.FileName}, language, isExplicitVideoRepeatRequest(text))
		}
		return true, nil
	}

	if analysis.Intent == IntentPriceQuestion || hasAny(normalized, []string{"цена", "стоимость", "сколько", "прайс", "қанша", "баға", "price", "cost"}) {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "price_question"))
		level := requestedLevelFromText(text)
		if level == 0 {
			level = analysis.SelectedLevel
		}
		if level == 0 && conversation.Lead.SelectedPackage != "" {
			level = selectedLevelFromConversation(conversation)
		}
		if level > 0 {
			return true, s.sendAndRemember(ctx, chatID, packagePriceText(language, level), StagePackageSuggested, 0)
		}
		if conversation.Lead.OfferSent || conversation.Lead.SelectedPackage != "" {
			return true, s.sendAndRemember(ctx, chatID, shortPriceReminderText(language), StagePackageSuggested, 0)
		}
		return true, s.sendAndRemember(ctx, chatID, PriceText(language), StagePackageSuggested, 0)
	}

	if analysis.Intent == IntentPortfolioRequest || hasAny(normalized, []string{"портфолио", "видео", "пример", "примеры", "мысал", "көрсет", "варианты", "вариант", "portfolio", "example"}) {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "portfolio_request"))
		level := requestedLevelFromText(text)
		allowRepeat := isExplicitVideoRepeatRequest(text)
		if (conversation.SentPortfolio || conversation.Lead.PortfolioSent) && !allowRepeat {
			return true, s.sendAndRemember(ctx, chatID, portfolioAlreadySentText(language), StagePortfolioSent, level)
		}

		reply := portfolioLinksMessage(language, s.portfolio, level)
		videoFiles := []string{}
		if level > 0 && s.portfolio.URLByLevel(level) == "" {
			if offer, ok := OfferByLevel(level); ok {
				videoFiles = []string{offer.FileName}
			}
		}
		if level == 0 && !s.portfolio.HasAny() {
			reply = PortfolioIntroText(language)
			if offer, ok := OfferByLevel(1); ok {
				videoFiles = []string{offer.FileName}
			}
		}

		if err := s.sendAndRemember(ctx, chatID, reply, StagePortfolioSent, level); err != nil {
			return true, err
		}
		if len(videoFiles) > 0 {
			return true, s.sendVideos(ctx, chatID, videoFiles, language, allowRepeat)
		}
		return true, nil
	}

	if hasAny(normalized, []string{"анкета", "бриф"}) {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "brief_request"))
		level := selectedLevelFromConversation(conversation)
		if level > 0 {
			return true, s.sendBriefForPackage(ctx, chatID, level, language)
		}
		return true, s.sendAndRemember(ctx, chatID, BriefText(language), StageBriefRequested, 0)
	}

	if analysis.Intent == IntentReadyToOrder {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "ready_to_order"))
		level := selectedLevelFromConversation(conversation)
		if level > 0 {
			return true, s.sendBriefForPackage(ctx, chatID, level, language)
		}
		return true, s.sendAndRemember(ctx, chatID, BriefText(language), StageBriefRequested, 0)
	}

	if analysis.Intent == IntentAgreement {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "agreement"))
		switch {
		case conversation.Stage == StageBriefRequested || conversation.Lead.BriefRequested:
			return true, s.sendAndRemember(ctx, chatID, BriefReminderText(language), StageBriefRequested, conversation.SelectedLevel)
		case isPackageSuggestionStage(conversation.Stage) && selectedLevelFromConversation(conversation) == 0:
			if conversation.Lead.OfferSent {
				return true, s.sendAndRemember(ctx, chatID, packageChoiceNoPricesText(language), StagePackageSuggested, 0)
			}
			return true, s.sendAndRemember(ctx, chatID, ClarifyPackageText(language), StagePackageSuggested, 0)
		}
	}

	if analysis.Intent == IntentObjection || (conversation.Stage != "" && hasAny(normalized, []string{"дорого", "подумаю", "подумаем", "позже", "кейін", "қымбат", "ойлан", "expensive"})) {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "objection"))
		return true, s.sendAndRemember(ctx, chatID, ObjectionText(language), StageObjection, 0)
	}

	if isLocalOfftopic(normalized) {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "offtopic"))
		return true, s.sendAndRemember(ctx, chatID, OfftopicText(language), StageOfftopic, 0)
	}

	return false, nil
}

func (s *Service) sendOffer(ctx context.Context, chatID string, level int, language string) error {
	return s.sendBriefForPackage(ctx, chatID, level, language)
}

func (s *Service) sendBriefForPackage(ctx context.Context, chatID string, level int, language string) error {
	if _, ok := OfferByLevel(level); !ok {
		return s.sendAndRemember(ctx, chatID, BriefText(language), StageBriefRequested, 0)
	}
	return s.sendAndRemember(ctx, chatID, BriefTextForPackage(language, level), StageBriefRequested, level)
}

func (s *Service) sendAndRemember(ctx context.Context, chatID string, message string, stage string, selectedLevel int, askedFields ...string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	duplicate, err := s.store.RecentlySentReply(ctx, chatID, message, outgoingRepeatWindow)
	if err != nil {
		return err
	}
	if duplicate {
		s.info("duplicate whatsapp reply skipped",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("stage", stage),
			zap.Int("selected_level", selectedLevel),
		)
		if err := s.store.MarkAskedFields(context.WithoutCancel(ctx), chatID, append(askedFields, fieldsAskedByMessage(message, stage)...)); err != nil {
			return err
		}
		if err := s.store.UpdateState(context.WithoutCancel(ctx), chatID, stage, selectedLevel); err != nil {
			return err
		}
		s.notifyAdminsIfNeeded(context.WithoutCancel(ctx), chatID, stage)
		return nil
	}

	if err := s.sender.SendMessage(ctx, chatID, message); err != nil {
		return err
	}
	incrementOutgoingCount(ctx)
	persistCtx := context.WithoutCancel(ctx)
	s.info("whatsapp reply sent",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("stage", stage),
		zap.Int("selected_level", selectedLevel),
	)
	if err := s.store.MarkReplySent(persistCtx, chatID, message); err != nil {
		return err
	}
	if err := s.store.AppendMessage(persistCtx, chatID, "assistant", message); err != nil {
		return err
	}
	if err := s.store.MarkAskedFields(persistCtx, chatID, append(askedFields, fieldsAskedByMessage(message, stage)...)); err != nil {
		return err
	}
	if err := s.store.UpdateState(persistCtx, chatID, stage, selectedLevel); err != nil {
		return err
	}
	s.notifyAdminsIfNeeded(persistCtx, chatID, stage)
	return nil
}

func (s *Service) sendVideos(ctx context.Context, chatID string, files []string, language string, allowRepeat bool) error {
	for index, fileName := range dedupeVideos(files) {
		if index > 0 {
			if err := sleepWithContext(ctx, videoSendDelay); err != nil {
				return err
			}
		}

		shouldSend, err := s.store.ShouldSendVideo(ctx, chatID, fileName, allowRepeat)
		if err != nil {
			return err
		}
		if !shouldSend {
			s.info("portfolio video skipped because already sent",
				zap.String("chat_hash", chatFingerprint(chatID)),
				zap.String("file_name", fileName),
			)
			continue
		}

		filePath := filepath.Join(s.videoDir, fileName)
		if _, err := os.Stat(filePath); err != nil {
			s.warn("portfolio video file is unavailable", zap.String("file_name", fileName), zap.Error(err))
			continue
		}

		caption := strings.TrimSpace(OfferCaptionByVideo(fileName, language))
		if caption == "" {
			caption = "Stone production"
		}

		if err := s.sender.SendFileByUpload(ctx, chatID, filePath, caption); err != nil {
			s.warn("portfolio video send failed", zap.String("file_name", fileName), zap.Error(err))
			return err
		}
		s.info("portfolio video sent",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("file_name", fileName),
		)
		incrementOutgoingCount(ctx)
		if err := s.store.MarkVideoSent(context.WithoutCancel(ctx), chatID, fileName); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) notifyAdminsIfNeeded(ctx context.Context, chatID string, stage string) {
	if len(s.adminChatIDs) == 0 {
		return
	}
	if stage != StageHandoffRequired && stage != StageBriefCollected {
		return
	}

	sent, err := s.store.AdminNotificationSent(ctx, chatID)
	if err != nil {
		s.warn("admin notification state check failed",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Error(err),
		)
		return
	}
	if sent {
		return
	}

	conversation, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		s.warn("admin notification snapshot failed",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Error(err),
		)
		return
	}
	if normalizeLeadStatus(conversation.LeadStatus) != LeadStatusHandoffRequired &&
		normalizeLeadStatus(conversation.Lead.LeadStatus) != LeadStatusHandoffRequired {
		return
	}

	message := adminLeadNotificationText(conversation)
	for _, adminChatID := range s.adminChatIDs {
		if err := s.sender.SendMessage(ctx, adminChatID, message); err != nil {
			s.warn("admin notification send failed",
				zap.String("admin_chat_hash", chatFingerprint(adminChatID)),
				zap.String("client_chat_hash", chatFingerprint(chatID)),
				zap.Error(err),
			)
			return
		}
		incrementOutgoingCount(ctx)
		s.info("admin notification sent",
			zap.String("admin_chat_hash", chatFingerprint(adminChatID)),
			zap.String("client_chat_hash", chatFingerprint(chatID)),
		)
	}

	if err := s.store.MarkAdminNotified(ctx, chatID); err != nil {
		s.warn("admin notification mark failed",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Error(err),
		)
	}
}

func adminLeadNotificationText(conversation Conversation) string {
	lead := conversation.Lead
	lines := []string{
		"Новая заявка Stone production",
		"Клиент: " + conversation.ChatID,
	}
	if link := whatsappLink(conversation.ChatID); link != "" {
		lines = append(lines, "WhatsApp: "+link)
	}
	lines = append(lines,
		"Статус: "+valueOrDash(conversation.LeadStatus),
		"Этап: "+valueOrDash(conversation.Stage),
	)
	if lead.SelectedPackage != "" {
		lines = append(lines, "Пакет: "+lead.SelectedPackage)
	}
	if lead.Niche != "" {
		lines = append(lines, "Ниша: "+lead.Niche)
	}
	if lead.Goal != "" {
		lines = append(lines, "Цель: "+lead.Goal)
	}
	if platform := lead.platformSummary(); platform != "" {
		lines = append(lines, "Площадка: "+platform)
	}
	if lead.Deadline != "" {
		lines = append(lines, "Срок: "+lead.Deadline)
	}
	if lead.Budget != "" {
		lines = append(lines, "Бюджет/интерес: "+lead.Budget)
	}
	if lead.TargetAudience != "" {
		lines = append(lines, "Аудитория: "+lead.TargetAudience)
	}
	if lead.AIExperience != "" {
		lines = append(lines, "AI-опыт: "+lead.AIExperience)
	}
	if text := strings.TrimSpace(conversation.LastIncomingText); text != "" {
		lines = append(lines, "Последнее сообщение: "+text)
	}
	return strings.Join(lines, "\n")
}

func normalizeAdminChatIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		chatID := normalizeWhatsAppChatID(value)
		if chatID == "" {
			continue
		}
		if _, exists := seen[chatID]; exists {
			continue
		}
		seen[chatID] = struct{}{}
		result = append(result, chatID)
	}
	return result
}

func normalizeWhatsAppChatID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "@") {
		return value
	}
	var digits strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	phone := digits.String()
	if len(phone) == 11 && strings.HasPrefix(phone, "8") {
		phone = "7" + phone[1:]
	}
	if phone == "" {
		return ""
	}
	return phone + "@c.us"
}

func whatsappLink(chatID string) string {
	phone, _, ok := strings.Cut(strings.TrimSpace(chatID), "@")
	if !ok || phone == "" {
		return ""
	}
	return "https://wa.me/" + phone
}

func valueOrDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func (s *Service) detectLanguage(text string) string {
	switch strings.ToLower(strings.TrimSpace(s.languageMode)) {
	case "ru":
		return "ru"
	case "kk":
		return "kk"
	case "en":
		return "en"
	}

	normalized := normalizeText(text)
	if normalized == "" {
		return "ru"
	}
	kazakhMarkers := []string{
		"ә", "ғ", "қ", "ң", "ө", "ұ", "ү", "һ", "і",
		"сәлем", "баға", "қанша", "қымбат", "кейін", "жасайық", "бастайық", "мысал", "иә", "керек",
	}
	if hasAny(normalized, kazakhMarkers) {
		return "kk"
	}

	hasCyrillic := false
	for _, r := range normalized {
		if unicode.Is(unicode.Cyrillic, r) {
			hasCyrillic = true
			break
		}
	}
	if hasCyrillic {
		return "ru"
	}
	if hasAny(normalized, []string{"hello", "hi", "price", "cost", "portfolio", "example", "video", "deadline", "sales", "leads"}) {
		return "en"
	}
	return "ru"
}

func toOpenAIMessages(messages []ChatMessage, analysis CustomerAnalysis) []openai.Message {
	result := make([]openai.Message, 0, len(messages)+1)
	result = append(result, openai.Message{
		Role:    "user",
		Content: "Последний анализ сообщения клиента JSON: " + analysis.JSON(),
	})
	for _, message := range messages {
		role := "user"
		if message.Role == "assistant" {
			role = "assistant"
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		result = append(result, openai.Message{
			Role:    role,
			Content: content,
		})
	}
	return result
}

func normalizeAIResponse(response openai.SalesResponse, fixedLanguage string) openai.SalesResponse {
	response.Reply = strings.TrimSpace(response.Reply)

	response.Language = normalizeLanguageCode(fixedLanguage)
	if !isAllowedStage(response.Stage) {
		response.Stage = StageDiagnosis
	}
	if response.RecommendedLevel < 0 || response.RecommendedLevel > 3 {
		response.RecommendedLevel = 0
	}

	response.SendVideos = dedupeVideos(response.SendVideos)
	response.LeadStatus = normalizeLeadStatus(response.LeadStatus)
	if response.NeedHuman && response.LeadStatus == "" {
		response.LeadStatus = LeadStatusHandoffRequired
	}
	if response.NeedHuman && (response.Stage == "" || response.Stage == StageDiagnosis) {
		response.Stage = StageHandoffRequired
	}
	response.CompletedFields = normalizeFieldList(response.CompletedFields)
	response.AskedFields = normalizeFieldList(response.AskedFields)
	return response
}

func (s *Service) applyAIState(ctx context.Context, chatID string, response openai.SalesResponse) error {
	if response.LeadStatus != "" {
		s.store.Update(chatID, func(conversation *Conversation) {
			conversation.Lead.LeadStatus = response.LeadStatus
			conversation.LeadStatus = response.LeadStatus
		})
	}
	if len(response.CompletedFields) > 0 {
		s.store.Update(chatID, func(conversation *Conversation) {
			for _, field := range response.CompletedFields {
				field = normalizeFieldName(field)
				if field != "" {
					conversation.CompletedFields[field] = true
				}
			}
		})
	}
	if len(response.AskedFields) > 0 {
		return s.store.MarkAskedFields(ctx, chatID, response.AskedFields)
	}
	return nil
}

func detectLanguageChoice(text string) string {
	normalized := normalizeText(text)
	switch normalized {
	case "1", "қазақша", "казахский", "казакша", "kz", "kk":
		return "kk"
	case "2", "русский", "орысша", "ru", "rus":
		return "ru"
	case "3", "english", "en":
		return "en"
	default:
		return ""
	}
}

func systemPromptForLanguage(language string, conversation Conversation) string {
	stateJSON := conversationPromptJSON(conversation)
	switch normalizeLanguageCode(language) {
	case "kk":
		return SystemPrompt + "\n\nТекущий язык диалога: kk. Все значения поля reply должны быть только на казахском языке.\nКраткое состояние диалога JSON: " + stateJSON
	case "en":
		return SystemPrompt + "\n\nТекущий язык диалога: en. All reply values must be only in English.\nConversation state JSON: " + stateJSON
	default:
		return SystemPrompt + "\n\nТекущий язык диалога: ru. Все значения поля reply должны быть только на русском языке.\nКраткое состояние диалога JSON: " + stateJSON
	}
}

func conversationPromptJSON(conversation Conversation) string {
	recent := make([]map[string]string, 0, len(conversation.Messages))
	for _, message := range conversation.Messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		recent = append(recent, map[string]string{
			"role":    message.Role,
			"content": content,
		})
	}

	sentVideos := make([]int, 0, len(conversation.SentVideos))
	for level, sent := range conversation.SentVideos {
		if sent {
			sentVideos = append(sentVideos, level)
		}
	}
	sort.Ints(sentVideos)

	payload := struct {
		Stage            string              `json:"stage"`
		LeadStatus       string              `json:"lead_status"`
		Language         string              `json:"language"`
		Lead             LeadState           `json:"lead"`
		CompletedFields  []string            `json:"completed_fields"`
		AskedFields      []string            `json:"asked_fields"`
		SentVideos       []int               `json:"sent_videos"`
		SentPortfolio    bool                `json:"sent_portfolio"`
		BriefAsked       bool                `json:"brief_asked"`
		BriefCollected   bool                `json:"brief_collected"`
		LastIncomingText string              `json:"last_incoming_text"`
		LastReplyText    string              `json:"last_reply_text"`
		RecentMessages   []map[string]string `json:"recent_messages"`
	}{
		Stage:            conversation.Stage,
		LeadStatus:       normalizeLeadStatus(conversation.LeadStatus),
		Language:         normalizeLanguageCode(conversation.Language),
		Lead:             conversation.Lead,
		CompletedFields:  mapKeys(conversation.CompletedFields),
		AskedFields:      mapKeys(conversation.AskedFields),
		SentVideos:       sentVideos,
		SentPortfolio:    conversation.SentPortfolio || conversation.Lead.PortfolioSent,
		BriefAsked:       conversation.BriefAsked || conversation.Lead.BriefRequested,
		BriefCollected:   conversation.BriefCollected || conversation.Lead.BriefCompleted,
		LastIncomingText: strings.TrimSpace(conversation.LastIncomingText),
		LastReplyText:    strings.TrimSpace(conversation.LastReplyText),
		RecentMessages:   recent,
	}
	if payload.Stage == "" {
		payload.Stage = StageNewLead
	}
	if payload.LeadStatus == "" {
		payload.LeadStatus = LeadStatusNeutral
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return conversation.Lead.PromptJSON(conversation.Stage)
	}
	return string(data)
}

func dedupeVideos(files []string) []string {
	allowed := map[string]struct{}{
		VideoLevel1: {},
		VideoLevel2: {},
		VideoLevel3: {},
	}
	seen := make(map[string]struct{}, len(files))
	result := make([]string, 0, len(files))
	for _, fileName := range files {
		fileName = strings.TrimSpace(filepath.Base(fileName))
		if _, ok := allowed[fileName]; !ok {
			continue
		}
		if _, exists := seen[fileName]; exists {
			continue
		}
		seen[fileName] = struct{}{}
		result = append(result, fileName)
	}
	return result
}

func isAllowedStage(stage string) bool {
	switch stage {
	case StageNewLead,
		StageQualification,
		StagePlatformDetected,
		StageAIExperienceChecked,
		StagePackageSuggested,
		StagePackageSelected,
		StagePortfolioSent,
		StageBriefRequested,
		StageBriefCollected,
		StageHandoffRequired,
		StageMuted,
		"greeting",
		StageDiagnosis,
		StageOffer,
		StagePortfolio,
		StageObjection,
		StageClosing,
		StageOfftopic:
		return true
	default:
		return false
	}
}

func (s *Service) warn(message string, fields ...zap.Field) {
	if s.logger == nil {
		return
	}
	s.logger.Warn(message, fields...)
}

func (s *Service) info(message string, fields ...zap.Field) {
	if s.logger == nil {
		return
	}
	s.logger.Info(message, fields...)
}

func (s *Service) lockChat(ctx context.Context, chatID string) (func(), error) {
	lockValue, _ := s.chatLocks.LoadOrStore(chatID, make(chan struct{}, 1))
	lock := lockValue.(chan struct{})
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func incrementOutgoingCount(ctx context.Context) {
	counter, ok := ctx.Value(outgoingCounterKey{}).(*outgoingCounter)
	if !ok || counter == nil {
		return
	}
	counter.count++
}

func chatFingerprint(chatID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(chatID)))
	return hex.EncodeToString(sum[:])[:12]
}

func ChatFingerprintForLog(chatID string) string {
	if strings.TrimSpace(chatID) == "" {
		return ""
	}
	return chatFingerprint(chatID)
}

func fieldsAskedByMessage(message string, stage string) []string {
	normalized := normalizeForAnalysis(message)
	fields := make([]string, 0, 4)
	add := func(field string) {
		field = normalizeFieldName(field)
		if field != "" {
			fields = append(fields, field)
		}
	}

	asksQuestion := strings.Contains(normalized, "?") || containsAny(normalized, []string{
		"подскажите", "уточните", "ответьте", "жазыңыз", "нақтылаңыз", "please", "share", "what", "which", "where", "қандай", "қай",
	})
	if !asksQuestion && stage != StageBriefRequested {
		return normalizeFieldList(fields)
	}

	if containsAny(normalized, []string{"ниша", "niche", "қай ниша", "сала"}) {
		add(fieldNiche)
	}
	if containsAny(normalized, []string{"цель", "мақсат", "goal", "заяв", "продаж", "узнаваем", "leads", "sales", "awareness"}) {
		add(fieldGoal)
	}
	if containsAny(normalized, []string{"instagram", "tiktok", "facebook", "whatsapp", "сайт", "website", "площад", "платформ", "қай жерде", "where will you use"}) {
		add(fieldPlatform)
	}
	if containsAny(normalized, []string{"срок", "мерзім", "timeline", "когда", "deadline"}) {
		add(fieldDeadline)
	}
	if containsAny(normalized, []string{"ии-ролик", "ai ролик", "ai video", "бұрын ai", "previously used"}) {
		add(fieldPreviousAIAds)
	}
	if stage == StageBriefRequested || containsAny(normalized, []string{"бриф", "brief"}) {
		add(fieldBrief)
	}
	return normalizeFieldList(fields)
}

func normalizeFieldList(fields []string) []string {
	result := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = normalizeFieldName(field)
		if field == "" {
			continue
		}
		if _, exists := seen[field]; exists {
			continue
		}
		seen[field] = struct{}{}
		result = append(result, field)
	}
	return result
}

func mapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if value {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func selectedLevelFromConversation(conversation Conversation) int {
	if conversation.SelectedLevel > 0 {
		return conversation.SelectedLevel
	}
	switch conversation.Lead.SelectedPackage {
	case "test":
		return 1
	case "basic":
		return 2
	case "standard":
		return 3
	default:
		return 0
	}
}

func isPackageSuggestionStage(stage string) bool {
	switch stage {
	case StagePackageSuggested, StageOffer, StageAIExperienceChecked:
		return true
	default:
		return false
	}
}

func isBriefAnswerForConversation(text string, analysis CustomerAnalysis, conversation Conversation) bool {
	if conversation.Stage != StageBriefRequested && !conversation.Lead.BriefRequested {
		return false
	}
	if analysis.Intent == IntentNegativeReaction ||
		analysis.Intent == IntentPortfolioRequest ||
		analysis.Intent == IntentPriceQuestion ||
		analysis.Intent == IntentPackageSelection ||
		analysis.Intent == IntentObjection ||
		analysis.Intent == IntentRefusal ||
		analysis.Intent == IntentReadyToOrder ||
		analysis.Intent == IntentAgreement {
		return false
	}

	normalized := normalizeForAnalysis(text)
	if normalized == "" || isAgreement(normalized) {
		return false
	}
	if strings.Contains(normalized, "http") || strings.Contains(normalized, "www") || strings.Contains(normalized, "instagram") || strings.Contains(normalized, "@") {
		return true
	}
	return len(strings.Fields(normalized)) >= 3 || analysis.HasBusinessSignal()
}

func isExplicitVideoRequest(text string) bool {
	normalized := normalizeText(text)
	return hasAny(normalized, []string{
		"портфолио", "видео", "пример", "примеры", "мысал", "көрсет", "покаж", "отправ", "жібер",
		"тестовый", "базовый", "стандарт", "премиум", "test", "basic", "standard", "premium",
	})
}

func isExplicitVideoRepeatRequest(text string) bool {
	normalized := normalizeText(text)
	return hasAny(normalized, []string{
		"еще раз", "ещё раз", "повторно", "заново", "қайта", "again", "send again", "one more time",
	})
}

func isLocalOfftopic(normalized string) bool {
	return hasAny(normalized, []string{
		"погода", "ауа райы", "политик", "саясат", "религ", "дін", "личная жизнь", "жеке өмір",
		"анекдот", "курс доллара", "футбол", "новости", "жаңалық",
	})
}

func normalizeText(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}

func hasAny(text string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(text, candidate) {
			return true
		}
	}
	return false
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
