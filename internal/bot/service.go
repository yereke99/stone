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
	IDMessage       string
	DedupeKey       string
	ChatID          string
	SenderName      string
	TypeMessage     string
	Text            string
	Timestamp       time.Time
	QuotedMessageID string
	QuotedText      string
	QuotedCaption   string
	QuotedType      string
	QuotedFileName  string
	LocalChatKnown  bool
}

type GreenSender interface {
	SendMessage(ctx context.Context, chatID string, message string) error
	SendFileByUpload(ctx context.Context, chatID string, filePath string, caption string) (string, error)
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
	historyGuard historyGuardRuntime
	autoPackages delayedPackageRuntime
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

func (s *Service) HandleIncomingMessage(ctx context.Context, msg IncomingMessage) error {
	return s.ProcessIncomingWhatsAppMessage(ctx, msg)
}

func (s *Service) ProcessIncomingWhatsAppMessage(ctx context.Context, msg IncomingMessage) (err error) {
	chatID := strings.TrimSpace(msg.ChatID)
	if chatID == "" {
		return fmt.Errorf("chat id is required")
	}
	unlock, err := s.lockChat(ctx, chatID)
	if err != nil {
		return err
	}
	defer unlock()

	standaloneDedupeKey := ""
	if strings.TrimSpace(msg.DedupeKey) == "" && strings.TrimSpace(msg.IDMessage) != "" {
		standaloneDedupeKey = chatID + "|" + strings.TrimSpace(msg.IDMessage)
		decision, dedupeErr := s.store.BeginMessageProcessing(ctx, standaloneDedupeKey)
		if dedupeErr != nil {
			return dedupeErr
		}
		if decision == MessageDedupeDuplicate || decision == MessageDedupeInFlight {
			s.info("duplicate standalone whatsapp message skipped",
				zap.String("chat_hash", chatFingerprint(chatID)),
				zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
				zap.String("decision", string(decision)),
			)
			return nil
		}
		defer func() {
			if finishErr := s.store.FinishMessageProcessing(context.Background(), standaloneDedupeKey, err == nil); finishErr != nil && err == nil {
				err = finishErr
			}
		}()
	}

	counter := &outgoingCounter{}
	ctx = context.WithValue(ctx, outgoingCounterKey{}, counter)

	text := strings.TrimSpace(msg.Text)

	if senderName := strings.TrimSpace(msg.SenderName); senderName != "" {
		s.store.Update(chatID, func(conversation *Conversation) {
			conversation.DisplayName = senderName
			if strings.TrimSpace(conversation.Lead.ClientName) == "" {
				conversation.Lead.ClientName = senderName
			}
		})
	}

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

	hasReplyContext := incomingHasReplyContext(msg)
	replyPackage, replyPackageDetected := detectPackageFromReplyContext(conversation, msg)
	s.info("incoming reply context inspected",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
		zap.Bool("has_reply_context", hasReplyContext),
		zap.String("quoted_type", strings.TrimSpace(msg.QuotedType)),
		zap.Bool("has_quoted_text", strings.TrimSpace(msg.QuotedText) != ""),
		zap.Bool("has_quoted_caption", strings.TrimSpace(msg.QuotedCaption) != ""),
		zap.Bool("package_detected", replyPackageDetected),
		zap.String("package_key", replyPackage),
	)

	if err := s.store.AppendMessage(ctx, chatID, "user", text); err != nil {
		return err
	}
	if err := s.store.MarkIncoming(ctx, chatID, text); err != nil {
		return err
	}
	conversation, err = s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(conversation.FollowupStage) != followupStageWeeklyDiscountSent {
		if err := s.cancelFollowups(ctx, chatID); err != nil {
			return err
		}
		conversation, err = s.store.Snapshot(ctx, chatID)
		if err != nil {
			return err
		}
	}
	if isConversationClosedForAutomation(conversation) {
		s.info("incoming message saved without automation reply",
			automationSilenceFields(chatID, conversation, "protected_conversation_state")...,
		)
		return nil
	}
	if handled, err := s.maybeApplyHistoryGuard(ctx, msg, text, language, conversation); err != nil {
		return err
	} else if handled {
		return nil
	}
	conversation, err = s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if isConversationClosedForAutomation(conversation) {
		s.info("incoming message saved without automation reply",
			automationSilenceFields(chatID, conversation, "protected_conversation_state_after_history_guard")...,
		)
		return nil
	}

	analysis := AnalyzeCustomerMessage(text, conversation.Lead, language)
	if faqKey, ok := detectFAQIntent(text); ok {
		analysis.Intent = IntentFAQ
		analysis.FAQKey = faqKey
	}
	if replyPackageDetected {
		analysis.SelectedLevel = levelByPackageKey(replyPackage)
		analysis.PackageInterest = stringPointer(replyPackage)
		analysis.Intent = IntentPackageSelection
	}
	if conversationIsWaitingForBrief(conversation) {
		analysis = normalizeBriefRequestedAnalysis(text, analysis, conversation)
	}
	if isBriefAnswerForConversation(text, analysis, conversation) {
		analysis.Intent = IntentBriefAnswer
	}
	if !isBusinessRelevantMessage(text, analysis, analysis.FAQKey != "", conversation) {
		s.info("incoming message ignored because it is outside stone production flow",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("state", conversation.Stage),
			zap.String("intent", analysis.Intent),
		)
		return nil
	}
	lead := conversation.Lead
	lead.ApplyAnalysis(analysis)
	if err := s.store.UpdateLead(ctx, chatID, lead); err != nil {
		return err
	}
	if replyPackageDetected {
		level := levelByPackageKey(replyPackage)
		if level > 0 {
			s.store.Update(chatID, func(conversation *Conversation) {
				conversation.SelectedLevel = level
				conversation.CompletedFields[fieldPackageInterest] = true
			})
		}
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

	_ = hadLanguage
	_ = lead
	return s.handleSalesState(ctx, chatID, text, language, conversation, analysis)
}

func (s *Service) handleSalesState(ctx context.Context, chatID string, text string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	state := clientStateForConversation(&conversation)
	if isOptOutText(text) || analysis.Intent == IntentMute {
		s.info("state machine opt out", zap.String("chat_hash", chatFingerprint(chatID)))
		return s.stopClient(ctx, chatID, true)
	}
	if isConversationClosedForAutomation(conversation) || conversation.OptOut || conversation.Stopped || state == ClientStateOptOut || state == ClientStateStopped || state == ClientStateHandedOff {
		fields := automationSilenceFields(chatID, conversation, "state_machine_stopped")
		fields = append(fields, zap.String("computed_state", state))
		s.info("state machine automatic reply stopped", fields...)
		return nil
	}
	if shouldSilenceForStoredHistory(conversation) {
		s.info("state machine automatic reply stopped by history guard",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("classification", conversation.HistoryClassification),
			zap.String("computed_state", state),
		)
		return nil
	}
	if isReplyAfterWeeklyFollowup(conversation) {
		return s.completeFollowupReplyHandoff(ctx, chatID, language, selectedLevelFromConversation(conversation))
	}
	if analysis.Intent == IntentFAQ && strings.TrimSpace(analysis.FAQKey) != "" {
		return s.handleFAQ(ctx, chatID, language, conversation, analysis)
	}
	if state == StageBriefRequested || conversation.QuestionnaireSent || conversation.Lead.BriefRequested {
		return s.handleBriefRequested(ctx, chatID, text, language, conversation, analysis)
	}
	if shouldSuppressRapidFollowup(conversation, analysis) {
		s.info("rapid follow-up suppressed",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("state", state),
		)
		return nil
	}
	if analysis.Intent == IntentPackageSelection {
		level := analysis.SelectedLevel
		if level == 0 {
			level = selectedLevelFromConversation(conversation)
		}
		if level > 0 {
			return s.selectPackageWithoutOpeningBrief(ctx, chatID, language, conversation, level)
		}
	}
	if shouldAskPackageBeforeQuestionnaire(conversation, analysis, text) {
		return s.askPackageBeforeQuestionnaire(ctx, chatID, language)
	}
	if shouldTransferToManagerNow(conversation, analysis) {
		level := analysis.SelectedLevel
		if level == 0 {
			level = selectedLevelFromConversation(conversation)
		}
		if analysis.Intent == IntentBriefAnswer {
			return s.completeBriefAndHandoff(ctx, chatID, language, level)
		}
		if analysis.Intent == IntentHumanRequest {
			return s.sendHumanHandoff(ctx, chatID, language, level)
		}
		return s.sendQuestionnaireAndAwaitBrief(ctx, chatID, language, level)
	}
	if wantsManagerFlow(conversation, analysis) {
		if qualification := managerQualificationForConversation(conversation); !qualification.Ready {
			s.info("handoff postponed because lead is incomplete",
				zap.String("chat_hash", chatFingerprint(chatID)),
				zap.String("state", conversation.Stage),
				zap.String("lead_status", conversation.LeadStatus),
				zap.Strings("missing_fields", qualification.Missing),
			)
			return s.askMissingBeforeManager(ctx, chatID, language, conversation, qualification.Missing)
		}
	}

	switch state {
	case ClientStateNeutralNew:
		if analysis.Intent == IntentPackageSelection {
			level := analysis.SelectedLevel
			if level == 0 {
				level = selectedLevelFromConversation(conversation)
			}
			if level > 0 {
				return s.selectPackageWithoutOpeningBrief(ctx, chatID, language, conversation, level)
			}
		}
		if len(conversation.Lead.MissingCoreFields()) == 0 && hasQualificationSignal(conversation, analysis) {
			return s.presentPortfolioAndPackages(ctx, chatID, language, conversation, analysis)
		}
		return s.sendGreetingAndSchedule(ctx, chatID, language)
	case ClientStateAwaitingQualification:
		if analysis.Intent == IntentPackageSelection {
			level := analysis.SelectedLevel
			if level == 0 {
				level = selectedLevelFromConversation(conversation)
			}
			if level > 0 {
				return s.selectPackageWithoutOpeningBrief(ctx, chatID, language, conversation, level)
			}
		}
		if hasPackageFlowStarted(conversation) {
			if missing := qualificationMissingFields(conversation.Lead); len(missing) > 0 {
				return s.sendAndRemember(ctx, chatID, qualificationFollowupText(language, conversation), ClientStateAwaitingQualification, selectedLevelFromConversation(conversation), missing...)
			}
			if level := selectedLevelFromConversation(conversation); level > 0 {
				if conversation.WantsQuestionnaire || conversation.Lead.WantsQuestionnaire || analysis.WantsQuestionnaire {
					return s.sendQuestionnaireAndAwaitBrief(ctx, chatID, language, level)
				}
				return s.sendQuestionnaireOffer(ctx, chatID, language, level)
			}
		}
		if len(conversation.Lead.MissingCoreFields()) > 0 && shouldClarifyWeakQualificationAnswer(analysis) {
			return s.sendAndRemember(ctx, chatID, qualificationFollowupText(language, conversation), ClientStateAwaitingQualification, 0, qualificationMissingFields(conversation.Lead)...)
		}
		if !hasQualificationSignal(conversation, analysis) {
			return s.sendAndRemember(ctx, chatID, qualificationFollowupText(language, conversation), ClientStateAwaitingQualification, 0, qualificationMissingFields(conversation.Lead)...)
		}
		return s.presentPortfolioAndPackages(ctx, chatID, language, conversation, analysis)
	case ClientStatePackagesPresented:
		return s.handlePackagesPresented(ctx, chatID, text, language, conversation, analysis)
	case ClientStateAwaitingQuestionnaireConfirm:
		return s.handleQuestionnaireConfirmation(ctx, chatID, text, language, conversation, analysis)
	case StageBriefRequested:
		return s.handleBriefRequested(ctx, chatID, text, language, conversation, analysis)
	default:
		if !conversation.InitialMessageSent && !conversation.Lead.HasBeenGreeted {
			return s.sendGreetingAndSchedule(ctx, chatID, language)
		}
		if conversation.Stage == StageBriefRequested || conversation.QuestionnaireSent || conversation.Lead.BriefRequested {
			return s.handleBriefRequested(ctx, chatID, text, language, conversation, analysis)
		}
		if conversation.QuestionnaireOfferSent {
			return s.handleQuestionnaireConfirmation(ctx, chatID, text, language, conversation, analysis)
		}
		if conversation.PackagesSent || conversation.Lead.OfferSent || conversation.SentPortfolio || conversation.Lead.PortfolioSent {
			return s.handlePackagesPresented(ctx, chatID, text, language, conversation, analysis)
		}
		return s.presentPortfolioAndPackages(ctx, chatID, language, conversation, analysis)
	}
}

func (s *Service) presentPortfolioAndPackages(ctx context.Context, chatID string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	if conversation.PackagesSent || conversation.Lead.OfferSent {
		return nil
	}
	_ = analysis
	return s.sendPackageVideosAndAskFormat(ctx, chatID, language, false, time.Now().UTC())
}

func (s *Service) handlePackagesPresented(ctx context.Context, chatID string, text string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	normalized := normalizeText(text)
	level := analysis.SelectedLevel
	if level == 0 {
		level = requestedLevelFromText(text)
	}

	if hasAny(normalized, []string{"анкета", "бриф", "brief"}) {
		if level == 0 {
			level = selectedLevelFromConversation(conversation)
		}
		return s.sendQuestionnaireAndAwaitBrief(ctx, chatID, language, level)
	}
	if analysis.Intent == IntentHumanRequest {
		if level == 0 {
			level = selectedLevelFromConversation(conversation)
		}
		return s.sendHumanHandoff(ctx, chatID, language, level)
	}
	if analysis.Intent == IntentPackageSelection && level > 0 {
		return s.selectPackageWithoutOpeningBrief(ctx, chatID, language, conversation, level)
	}
	if analysis.Intent == IntentReadyToOrder || containsReadySignal(text) {
		if level == 0 {
			level = selectedLevelFromConversation(conversation)
		}
		return s.sendQuestionnaireAndAwaitBrief(ctx, chatID, language, level)
	}
	if analysis.Intent == IntentPortfolioRequest || hasAny(normalized, []string{"портфолио", "видео", "пример", "примеры", "мысал", "көрсет", "portfolio", "example"}) {
		if conversation.SentPortfolio || conversation.Lead.PortfolioSent {
			if isExplicitVideoRepeatRequest(text) {
				return s.sendVideos(ctx, chatID, []string{VideoLevel1, VideoLevel2, VideoLevel3}, language, true)
			}
			return s.sendAndRemember(ctx, chatID, portfolioAlreadySentText(language), ClientStatePackagesPresented, level)
		}
		return s.presentPortfolioAndPackages(ctx, chatID, language, conversation, analysis)
	}
	if analysis.Intent == IntentPackageQuestion && level > 0 {
		return s.sendAndRemember(ctx, chatID, packageDetailText(language, level), ClientStatePackagesPresented, 0)
	}
	if analysis.Intent == IntentPriceQuestion || hasAny(normalized, []string{"цена", "стоимость", "сколько", "прайс", "қанша", "баға", "price", "cost"}) {
		if level > 0 {
			return s.sendAndRemember(ctx, chatID, packagePriceText(language, level), ClientStatePackagesPresented, 0)
		}
		return s.sendAndRemember(ctx, chatID, shortPriceReminderText(language), ClientStatePackagesPresented, 0)
	}
	if analysis.Intent == IntentObjection || hasAny(normalized, []string{"дорого", "қымбат", "expensive"}) {
		return s.sendAndRemember(ctx, chatID, ObjectionText(language), ClientStatePackagesPresented, 0)
	}
	if analysis.Intent == IntentAgreement {
		return s.sendFormatQuestionAndSchedule(ctx, chatID, language, selectedLevelFromConversation(conversation), time.Now().UTC())
	}
	if isLocalOfftopic(normalized) {
		return nil
	}
	return s.sendFormatQuestionAndSchedule(ctx, chatID, language, selectedLevelFromConversation(conversation), time.Now().UTC())
}

func (s *Service) askPackageBeforeQuestionnaire(ctx context.Context, chatID string, language string) error {
	s.store.Update(chatID, func(conversation *Conversation) {
		conversation.MissingFields = []string{fieldPackageInterest}
	})
	return s.sendFormatQuestionAndSchedule(ctx, chatID, language, 0, time.Now().UTC())
}

func (s *Service) handleQuestionnaireConfirmation(ctx context.Context, chatID string, text string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	normalized := normalizeText(text)
	level := analysis.SelectedLevel
	if level == 0 {
		level = selectedLevelFromConversation(conversation)
	}

	if isPositiveConfirmation(text) || analysis.Intent == IntentReadyToOrder || analysis.Intent == IntentPackageSelection {
		return s.sendQuestionnaireAndAwaitBrief(ctx, chatID, language, level)
	}
	if analysis.Intent == IntentHumanRequest {
		return s.sendHumanHandoff(ctx, chatID, language, level)
	}
	if analysis.Intent == IntentPriceQuestion {
		if level > 0 {
			return s.sendAndRemember(ctx, chatID, packagePriceText(language, level), ClientStateAwaitingQuestionnaireConfirm, 0)
		}
		return s.sendAndRemember(ctx, chatID, shortPriceReminderText(language), ClientStateAwaitingQuestionnaireConfirm, 0)
	}
	if analysis.Intent == IntentPortfolioRequest {
		return s.sendAndRemember(ctx, chatID, portfolioAlreadySentText(language), ClientStateAwaitingQuestionnaireConfirm, level)
	}
	if analysis.Intent == IntentObjection || hasAny(normalized, []string{"дорого", "қымбат", "expensive"}) {
		return s.sendAndRemember(ctx, chatID, ObjectionText(language), ClientStateAwaitingQuestionnaireConfirm, 0)
	}
	if looksLikeBriefDetails(text, analysis) {
		s.recordBriefMessage(chatID, text, analysis)
		return s.handleBriefRequested(ctx, chatID, text, language, conversation, analysis)
	}
	if isSoftNo(text) {
		return s.stopClient(ctx, chatID, false)
	}
	return s.sendAndRemember(ctx, chatID, questionnaireConfirmationFallbackText(language), ClientStateAwaitingQuestionnaireConfirm, level)
}

func (s *Service) sendQuestionnaireOffer(ctx context.Context, chatID string, language string, level int) error {
	if err := s.sendAndRemember(ctx, chatID, QuestionnaireOfferText(language), ClientStateAwaitingQuestionnaireConfirm, level); err != nil {
		return err
	}
	return s.scheduleFollowup(ctx, chatID, followupStageQuestionnaireReminder, questionnaireReminderAfter, time.Now().UTC())
}

func (s *Service) sendQuestionnaireOfferWithSelectedVideo(ctx context.Context, chatID string, language string, level int) error {
	if err := s.sendQuestionnaireOffer(ctx, chatID, language, level); err != nil {
		return err
	}
	return s.sendSelectedPackageVideo(ctx, chatID, language, level, false)
}

func (s *Service) sendSelectedPackageVideo(ctx context.Context, chatID string, language string, level int, allowRepeat bool) error {
	offer, ok := OfferByLevel(level)
	if !ok {
		return nil
	}
	return s.sendVideos(ctx, chatID, []string{offer.FileName}, language, allowRepeat)
}

func (s *Service) selectPackageWithoutOpeningBrief(ctx context.Context, chatID string, language string, conversation Conversation, level int) error {
	offer, ok := OfferByLevel(level)
	if !ok {
		return nil
	}
	s.store.Update(chatID, func(conversation *Conversation) {
		conversation.SelectedLevel = level
		conversation.Lead.SelectedPackage = packageKey(level)
		conversation.CompletedFields[fieldPackageInterest] = true
	})
	conversation, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	shouldSend, err := s.store.ShouldSendVideo(ctx, chatID, offer.FileName, false)
	if err != nil {
		return err
	}
	if shouldSend {
		if err := s.sendSelectedPackageVideo(ctx, chatID, language, level, false); err != nil {
			return err
		}
		if err := s.store.UpdateState(ctx, chatID, ClientStatePackagesPresented, level); err != nil {
			return err
		}
		conversation, err = s.store.Snapshot(ctx, chatID)
		if err != nil {
			return err
		}
	}
	if missing := qualificationMissingFields(conversation.Lead); len(missing) > 0 {
		return s.sendAndRemember(ctx, chatID, qualificationFollowupText(language, conversation), ClientStateAwaitingQualification, level, missing...)
	}
	_ = shouldSend
	return s.sendQuestionnaireOffer(ctx, chatID, language, level)
}

func (s *Service) sendQuestionnaireAndAwaitBrief(ctx context.Context, chatID string, language string, level int) error {
	s.store.Update(chatID, func(conversation *Conversation) {
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		if level > 0 {
			conversation.SelectedLevel = level
			conversation.Lead.SelectedPackage = packageKey(level)
		}
	})
	conversation, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if level == 0 {
		level = selectedLevelFromConversation(conversation)
	}

	text := BriefText(language)
	if level > 0 {
		text = BriefTextForPackage(language, level)
	}
	if err := s.sendAndRemember(ctx, chatID, text, StageBriefRequested, level); err != nil {
		return err
	}
	s.store.Update(chatID, func(conversation *Conversation) {
		conversation.QuestionnaireSent = true
		conversation.Lead.BriefRequested = true
	})
	return s.cancelFollowups(ctx, chatID)
}

func (s *Service) completeBriefAndHandoff(ctx context.Context, chatID string, language string, level int) error {
	s.store.Update(chatID, func(conversation *Conversation) {
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.QuestionnaireSent = true
		conversation.Lead.BriefRequested = true
		conversation.Lead.BriefCompleted = true
		conversation.Lead.ContactBriefReady = true
		if level > 0 {
			conversation.SelectedLevel = level
			conversation.Lead.SelectedPackage = packageKey(level)
		} else if !isValidPackageInterest(conversation.Lead.SelectedPackage) {
			conversation.Lead.SelectedPackage = packageNeedsManagerRecommendation
		}
	})
	conversation, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if level == 0 {
		level = selectedLevelFromConversation(conversation)
	}
	if qualification := managerQualificationForConversation(conversation); !qualification.Ready {
		return s.askMissingBeforeManager(ctx, chatID, language, conversation, qualification.Missing)
	}
	if err := s.sendAndRemember(ctx, chatID, BriefCollectedText(language), StageHandoffRequired, level, fieldBrief); err != nil {
		return err
	}
	return s.cancelFollowups(ctx, chatID)
}

func (s *Service) sendHumanHandoff(ctx context.Context, chatID string, language string, level int) error {
	s.store.Update(chatID, func(conversation *Conversation) {
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		if level > 0 {
			conversation.SelectedLevel = level
			conversation.Lead.SelectedPackage = packageKey(level)
		}
	})
	conversation, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if level == 0 {
		level = selectedLevelFromConversation(conversation)
	}
	if qualification := managerQualificationForConversation(conversation); !qualification.Ready {
		return s.askMissingBeforeManager(ctx, chatID, language, conversation, qualification.Missing)
	}
	return s.sendAndRemember(ctx, chatID, HumanHandoffText(language), ClientStateHandedOff, level)
}

func (s *Service) askMissingBeforeManager(ctx context.Context, chatID string, language string, conversation Conversation, missing []string) error {
	if len(missing) == 0 {
		missing = requiredLeadMissingFields(conversation)
	}
	s.store.Update(chatID, func(current *Conversation) {
		current.MissingFields = normalizeFieldList(missing)
		current.WantsQuestionnaire = current.WantsQuestionnaire || current.Lead.WantsQuestionnaire
	})
	stage := ClientStateAwaitingQualification
	if conversation.QuestionnaireOfferSent {
		stage = ClientStateAwaitingQuestionnaireConfirm
	} else if conversation.PackagesSent || conversation.SentPortfolio || conversation.Lead.OfferSent || conversation.Lead.PortfolioSent {
		stage = ClientStatePackagesPresented
	}
	return s.sendAndRemember(ctx, chatID, managerMissingFieldsReply(language, conversation.Lead, missing, conversation.WantsQuestionnaire || conversation.Lead.WantsQuestionnaire), stage, selectedLevelFromConversation(conversation), missing...)
}

func (s *Service) stopClient(ctx context.Context, chatID string, optOut bool) error {
	stage := ClientStateStopped
	if optOut {
		stage = ClientStateOptOut
	}
	s.store.Update(chatID, func(conversation *Conversation) {
		conversation.Stopped = true
		conversation.OptOut = conversation.OptOut || optOut
		if optOut {
			conversation.Lead.LeadStatus = LeadStatusMuted
			conversation.LeadStatus = LeadStatusMuted
		}
	})
	if err := s.store.UpdateState(ctx, chatID, stage, 0); err != nil {
		return err
	}
	return s.cancelFollowups(ctx, chatID)
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

	if analysis.Intent == IntentHumanRequest {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "human_request"), zap.Int("selected_level", analysis.SelectedLevel))
		level := analysis.SelectedLevel
		if level == 0 {
			level = selectedLevelFromConversation(conversation)
		}
		return true, s.sendHumanHandoff(ctx, chatID, language, level)
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
	latest, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if !canSendAutomationToConversation(latest) && stage != ClientStateHandedOff && stage != StageHandoffRequired && stage != StageBriefCollected {
		s.info("outgoing whatsapp reply skipped because automation is closed",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("stage", stage),
		)
		return nil
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
	if err := s.store.LogOutgoingMessage(persistCtx, chatID, "text", message); err != nil {
		return err
	}
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
	latest, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if !canSendAutomationToConversation(latest) {
		s.info("portfolio video skipped because automation is closed",
			zap.String("chat_hash", chatFingerprint(chatID)),
		)
		return nil
	}
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

		messageID, err := s.sender.SendFileByUpload(ctx, chatID, filePath, caption)
		if err != nil {
			s.warn("portfolio video send failed", zap.String("file_name", fileName), zap.Error(err))
			return err
		}
		s.info("portfolio video sent",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("file_name", fileName),
			zap.String("message_id", strings.TrimSpace(messageID)),
		)
		incrementOutgoingCount(ctx)
		if err := s.store.LogOutgoingGreenAPIMessage(context.WithoutCancel(ctx), chatID, messageID, "file", caption); err != nil {
			return err
		}
		if offer, ok := OfferByVideo(fileName); ok {
			if err := s.store.RecordOutgoingPackageMessage(context.WithoutCancel(ctx), chatID, messageID, packageKey(offer.Level), fileName, caption); err != nil {
				return err
			}
		}
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
	if stage != StageHandoffRequired && stage != StageBriefCollected && stage != ClientStateHandedOff {
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
	operatorRequest := isOperatorRequestText(conversation.LastIncomingText)

	var sent bool
	if operatorRequest {
		sent, err = s.store.AdminOperatorNotificationSent(ctx, chatID)
	} else {
		sent, err = s.store.AdminNotificationSent(ctx, chatID)
	}
	if err != nil {
		s.warn("admin notification state check failed",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Bool("operator_request", operatorRequest),
			zap.Error(err),
		)
		return
	}
	if sent {
		return
	}

	qualification := managerQualificationForConversation(conversation)
	if !qualification.Ready {
		s.info("admin notification skipped because lead is incomplete",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Strings("missing_fields", qualification.Missing),
		)
		s.store.Update(chatID, func(current *Conversation) {
			current.MissingFields = qualification.Missing
			current.HandedOffToOwner = false
			current.AutomationClosed = false
			current.Stopped = false
			if current.Stage == ClientStateHandedOff {
				current.Stage = ClientStateAwaitingQuestionnaireConfirm
			}
			if normalizeLeadStatus(current.LeadStatus) == LeadStatusHandoffRequired {
				current.LeadStatus = LeadStatusHot
			}
			if normalizeLeadStatus(current.Lead.LeadStatus) == LeadStatusHandoffRequired {
				current.Lead.LeadStatus = LeadStatusHot
			}
		})
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
		if err := s.store.LogOutgoingMessage(ctx, adminChatID, "text", message); err != nil {
			s.warn("admin notification log failed",
				zap.String("admin_chat_hash", chatFingerprint(adminChatID)),
				zap.String("client_chat_hash", chatFingerprint(chatID)),
				zap.Error(err),
			)
		}
		s.info("admin notification sent",
			zap.String("admin_chat_hash", chatFingerprint(adminChatID)),
			zap.String("client_chat_hash", chatFingerprint(chatID)),
		)
	}

	if operatorRequest {
		err = s.store.MarkAdminOperatorNotified(ctx, chatID)
	} else {
		err = s.store.MarkAdminNotified(ctx, chatID)
	}
	if err != nil {
		s.warn("admin notification mark failed",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Bool("operator_request", operatorRequest),
			zap.Error(err),
		)
	}
}

func adminLeadNotificationText(conversation Conversation) string {
	lead := conversation.Lead
	lines := []string{
		"Новый квалифицированный лид WhatsApp",
		"",
	}
	if name := adminClientName(conversation); name != "" {
		lines = append(lines, "Имя: "+name)
	}
	lines = append(lines,
		"Телефон: "+formatPhoneForAdmin(conversation.ChatID),
		"ChatID: "+strings.TrimSpace(conversation.ChatID),
		"",
		"Ниша: "+valueOrDash(lead.Niche),
		"Цель: "+valueOrDash(lead.Goal),
		"Срок: "+valueOrDash(lead.Deadline),
		"Пакет: "+adminPackageLabel(lead.SelectedPackage),
		"",
		"Намерение клиента: "+adminClientIntent(conversation),
		"Последнее сообщение клиента: "+strings.TrimSpace(conversation.LastIncomingText),
		"",
		"Резюме диалога:",
		strings.TrimSpace(conversation.ConversationSummary),
		"",
		"Статус: квалифицирован, передан менеджеру",
	)
	if link := whatsappLink(conversation.ChatID); link != "" {
		lines = append(lines, "WhatsApp: "+link)
	}
	return strings.Join(lines, "\n")
}

func adminClientName(conversation Conversation) string {
	if name := strings.TrimSpace(conversation.Lead.ClientName); name != "" {
		return name
	}
	return strings.TrimSpace(conversation.DisplayName)
}

func adminClientIntent(conversation Conversation) string {
	if conversation.WantsQuestionnaire || conversation.Lead.WantsQuestionnaire {
		return "хочет продолжить / готов к обработке заявки"
	}
	if isValidPackageInterest(conversation.Lead.SelectedPackage) {
		return "выбрал пакет / готов обсудить заказ"
	}
	return "квалифицированный лид"
}

func adminStatusLabel(status string) string {
	switch normalizeLeadStatus(status) {
	case LeadStatusHandoffRequired:
		return "Передать менеджеру"
	case LeadStatusHot:
		return "Горячий лид"
	case LeadStatusWarm:
		return "Тёплый лид"
	case LeadStatusNew:
		return "Новый лид"
	case LeadStatusClosed:
		return "Закрыт / отказ"
	case LeadStatusMuted:
		return "Не писать автоматически"
	default:
		return valueOrDash(status)
	}
}

func adminStageLabel(stage string) string {
	switch strings.TrimSpace(stage) {
	case StageHandoffRequired, StageBriefCollected:
		return "Бриф принят, нужен менеджер"
	case StageBriefRequested:
		return "Бриф отправлен клиенту"
	case StagePackageSelected:
		return "Пакет выбран"
	case StagePackageSuggested:
		return "Пакеты предложены"
	case StagePortfolioSent:
		return "Портфолио отправлено"
	default:
		return valueOrDash(stage)
	}
}

func adminPackageLabel(packageKey string) string {
	switch strings.ToLower(strings.TrimSpace(packageKey)) {
	case "test":
		return "Test / Тестовый"
	case "basic":
		return "Basic / Базовый"
	case "standard":
		return "Standard / Стандарт"
	case packageNeedsManagerRecommendation:
		return "нужна рекомендация менеджера"
	default:
		return valueOrDash(packageKey)
	}
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

func isOperatorRequestText(text string) bool {
	return containsHumanRequest(normalizeForAnalysis(text))
}

func formatPhoneForAdmin(chatID string) string {
	phone, _, _ := strings.Cut(strings.TrimSpace(chatID), "@")
	var digits strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	raw := digits.String()
	if len(raw) == 11 && strings.HasPrefix(raw, "8") {
		raw = "7" + raw[1:]
	}
	if len(raw) != 11 || !strings.HasPrefix(raw, "7") {
		return raw
	}
	return fmt.Sprintf("+7 %s %s %s %s", raw[1:4], raw[4:7], raw[7:9], raw[9:11])
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
		Stage                string              `json:"stage"`
		LeadStatus           string              `json:"lead_status"`
		Language             string              `json:"language"`
		Lead                 LeadState           `json:"lead"`
		CompletedFields      []string            `json:"completed_fields"`
		AskedFields          []string            `json:"asked_fields"`
		SentVideos           []int               `json:"sent_videos"`
		SentPortfolio        bool                `json:"sent_portfolio"`
		PackagesSent         bool                `json:"packages_sent"`
		WantsQuestionnaire   bool                `json:"wants_questionnaire"`
		AutomationClosed     bool                `json:"automation_closed"`
		TransferredToManager bool                `json:"transferred_to_manager"`
		MissingFields        []string            `json:"missing_fields"`
		ConversationSummary  string              `json:"conversation_summary"`
		BriefAsked           bool                `json:"brief_asked"`
		BriefCollected       bool                `json:"brief_collected"`
		LastIncomingText     string              `json:"last_incoming_text"`
		LastReplyText        string              `json:"last_reply_text"`
		RecentMessages       []map[string]string `json:"recent_messages"`
	}{
		Stage:                conversation.Stage,
		LeadStatus:           normalizeLeadStatus(conversation.LeadStatus),
		Language:             normalizeLanguageCode(conversation.Language),
		Lead:                 conversation.Lead,
		CompletedFields:      mapKeys(conversation.CompletedFields),
		AskedFields:          mapKeys(conversation.AskedFields),
		SentVideos:           sentVideos,
		SentPortfolio:        conversation.SentPortfolio || conversation.Lead.PortfolioSent,
		PackagesSent:         conversation.PackagesSent || conversation.Lead.OfferSent,
		WantsQuestionnaire:   conversation.WantsQuestionnaire || conversation.Lead.WantsQuestionnaire,
		AutomationClosed:     conversation.AutomationClosed,
		TransferredToManager: conversation.HandedOffToOwner || !conversation.TransferredAt.IsZero(),
		MissingFields:        append([]string(nil), conversation.MissingFields...),
		ConversationSummary:  strings.TrimSpace(conversation.ConversationSummary),
		BriefAsked:           conversation.BriefAsked || conversation.Lead.BriefRequested,
		BriefCollected:       conversation.BriefCollected || conversation.Lead.BriefCompleted,
		LastIncomingText:     strings.TrimSpace(conversation.LastIncomingText),
		LastReplyText:        strings.TrimSpace(conversation.LastReplyText),
		RecentMessages:       recent,
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

func automationSilenceFields(chatID string, conversation Conversation, reason string) []zap.Field {
	return []zap.Field{
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("state", conversation.Stage),
		zap.String("lead_status", conversation.LeadStatus),
		zap.Bool("handed_off_to_owner", conversation.HandedOffToOwner),
		zap.Bool("automation_closed", conversation.AutomationClosed),
		zap.Bool("stopped", conversation.Stopped),
		zap.Bool("opt_out", conversation.OptOut),
		zap.String("reason", reason),
	}
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
	if containsAny(normalized, []string{"пакет", "формат", "test", "basic", "standard", "тест", "базов", "стандарт", "premium", "премиум"}) {
		add(fieldPackageInterest)
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

func conversationIsWaitingForBrief(conversation Conversation) bool {
	return conversation.Stage == StageBriefRequested || conversation.QuestionnaireSent || conversation.Lead.BriefRequested
}

func isPackageSuggestionStage(stage string) bool {
	switch stage {
	case StagePackageSuggested, StageOffer, StageAIExperienceChecked:
		return true
	default:
		return false
	}
}

func normalizeBriefRequestedAnalysis(text string, analysis CustomerAnalysis, conversation Conversation) CustomerAnalysis {
	if !conversationIsWaitingForBrief(conversation) {
		return analysis
	}
	if isExplicitOptOutText(text) || analysis.Intent == IntentMute || analysis.Intent == IntentHumanRequest {
		return analysis
	}
	normalized := normalizeForAnalysis(text)
	if analysis.Intent == IntentFAQ && !hasExplicitBriefTextSignal(normalized) {
		return analysis
	}
	looksLikeBrief := looksLikeBriefAnswerInRequestedState(text, analysis, conversation)
	if !looksLikeBrief {
		return analysis
	}
	if briefCompletionStatusWithIncoming(conversation, text).complete {
		analysis.Intent = IntentBriefAnswer
		return analysis
	}
	if analysis.Intent == IntentRefusal || analysis.Intent == IntentFAQ {
		analysis.Intent = IntentAnswer
	}
	return analysis
}

func hasExplicitBriefTextSignal(normalized string) bool {
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "http") || strings.Contains(normalized, "www") || strings.Contains(normalized, "instagram") || strings.Contains(normalized, "@") {
		return true
	}
	return hasNumberedBriefStructure(normalized) ||
		isNoOfferBriefAnswer(normalized) ||
		hasBriefSpecificSignal(normalized) ||
		isBriefLikeBusinessText(normalized)
}

func briefCompletionStatusWithIncoming(conversation Conversation, text string) briefStatus {
	conversation.Lead.FreeText = appendBriefText(conversation.Lead.FreeText, text)
	conversation.Lead.Notes = appendBriefText(conversation.Lead.Notes, text)
	conversation.LastIncomingText = strings.TrimSpace(text)
	return briefCompletionStatus(conversation)
}

func isBriefAnswerForConversation(text string, analysis CustomerAnalysis, conversation Conversation) bool {
	if !conversationIsWaitingForBrief(conversation) {
		return false
	}
	if analysis.Intent == IntentNegativeReaction ||
		analysis.Intent == IntentPortfolioRequest ||
		analysis.Intent == IntentPriceQuestion ||
		analysis.Intent == IntentPackageSelection ||
		analysis.Intent == IntentObjection ||
		analysis.Intent == IntentReadyToOrder ||
		analysis.Intent == IntentAgreement {
		return false
	}

	return looksLikeBriefAnswerInRequestedState(text, analysis, conversation) &&
		briefCompletionStatusWithIncoming(conversation, text).complete
}

func looksLikeBriefAnswerInRequestedState(text string, analysis CustomerAnalysis, conversation Conversation) bool {
	if !conversationIsWaitingForBrief(conversation) {
		return false
	}
	normalized := normalizeForAnalysis(text)
	if normalized == "" || isAgreement(normalized) {
		return false
	}
	if strings.Contains(normalized, "http") || strings.Contains(normalized, "www") || strings.Contains(normalized, "instagram") || strings.Contains(normalized, "@") {
		return true
	}
	if hasNumberedBriefStructure(normalized) {
		return true
	}
	if isNoOfferBriefAnswer(normalized) {
		return true
	}
	if hasBriefSpecificSignal(normalized) && len(strings.Fields(normalized)) >= 2 {
		return true
	}
	return analysis.HasBusinessSignal() || isBriefLikeBusinessText(normalized)
}

func hasNumberedBriefStructure(normalized string) bool {
	return (strings.Contains(normalized, "1)") || strings.Contains(normalized, "1.") || strings.Contains(normalized, "1 ")) &&
		(strings.Contains(normalized, "2)") || strings.Contains(normalized, "2.") || strings.Contains(normalized, "2 ")) &&
		(strings.Contains(normalized, "3)") || strings.Contains(normalized, "3.") || strings.Contains(normalized, "3 "))
}

func hasBriefSpecificSignal(normalized string) bool {
	hasProduct := containsAny(normalized, []string{"рекламируем", "рекламировать", "продвигаем", "продвигать", "что реклам", "товар", "услуг", "продукт", "курс", "прода", "магазин", "салон", "мебель", "обув", "одежд", "advertise", "product", "service"})
	hasValue := containsAny(normalized, []string{"ценность", "преимуществ", "почему", "отлич", "уникаль", "качество", "быстро", "преми", "premium", "value", "benefit"})
	hasAudience := containsAny(normalized, []string{"аудитор", "клиент", "покупател", "бизнесмен", "предпринимател", "муж", "жен", "девуш", "блогер", "инфлю", "target audience", "audience"})
	hasPain := containsAny(normalized, []string{"боль", "желан", "сомнева", "хотят", "нужно", "проблем", "pain", "desire"})
	hasOffer := containsAny(normalized, []string{"оффер", "офер", "скидк", "акци", "бонус", "подар", "рассроч", "заявк", "offer", "discount", "bonus"}) ||
		isNoOfferBriefAnswer(normalized)
	return (hasProduct && (hasValue || hasAudience || hasPain || hasOffer)) ||
		(hasOffer && (hasValue || hasAudience || hasPain))
}

func shouldAcknowledgePostHandoffBrief(text string, language string, conversation Conversation, analysis CustomerAnalysis) bool {
	if conversation.OptOut {
		return false
	}
	if strings.TrimSpace(conversation.LastReplyText) == BriefCollectedText(language) {
		return false
	}
	if !conversation.QuestionnaireSent && !conversation.Lead.BriefRequested {
		return false
	}
	if !managerQualificationForConversation(conversation).Ready {
		return false
	}
	return isBriefAnswerForConversation(text, analysis, conversation)
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

func hasQualificationSignal(conversation Conversation, analysis CustomerAnalysis) bool {
	lead := conversation.Lead
	return isValidNiche(lead.Niche) ||
		isValidGoal(lead.Goal) ||
		isValidDeadline(lead.Deadline) ||
		analysis.HasBusinessSignal() ||
		analysis.Intent == IntentPriceQuestion ||
		analysis.Intent == IntentPortfolioRequest ||
		analysis.Intent == IntentReadyToOrder ||
		containsAny(normalizeForAnalysis(conversation.LastIncomingText), []string{"первый раз", "впервые", "попроб", "протест", "тест", "first time", "try", "test"})
}

func qualificationMissingFields(lead LeadState) []string {
	missing := make([]string, 0, 3)
	if !isValidNiche(lead.Niche) {
		missing = append(missing, fieldNiche)
	}
	if !isValidGoal(lead.Goal) {
		missing = append(missing, fieldGoal)
	}
	if !isValidDeadline(lead.Deadline) {
		missing = append(missing, fieldDeadline)
	}
	return missing
}

func qualificationFollowupText(language string, conversation Conversation) string {
	missing := qualificationMissingFields(conversation.Lead)
	if len(missing) == 0 {
		return packagesPresentedFallbackText(language)
	}
	if len(missing) == 1 {
		return singleMissingQuestion(language, missing[0], conversation.Lead)
	}
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсіндім. Нишаны және роликті қашан іске қосқыңыз келетінін қысқаша жазыңыз."
	case "en":
		return "Got it. Please share the niche and when you want to launch the video."
	default:
		if sameFields(missing, []string{fieldNiche, fieldDeadline}) {
			return "Понял вас. Подскажите, пожалуйста, нишу и когда хотите запустить ролик?"
		}
		if sameFields(missing, []string{fieldNiche, fieldGoal}) {
			return "Понял вас. Подскажите, пожалуйста, нишу и главную цель ролика?"
		}
		if sameFields(missing, []string{fieldGoal, fieldDeadline}) {
			return "Понял вас. Подскажите, пожалуйста, цель и сроки запуска?"
		}
		return "Понял вас. Подскажите, пожалуйста, нишу, цель и сроки запуска одним сообщением."
	}
}

func shouldRecommendTestPackage(conversation Conversation, analysis CustomerAnalysis) bool {
	if analysis.SelectedLevel == 1 || conversation.SelectedLevel == 1 || conversation.Lead.SelectedPackage == "test" {
		return true
	}
	if conversation.Lead.PreviousAIAds != nil && !*conversation.Lead.PreviousAIAds {
		return true
	}
	if analysis.PreviousAIAds != nil && !*analysis.PreviousAIAds {
		return true
	}
	return containsAny(normalizeForAnalysis(conversation.LastIncomingText), []string{
		"первый раз", "впервые", "попроб", "протест", "тест", "first time", "try", "test",
	})
}

func shouldTransferToManagerNow(conversation Conversation, analysis CustomerAnalysis) bool {
	if conversation.AutomationClosed || conversation.HandedOffToOwner || !conversation.TransferredAt.IsZero() {
		return false
	}
	if !managerQualificationForConversation(conversation).Ready {
		return false
	}
	if analysis.WantsQuestionnaire {
		return true
	}
	switch analysis.Intent {
	case IntentReadyToOrder, IntentHumanRequest, IntentBriefAnswer:
		return true
	default:
		return false
	}
}

func wantsManagerFlow(conversation Conversation, analysis CustomerAnalysis) bool {
	if analysis.WantsQuestionnaire {
		return true
	}
	switch analysis.Intent {
	case IntentReadyToOrder, IntentHumanRequest, IntentBriefAnswer:
		return true
	default:
		return false
	}
}

func hasPackageFlowStarted(conversation Conversation) bool {
	return conversation.PackagesSent ||
		conversation.Lead.OfferSent ||
		conversation.SentPortfolio ||
		conversation.Lead.PortfolioSent ||
		selectedLevelFromConversation(conversation) > 0 ||
		isValidPackageInterest(conversation.Lead.SelectedPackage)
}

func shouldAskPackageBeforeQuestionnaire(conversation Conversation, analysis CustomerAnalysis, text string) bool {
	if isValidPackageInterest(conversation.Lead.SelectedPackage) || selectedLevelFromConversation(conversation) > 0 || analysis.SelectedLevel > 0 {
		return false
	}
	if !hasPackageFlowStarted(conversation) {
		return false
	}
	switch analysis.Intent {
	case IntentReadyToOrder, IntentAgreement:
		return true
	}
	if analysis.WantsQuestionnaire {
		return true
	}
	return containsQuestionnaireIntent(normalizeForAnalysis(text)) || containsReadySignal(text)
}

func shouldClarifyWeakQualificationAnswer(analysis CustomerAnalysis) bool {
	if analysis.HasBusinessSignal() {
		return false
	}
	switch analysis.Intent {
	case IntentOther, IntentGreeting, IntentAgreement:
		return true
	default:
		return false
	}
}

func isOptOutText(text string) bool {
	return isExplicitOptOutText(text)
}

func isExplicitOptOutText(text string) bool {
	normalized := normalizeForAnalysis(text)
	if normalized == "" {
		return false
	}
	clean := strings.Trim(normalized, " .,!?:;")
	if clean == "стоп" || clean == "stop" || clean == "unsubscribe" || clean == "отмена" || clean == "cancel" {
		return true
	}
	return containsAny(normalized, []string{
		"не пишите", "больше не пишите", "не надо", "не интересно", "не актуально", "передумал",
		"передумали", "не хочу", "отстан", "отпиш", "unsubscribe", "stop messaging",
		"жазбаңыз", "мазаламаңыз", "керек емес", "not interested", "do not message",
	})
}

func isNoOfferBriefAnswer(normalized string) bool {
	normalized = strings.Trim(normalizeForAnalysis(normalized), " .,!?:;")
	if normalized == "" {
		return false
	}
	switch normalized {
	case "нет", "нету", "жок", "no", "не знаю", "пока нет", "акции нет", "нет акции", "оффера нет", "нет оффера", "офера нет", "нет офера":
		return true
	}
	return containsAny(normalized, []string{
		"акции нет", "нет акции", "оффера нет", "нет оффера", "офера нет", "нет офера",
		"пока нет акции", "пока нет оффера", "пока нет офера", "оффер пока не", "офер пока не",
		"не знаю акции", "не знаю, акции", "акцию не", "оффер не придум", "офер не придум",
	})
}

func isPositiveConfirmation(text string) bool {
	normalized := normalizeForAnalysis(text)
	clean := strings.Trim(normalized, " .,!?:;")
	switch clean {
	case "да", "ок", "окей", "хорошо", "можно", "отправьте", "давайте", "жду", "конечно", "иә", "иа", "жаксы", "жақсы", "yes", "ok", "okay", "sure":
		return true
	default:
		return containsAny(normalized, []string{"отправьте", "скиньте", "сбросьте", "жду", "жибер", "жібер", "send it", "send me"})
	}
}

func isSoftNo(text string) bool {
	clean := strings.Trim(normalizeForAnalysis(text), " .,!?:;")
	switch clean {
	case "нет", "не сейчас", "позже", "жоқ", "кейін", "no", "not now", "later":
		return true
	default:
		return false
	}
}

func looksLikeBriefDetails(text string, analysis CustomerAnalysis) bool {
	normalized := normalizeForAnalysis(text)
	if strings.Contains(normalized, "http") || strings.Contains(normalized, "www") || strings.Contains(normalized, "instagram") || strings.Contains(normalized, "@") {
		return true
	}
	return analysis.HasBusinessSignal() && len(strings.Fields(normalized)) >= 3
}

func shouldSuppressRapidFollowup(conversation Conversation, analysis CustomerAnalysis) bool {
	if conversation.LastReplyAt.IsZero() || time.Since(conversation.LastReplyAt) > 3*time.Second {
		return false
	}
	if conversation.Stage != ClientStatePackagesPresented {
		return false
	}
	switch analysis.Intent {
	case IntentOther, IntentGreeting, IntentAnswer:
		return true
	default:
		return false
	}
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
