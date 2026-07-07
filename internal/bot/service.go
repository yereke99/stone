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
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/yereke99/stone/internal/openai"
	"go.uber.org/zap"
)

const (
	videoSendDelay                     = 1500 * time.Millisecond
	quantityDiscountPricingSource      = "individual_manager_calculation"
	quantityDiscountOfficialConfigUsed = false
)

type outgoingCounterKey struct{}

type outgoingAutomationStageKey struct{}

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
	DownloadURL     string
	FileName        string
	MimeType        string
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

type purposeGreenSender interface {
	SendMessageWithPurpose(ctx context.Context, chatID string, message string, purpose string, allowedGroupChatIDs []string) error
	SendFileByUploadWithPurpose(ctx context.Context, chatID string, filePath string, caption string, purpose string, allowedGroupChatIDs []string) (string, error)
}

type SalesAI interface {
	GenerateReplyText(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.ReplyTextResponse, error)
	AnalyzeCustomerMessage(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.CustomerUnderstanding, error)
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
	llmReply     llmReplyOptions
	audio        audioTranscriptionOptions
}

type llmReplyOptions struct {
	Enabled         bool
	DryRun          bool
	Timeout         time.Duration
	Model           string
	MaxOutputTokens int
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
		llmReply:     loadLLMReplyOptionsFromEnv(),
		audio:        loadAudioTranscriptionOptionsFromEnv(),
	}
}

func loadLLMReplyOptionsFromEnv() llmReplyOptions {
	return llmReplyOptions{
		Enabled:         parseBoolEnv("BOT_LLM_REPLY_ENABLED", false),
		DryRun:          parseBoolEnv("BOT_LLM_REPLY_DRY_RUN", false),
		Timeout:         parseDurationEnv("BOT_LLM_REPLY_TIMEOUT", 15*time.Second),
		Model:           strings.TrimSpace(os.Getenv("BOT_LLM_REPLY_MODEL")),
		MaxOutputTokens: parsePositiveIntEnvAny([]string{"LLM_REPLY_MAX_OUTPUT_TOKENS", "BOT_LLM_REPLY_MAX_TOKENS"}, 1000),
	}
}

func parseBoolEnv(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "t", "true", "yes", "y", "on":
		return true
	case "0", "f", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	if duration, err := time.ParseDuration(raw); err == nil && duration > 0 {
		return duration
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func parsePositiveIntEnvAny(keys []string, fallback int) int {
	for _, key := range keys {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return fallback
		}
		return value
	}
	return fallback
}

func (s *Service) HandleIncomingMessage(ctx context.Context, msg IncomingMessage) error {
	return s.ProcessIncomingWhatsAppMessage(ctx, msg)
}

func (s *Service) ProcessIncomingWhatsAppMessage(ctx context.Context, msg IncomingMessage) (err error) {
	chatID := strings.TrimSpace(msg.ChatID)
	if chatID == "" {
		return fmt.Errorf("chat id is required")
	}
	if s.isAutomationSuppressed(chatID) {
		s.logAutomationSuppressionSkip("incoming message skipped because chat is in automation suppression list",
			chatID,
			zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
		)
		return nil
	}
	if isUnsafeCustomerWhatsAppChatID(chatID) {
		s.info("incoming WhatsApp group message skipped; automation disabled for groups",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
		)
		return nil
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

	if isIncomingAudioMessage(msg) && text == "" {
		transcript, handled, err := s.maybeTranscribeIncomingAudio(ctx, chatID, msg, language)
		if err != nil {
			return err
		}
		if handled && transcript == "" {
			return nil
		}
		if transcript != "" {
			text = transcript
			msg.Text = transcript
		}
	}

	if text != "" {
		var refreshErr error
		language, refreshErr = s.refreshLanguageForCurrentText(ctx, chatID, language, text)
		if refreshErr != nil {
			return refreshErr
		}
	}

	if isIncomingMediaContext(msg) && text == "" {
		return s.handleIncomingMediaContext(ctx, chatID, msg, text, language)
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
	if strings.TrimSpace(msg.TypeMessage) == "quotedMessage" && text != "" {
		s.info("quoted_message_current_text_used",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
			zap.Bool("has_reply_context", hasReplyContext),
		)
	}

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
	if isExplicitOptOutText(text) {
		s.info("incoming customer stop trigger detected",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
			zap.String("normalized_text", NormalizeAdminStopCommand(text)),
		)
		return s.stopAutomationSilently(ctx, chatID, selectedLevelFromConversation(conversation), StopReasonCustomerOptOut, true)
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

	analysis, openAIAnalyzerUsed := s.understandCustomerMessage(ctx, chatID, msg, text, language, conversation)
	if analysis.NumberedQualificationAnswer {
		extracted := make([]string, 0, 3)
		if analysis.Niche != nil {
			extracted = append(extracted, fieldNiche)
		}
		if analysis.Goal != nil {
			extracted = append(extracted, fieldGoal)
		}
		if analysis.Deadline != nil {
			extracted = append(extracted, fieldDeadline)
		}
		s.info("numbered_qualification_answer_detected",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("state", conversation.Stage),
			zap.Strings("qualification_fields_extracted", extracted),
		)
	}
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
	mergedLead := lead
	s.info("lead state merged after analysis",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("intent", analysis.Intent),
		zap.Strings("completed_fields", mapKeys(completedFieldsForLead(mergedLead))),
		zap.Strings("missing_fields", mergedLead.MissingCoreFields()),
		zap.Bool("asks_for_food_examples", analysis.AsksForFoodExamples),
		zap.Bool("asks_for_more_options", analysis.AsksForMoreOptions),
	)
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
		zap.Bool("openai_analyzer_used", openAIAnalyzerUsed),
		zap.Int("selected_level", analysis.SelectedLevel),
		zap.String("state_before", conversation.Stage),
		zap.String("lead_status", conversation.LeadStatus),
		zap.Strings("missing_fields", analysis.MissingFields),
		zap.Strings("extracted_fields", extractedAnalysisFields(analysis)),
	)

	_ = lead
	return s.handleSalesState(ctx, chatID, text, language, conversation, analysis)
}

func (s *Service) handleSalesState(ctx context.Context, chatID string, text string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	state := clientStateForConversation(&conversation)
	if analysis.Intent == IntentDefer || isClientDeferText(text) {
		s.info("state machine deferred reply silently",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("state", state),
		)
		return s.deferClientReply(ctx, chatID, selectedLevelFromConversation(conversation))
	}
	if isOptOutText(text) || analysis.Intent == IntentMute {
		s.info("state machine opt out stopped silently", zap.String("chat_hash", chatFingerprint(chatID)))
		return s.stopAutomationSilently(ctx, chatID, selectedLevelFromConversation(conversation), StopReasonCustomerOptOut, true)
	}
	if analysis.Intent == IntentFrustration {
		s.info("state machine handling customer frustration without restarting questionnaire", zap.String("chat_hash", chatFingerprint(chatID)))
		return s.handleFrustrationReply(ctx, chatID, language, conversation)
	}
	if analysis.Intent == IntentNegativeReaction || analysis.Frustrated || (analysis.ShouldStop && analysis.ShouldHandoff) {
		s.info("state machine negative reaction stopped silently", zap.String("chat_hash", chatFingerprint(chatID)))
		return s.stopAutomationSilently(ctx, chatID, selectedLevelFromConversation(conversation), StopReasonCustomerNegative, false)
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
	if analysis.Intent == IntentHumanRequest {
		level := analysis.SelectedLevel
		if level == 0 {
			level = selectedLevelFromConversation(conversation)
		}
		return s.sendHumanHandoff(ctx, chatID, language, level)
	}
	if analysis.ShouldHandoff {
		level := analysis.SelectedLevel
		if level == 0 {
			level = selectedLevelFromConversation(conversation)
		}
		return s.sendQualifiedLeadHandoff(ctx, chatID, language, level)
	}
	if analysis.Intent == IntentQuantityDiscountQuestion {
		return s.handleQuantityDiscount(ctx, chatID, text, language, conversation, analysis)
	}
	if analysis.Intent == IntentConfusion {
		return s.handleConfusionReply(ctx, chatID, language, conversation)
	}
	if analysis.Intent == IntentFeasibilityQuestion {
		return s.handleFeasibilityQuestion(ctx, chatID, language, conversation)
	}
	if analysis.Intent == IntentVoiceQuestion {
		return s.handleVoiceQuestion(ctx, chatID, text, language, conversation)
	}
	if analysis.Intent == IntentCopyrightQuestion {
		return s.handleCopyrightQuestion(ctx, chatID, language, conversation)
	}
	if analysis.Intent == IntentFormatPreference {
		return s.handleFormatPreference(ctx, chatID, language, conversation, analysis)
	}
	if analysis.Intent == IntentNegativeSelection {
		return s.handleNegativeSelection(ctx, chatID, language, conversation, analysis)
	}
	if analysis.Intent == IntentNicheSpecificCaseRequest {
		return s.handleNicheSpecificCaseRequest(ctx, chatID, language, conversation, analysis)
	}
	if s.shouldSendRelevantAIWorkExamples(conversation, analysis) {
		return s.sendRelevantAIWorkExamples(ctx, chatID, language, conversation, analysis, "")
	}
	if conversation.QuestionnaireOfferSent && isAdsFitQuestion(text) {
		message := FAQAnswerText(faqAds, language) + "\n\n" + questionnaireConfirmationFallbackText(language)
		return s.sendAndRemember(ctx, chatID, message, ClientStateAwaitingQuestionnaireConfirm, selectedLevelFromConversation(conversation))
	}
	if analysis.Intent == IntentFAQ && strings.TrimSpace(analysis.FAQKey) != "" {
		return s.handleFAQ(ctx, chatID, language, conversation, analysis)
	}
	if analysis.AsksForFoodExamples {
		s.info("selected next action",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("action", "answer_food_examples_and_ask_missing"),
		)
		return s.handleFoodExamplesRequest(ctx, chatID, language, conversation)
	}
	if analysis.AsksForMoreOptions {
		s.info("selected next action",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("action", "send_package_options"),
		)
		return s.handleMoreOptionsRequest(ctx, chatID, language, conversation)
	}
	if analysis.Intent == IntentPortfolioRequest && !conversationIsWaitingForBrief(conversation) && !hasPackageFlowStarted(conversation) {
		s.info("selected next action",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("action", "answer_cases_request"),
			zap.Strings("missing_fields", qualificationMissingFields(conversation.Lead)),
		)
		return s.handleCasesRequest(ctx, chatID, text, language, conversation, analysis)
	}
	if state == StageBriefRequested || conversation.QuestionnaireSent || conversation.Lead.BriefRequested {
		return s.handleBriefRequested(ctx, chatID, text, language, conversation, analysis)
	}
	if analysis.Intent == IntentFormatAdvice {
		return s.handleFormatAdvice(ctx, chatID, language, conversation)
	}
	if analysis.Intent == IntentBusinessLink {
		return s.handleBusinessLink(ctx, chatID, text, language, conversation, analysis)
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
	if state == ClientStateAwaitingQuestionnaireConfirm || conversation.QuestionnaireOfferSent {
		return s.handleQuestionnaireConfirmation(ctx, chatID, text, language, conversation, analysis)
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
		if missing := qualificationMissingFields(conversation.Lead); len(missing) > 0 && hasPartialQualificationSignal(conversation, analysis) {
			reply := qualificationFollowupText(language, conversation)
			return s.sendAndRemember(ctx, chatID, reply, ClientStateAwaitingQualification, selectedLevelFromConversation(conversation), qualificationFollowupAskedFields(reply, missing)...)
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
				reply := qualificationFollowupText(language, conversation)
				return s.sendAndRemember(ctx, chatID, reply, ClientStateAwaitingQualification, selectedLevelFromConversation(conversation), qualificationFollowupAskedFields(reply, missing)...)
			}
			if level := selectedLevelFromConversation(conversation); level > 0 {
				if conversation.WantsQuestionnaire || conversation.Lead.WantsQuestionnaire || analysis.WantsQuestionnaire {
					return s.sendQuestionnaireAndAwaitBrief(ctx, chatID, language, level)
				}
				return s.sendQuestionnaireOffer(ctx, chatID, language, level)
			}
		}
		if analysis.Intent == IntentPriceQuestion || analysis.Intent == IntentPortfolioRequest {
			return s.presentPortfolioAndPackages(ctx, chatID, language, conversation, analysis)
		}
		if missing := qualificationMissingFields(conversation.Lead); len(missing) > 0 {
			reply := qualificationFollowupText(language, conversation)
			return s.sendAndRemember(ctx, chatID, reply, ClientStateAwaitingQualification, 0, qualificationFollowupAskedFields(reply, missing)...)
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

func (s *Service) handleQuantityDiscount(ctx context.Context, chatID string, text string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	latest, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	reply := quantityDiscountResponse(language, latest.Lead)
	quantity := strings.TrimSpace(latest.Lead.VideoQuantity)
	if analysis.VideoQuantity != nil && strings.TrimSpace(*analysis.VideoQuantity) != "" {
		quantity = strings.TrimSpace(*analysis.VideoQuantity)
	}
	s.info("quantity discount handling selected",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("incoming_text", strings.TrimSpace(text)),
		zap.String("detected_intent", analysis.Intent),
		zap.String("extracted_quantity", quantity),
		zap.String("pricing_discount_source", quantityDiscountPricingSource),
		zap.Bool("official_discount_config_used", quantityDiscountOfficialConfigUsed),
		zap.String("response_template_id", reply.templateID),
	)
	return s.sendAndRemember(ctx, chatID, reply.text, quantityDiscountReplyStage(conversation), selectedLevelFromConversation(latest), reply.askedFields...)
}

func quantityDiscountReplyStage(conversation Conversation) string {
	switch conversation.Stage {
	case ClientStatePackagesPresented,
		ClientStateAwaitingQuestionnaireConfirm,
		StageBriefRequested:
		return conversation.Stage
	default:
		return ClientStateAwaitingQualification
	}
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
	if analysis.Intent == IntentPortfolioRequest || containsPortfolioRequest(normalized) {
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
	if analysis.Intent == IntentFormatAdvice {
		return s.handleFormatAdvice(ctx, chatID, language, conversation)
	}
	if analysis.Intent == IntentBusinessLink {
		return s.handleBusinessLink(ctx, chatID, text, language, conversation, analysis)
	}
	if analysis.HasBusinessSignal() {
		if missing := qualificationMissingFields(conversation.Lead); len(missing) > 0 {
			reply := qualificationFollowupText(language, conversation)
			return s.sendAndRemember(ctx, chatID, reply, ClientStateAwaitingQualification, level, qualificationFollowupAskedFields(reply, missing)...)
		}
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
	if isAdsFitQuestion(normalized) {
		message := FAQAnswerText(faqAds, language) + "\n\n" + questionnaireConfirmationFallbackText(language)
		return s.sendAndRemember(ctx, chatID, message, ClientStateAwaitingQuestionnaireConfirm, level)
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
	if analysis.Intent == IntentFormatAdvice {
		return s.handleFormatAdvice(ctx, chatID, language, conversation)
	}
	if analysis.Intent == IntentBusinessLink {
		return s.handleBusinessLink(ctx, chatID, text, language, conversation, analysis)
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
		reply := qualificationFollowupText(language, conversation)
		return s.sendAndRemember(ctx, chatID, reply, ClientStateAwaitingQualification, level, qualificationFollowupAskedFields(reply, missing)...)
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
	if leadHasBusinessLink(conversation.Lead) {
		text = BriefTextAfterLink(language)
	}
	if level > 0 {
		if leadHasBusinessLink(conversation.Lead) {
			text = BriefTextAfterLink(language)
		} else {
			text = BriefTextForPackage(language, level)
		}
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
		} else if !isValidPackageInterest(conversation.Lead.SelectedPackage) {
			conversation.Lead.SelectedPackage = packageNeedsManagerRecommendation
		}
		handoffNote := "Запрос менеджера"
		if text := strings.TrimSpace(conversation.LastIncomingText); text != "" {
			handoffNote += ": " + text
		}
		conversation.Lead.FreeText = appendBriefText(conversation.Lead.FreeText, handoffNote)
		conversation.Lead.Notes = appendBriefText(conversation.Lead.Notes, handoffNote)
		conversation.Lead.BriefCompleted = true
		conversation.Lead.ContactBriefReady = true
		conversation.Lead.LeadStatus = LeadStatusHandoffRequired
		conversation.LeadStatus = LeadStatusHandoffRequired
		conversation.HandedOffToOwner = true
		conversation.AutomationClosed = true
		conversation.Stopped = true
		if conversation.TransferredAt.IsZero() {
			conversation.TransferredAt = time.Now().UTC()
		}
	})
	conversation, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if level == 0 {
		level = selectedLevelFromConversation(conversation)
	}
	return s.sendAndRemember(ctx, chatID, HumanHandoffText(language), ClientStateHandedOff, level)
}

func (s *Service) deferClientReply(ctx context.Context, chatID string, level int) error {
	s.store.Update(chatID, func(conversation *Conversation) {
		if level > 0 {
			conversation.SelectedLevel = level
			conversation.Lead.SelectedPackage = packageKey(level)
		}
		conversation.Stage = StageClosing
		conversation.NextFollowupAt = time.Time{}
		conversation.FollowupStage = ""
		conversation.FollowupReferenceAt = time.Time{}
		note := "Клиент отложил ответ"
		if text := strings.TrimSpace(conversation.LastIncomingText); text != "" {
			note += ": " + text
		}
		conversation.Lead.Notes = appendBriefText(conversation.Lead.Notes, note)
	})
	return s.cancelFollowups(ctx, chatID)
}

func (s *Service) stopAutomationSilently(ctx context.Context, chatID string, level int, reason string, optOut bool) error {
	now := time.Now().UTC()
	s.store.Update(chatID, func(conversation *Conversation) {
		if level > 0 {
			conversation.SelectedLevel = level
			conversation.Lead.SelectedPackage = packageKey(level)
		}
		if optOut {
			conversation.Stage = ClientStateOptOut
			conversation.OptOut = true
			conversation.Lead.LeadStatus = LeadStatusMuted
			conversation.LeadStatus = LeadStatusMuted
		} else {
			conversation.Stage = ClientStateStopped
		}
		conversation.Stopped = true
		conversation.AutomationClosed = true
		conversation.StoppedAt = now
		conversation.StoppedBy = StoppedByCustomer
		conversation.StopReason = strings.TrimSpace(reason)
		conversation.NextFollowupAt = time.Time{}
		conversation.FollowupStage = ""
		conversation.FollowupReferenceAt = time.Time{}
		note := "Клиент остановил автоматизацию"
		if optOut {
			note = "Клиент отказался от рассылки"
		}
		if text := strings.TrimSpace(conversation.LastIncomingText); text != "" {
			note += ": " + text
		}
		conversation.Lead.Notes = appendBriefText(conversation.Lead.Notes, note)
	})
	if err := s.store.SuppressAutomation(context.WithoutCancel(ctx), chatID, reason); err != nil {
		return err
	}
	return s.cancelFollowups(ctx, chatID)
}

func (s *Service) sendQualifiedLeadHandoff(ctx context.Context, chatID string, language string, level int) error {
	s.store.Update(chatID, func(conversation *Conversation) {
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		if level > 0 {
			conversation.SelectedLevel = level
			conversation.Lead.SelectedPackage = packageKey(level)
		} else if !isValidPackageInterest(conversation.Lead.SelectedPackage) {
			conversation.Lead.SelectedPackage = packageNeedsManagerRecommendation
		}
		handoffNote := "Квалифицированный лид готов к передаче менеджеру"
		if text := strings.TrimSpace(conversation.LastIncomingText); text != "" {
			handoffNote += ": " + text
		}
		conversation.Lead.FreeText = appendBriefText(conversation.Lead.FreeText, handoffNote)
		conversation.Lead.Notes = appendBriefText(conversation.Lead.Notes, handoffNote)
		conversation.Lead.BriefCompleted = true
		conversation.Lead.ContactBriefReady = true
		conversation.Lead.LeadStatus = LeadStatusHandoffRequired
		conversation.LeadStatus = LeadStatusHandoffRequired
		conversation.HandedOffToOwner = true
		conversation.AutomationClosed = true
		conversation.Stopped = true
		if conversation.TransferredAt.IsZero() {
			conversation.TransferredAt = time.Now().UTC()
		}
	})
	conversation, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if level == 0 {
		level = selectedLevelFromConversation(conversation)
	}
	return s.sendAndRemember(ctx, chatID, QualifiedLeadHandoffText(language, conversation.Lead), ClientStateHandedOff, level)
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
	if err := s.store.SuppressAutomation(context.WithoutCancel(ctx), chatID, StopReasonCustomerOptOut); err != nil {
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

func (s *Service) handleIncomingMediaContext(ctx context.Context, chatID string, msg IncomingMessage, text string, language string) error {
	placeholder := mediaIncomingText(msg.TypeMessage, text)
	if err := s.store.AppendMessage(ctx, chatID, "user", placeholder); err != nil {
		return err
	}
	if err := s.store.MarkIncoming(ctx, chatID, placeholder); err != nil {
		return err
	}
	conversation, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if isConversationClosedForAutomation(conversation) {
		s.info("incoming media saved without automation reply",
			automationSilenceFields(chatID, conversation, "protected_conversation_state_media")...,
		)
		return nil
	}
	reply := NonTextFallbackText(language)
	if strings.TrimSpace(conversation.LastReplyText) == reply {
		return nil
	}
	return s.sendAndRemember(ctx, chatID, reply, mediaFallbackStage(conversation), selectedLevelFromConversation(conversation), qualificationMissingFields(conversation.Lead)...)
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

	if analysis.Intent == IntentDefer || isClientDeferText(text) {
		s.info("local rule used", zap.String("chat_hash", chatFingerprint(chatID)), zap.String("rule", "defer"))
		return true, s.deferClientReply(ctx, chatID, selectedLevelFromConversation(conversation))
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

	if analysis.Intent == IntentPortfolioRequest || containsPortfolioRequest(normalized) || hasAny(normalized, []string{"көрсет", "варианты", "вариант"}) {
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

	if analysis.Intent == IntentObjection || (conversation.Stage != "" && hasAny(normalized, []string{"дорого", "қымбат", "expensive"})) {
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

func (s *Service) sendCustomerWhatsAppMessage(ctx context.Context, chatID string, message string) error {
	if isUnsafeCustomerWhatsAppChatID(chatID) {
		s.blockOutgoingWhatsAppGroupMessage(chatID, WhatsAppPurposeCustomerAutomation)
		return nil
	}
	if s.isAutomationSuppressed(chatID) {
		s.logAutomationSuppressionSkip("outgoing whatsapp message skipped because chat is in automation suppression list", chatID)
		return nil
	}
	allowed, err := s.customerAutomationAllowedForOutgoing(ctx, chatID, "outgoing whatsapp message skipped because automation is closed")
	if err != nil {
		return err
	}
	if !allowed {
		return nil
	}
	if sender, ok := s.sender.(purposeGreenSender); ok {
		return sender.SendMessageWithPurpose(ctx, chatID, message, WhatsAppPurposeCustomerAutomation, nil)
	}
	return s.sender.SendMessage(ctx, chatID, message)
}

func (s *Service) sendCustomerWhatsAppFile(ctx context.Context, chatID string, filePath string, caption string) (string, error) {
	if isUnsafeCustomerWhatsAppChatID(chatID) {
		s.blockOutgoingWhatsAppGroupMessage(chatID, WhatsAppPurposeCustomerAutomation)
		return "", nil
	}
	if s.isAutomationSuppressed(chatID) {
		s.logAutomationSuppressionSkip("outgoing whatsapp file skipped because chat is in automation suppression list", chatID)
		return "", nil
	}
	allowed, err := s.customerAutomationAllowedForOutgoing(ctx, chatID, "outgoing whatsapp file skipped because automation is closed")
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", nil
	}
	if sender, ok := s.sender.(purposeGreenSender); ok {
		return sender.SendFileByUploadWithPurpose(ctx, chatID, filePath, caption, WhatsAppPurposeCustomerAutomation, nil)
	}
	return s.sender.SendFileByUpload(ctx, chatID, filePath, caption)
}

func (s *Service) customerAutomationAllowedForOutgoing(ctx context.Context, chatID string, logMessage string) (bool, error) {
	if s == nil || s.store == nil {
		return true, nil
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false, nil
	}
	exists, err := s.store.ConversationExists(ctx, chatID)
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil
	}
	latest, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return false, err
	}
	if isConversationManuallyStopped(latest) {
		fields := automationSilenceFields(chatID, latest, "outbound_guard_manual_stop")
		s.info(logMessage, fields...)
		return false, nil
	}
	if canSendAutomationToConversation(latest) || outgoingStageAllowsClosedAutomation(ctx) {
		return true, nil
	}
	fields := automationSilenceFields(chatID, latest, "outbound_guard_closed_state")
	s.info(logMessage, fields...)
	return false, nil
}

func outgoingStageAllowsClosedAutomation(ctx context.Context) bool {
	stage, _ := ctx.Value(outgoingAutomationStageKey{}).(string)
	switch strings.TrimSpace(stage) {
	case ClientStateHandedOff, StageHandoffRequired, StageBriefCollected:
		return true
	default:
		return false
	}
}

func (s *Service) sendManagerWhatsAppMessage(ctx context.Context, chatID string, message string) error {
	if !canSendToWhatsAppChat(chatID, WhatsAppPurposeManagerNotification, s.adminChatIDs) {
		s.blockOutgoingWhatsAppGroupMessage(chatID, WhatsAppPurposeManagerNotification)
		return nil
	}
	if sender, ok := s.sender.(purposeGreenSender); ok {
		return sender.SendMessageWithPurpose(ctx, chatID, message, WhatsAppPurposeManagerNotification, s.adminChatIDs)
	}
	return s.sender.SendMessage(ctx, chatID, message)
}

func (s *Service) sendAndRemember(ctx context.Context, chatID string, message string, stage string, selectedLevel int, askedFields ...string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if isUnsafeCustomerWhatsAppChatID(chatID) {
		s.blockOutgoingWhatsAppGroupMessage(chatID, WhatsAppPurposeCustomerAutomation)
		return nil
	}
	if s.isAutomationSuppressed(chatID) {
		s.logAutomationSuppressionSkip("outgoing whatsapp reply skipped because chat is in automation suppression list",
			chatID,
			zap.String("stage", stage),
		)
		return nil
	}
	latest, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if isConversationManuallyStopped(latest) {
		s.info("outgoing whatsapp reply skipped because manual stop is active",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("stage", stage),
		)
		return nil
	}
	if !canSendAutomationToConversation(latest) && stage != ClientStateHandedOff && stage != StageHandoffRequired && stage != StageBriefCollected {
		s.info("outgoing whatsapp reply skipped because automation is closed",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("stage", stage),
		)
		return nil
	}
	requiresPortfolioExamples := needsPortfolioExamplesBeforeFormatQuestion(message)
	backendMessage := message
	backendAction := selectedBackendAction(stage, backendMessage, askedFields, latest)
	s.info("llm final reply path evaluated",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.Bool("llm_reply_enabled", s.llmReply.Enabled),
		zap.Bool("llm_reply_dry_run", s.llmReply.DryRun),
		zap.String("selected_backend_action", backendAction),
		zap.Any("known_fields_snapshot", knownFieldsSnapshot(latest)),
		zap.Strings("missing_fields_snapshot", qualificationMissingFields(latest.Lead)),
	)
	if llmMessage, called := s.maybeGenerateConversationReply(ctx, chatID, backendMessage, stage, selectedLevel, askedFields, latest, backendAction); called {
		if strings.TrimSpace(llmMessage) != "" {
			llmValidation := validateOutgoingReply(llmMessage, stage, latest)
			if !llmValidation.Prevented && llmValidation.Status == "passed" && strings.TrimSpace(llmValidation.Message) != "" {
				message = strings.TrimSpace(llmValidation.Message)
				s.info("selected final customer reply",
					zap.String("chat_hash", chatFingerprint(chatID)),
					zap.String("stage", stage),
					zap.String("final_reply_source", "llm"),
					zap.String("selected_backend_action", backendAction),
					zap.Bool("llm_reply_validation_passed", true),
					zap.String("final_reply_preview", previewText(message, 180)),
				)
			} else {
				s.warn("openai final customer reply rejected by validation; using backend fallback",
					zap.String("chat_hash", chatFingerprint(chatID)),
					zap.String("stage", stage),
					zap.String("selected_backend_action", backendAction),
					zap.String("final_reply_validation", llmValidation.Status),
					zap.Bool("fallback_used", true),
					zap.String("fallback_reason", llmValidation.Status),
					zap.String("llm_reply_preview", previewText(llmMessage, 180)),
					zap.String("backend_reply_preview", previewText(backendMessage, 180)),
				)
				message = backendMessage
			}
		} else {
			s.info("openai final customer reply fallback used",
				zap.String("chat_hash", chatFingerprint(chatID)),
				zap.String("stage", stage),
				zap.String("selected_backend_action", backendAction),
				zap.Bool("fallback_used", true),
				zap.String("fallback_reason", "dry_run_or_generation_failed"),
			)
		}
	}
	validation := validateOutgoingReply(message, stage, latest)
	if validation.Prevented {
		s.warn("outgoing whatsapp reply adjusted by validation gate",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("stage", stage),
			zap.String("final_reply_validation", validation.Status),
			zap.String("final_reply_preview", previewText(validation.Message, 180)),
		)
	}
	message = strings.TrimSpace(validation.Message)
	if message == "" {
		s.info("outgoing whatsapp reply skipped by validation gate",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("stage", stage),
			zap.String("final_reply_validation", validation.Status),
		)
		return nil
	}
	if requiresPortfolioExamples || needsPortfolioExamplesBeforeFormatQuestion(message) {
		ready, err := s.ensurePortfolioExamplesSentBeforeFormatQuestion(ctx, chatID)
		if err != nil {
			return err
		}
		if !ready {
			return nil
		}
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

	sendCtx := context.WithValue(ctx, outgoingAutomationStageKey{}, stage)
	if err := s.sendCustomerWhatsAppMessage(sendCtx, chatID, message); err != nil {
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
	_, err := s.sendVideosWithCaptions(ctx, chatID, files, language, allowRepeat, nil)
	return err
}

func (s *Service) ensurePortfolioExamplesSentBeforeFormatQuestion(ctx context.Context, chatID string) (bool, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false, nil
	}
	if isUnsafeCustomerWhatsAppChatID(chatID) {
		s.blockOutgoingWhatsAppGroupMessage(chatID, WhatsAppPurposeCustomerAutomation)
		return false, nil
	}
	if s.isAutomationSuppressed(chatID) {
		s.logAutomationSuppressionSkip("format question skipped because chat is in automation suppression list", chatID)
		return false, nil
	}
	latest, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return false, err
	}
	if !canSendAutomationToConversation(latest) {
		s.info("format question skipped because automation is closed",
			zap.String("chat_hash", chatFingerprint(chatID)),
		)
		return false, nil
	}

	missing := missingPortfolioExampleVideos(latest)
	if len(missing) == 0 {
		return true, nil
	}
	language := latest.Language
	if language == "" {
		language = "ru"
	}
	sent, err := s.sendVideosWithCaptions(ctx, chatID, missing, language, false, nil)
	if err != nil {
		return false, err
	}
	latest, err = s.store.Snapshot(ctx, chatID)
	if err != nil {
		return false, err
	}
	if missing = missingPortfolioExampleVideos(latest); len(missing) > 0 {
		unavailable := make([]string, 0, len(missing))
		for _, fileName := range missing {
			if _, statErr := os.Stat(filepath.Join(s.videoDir, fileName)); statErr != nil {
				unavailable = append(unavailable, fileName)
			}
		}
		if len(unavailable) == len(missing) {
			// The remaining example files do not exist on disk: this is a
			// deployment/config failure, not a transient send error. Log it
			// clearly and keep the format question blocked so we never claim
			// that examples were shown.
			s.warn("portfolio example videos are unavailable on disk; format question skipped",
				zap.String("chat_hash", chatFingerprint(chatID)),
				zap.String("video_dir", s.videoDir),
				zap.Strings("missing_files", missing),
				zap.Int("sent_now", sent),
			)
			return false, fmt.Errorf("portfolio example videos unavailable before format question: missing %s", strings.Join(missing, ", "))
		}
		s.warn("format question skipped because portfolio examples were not fully sent",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Strings("missing_files", missing),
		)
		return false, fmt.Errorf("portfolio examples not fully sent before format question: missing %s", strings.Join(missing, ", "))
	}
	return true, nil
}

func (s *Service) sendVideosWithCaptions(ctx context.Context, chatID string, files []string, language string, allowRepeat bool, captionsByFile map[string]string) (int, error) {
	if isUnsafeCustomerWhatsAppChatID(chatID) {
		s.blockOutgoingWhatsAppGroupMessage(chatID, WhatsAppPurposeCustomerAutomation)
		return 0, nil
	}
	if s.isAutomationSuppressed(chatID) {
		s.logAutomationSuppressionSkip("portfolio video skipped because chat is in automation suppression list", chatID)
		return 0, nil
	}
	latest, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return 0, err
	}
	if !canSendAutomationToConversation(latest) {
		s.info("portfolio video skipped because automation is closed",
			zap.String("chat_hash", chatFingerprint(chatID)),
		)
		return 0, nil
	}
	sent := 0
	for index, fileName := range dedupeVideos(files) {
		if index > 0 {
			if err := sleepWithContext(ctx, videoSendDelay); err != nil {
				return sent, err
			}
		}
		latest, err := s.store.Snapshot(ctx, chatID)
		if err != nil {
			return sent, err
		}
		protected := s.isAutomationSuppressed(chatID)
		if protected || isConversationManuallyStopped(latest) || !canSendAutomationToConversation(latest) {
			s.info("portfolio video send suppressed due to stopped/protected status",
				zap.String("chat_hash", chatFingerprint(chatID)),
				zap.String("file_name", fileName),
				zap.Bool("suppressed_contact", protected),
				zap.Bool("manual_stop", isConversationManuallyStopped(latest)),
			)
			return sent, nil
		}

		shouldSend, err := s.store.ShouldSendVideo(ctx, chatID, fileName, allowRepeat)
		if err != nil {
			return sent, err
		}
		if !shouldSend {
			s.info("portfolio video skipped because already sent",
				zap.String("chat_hash", chatFingerprint(chatID)),
				zap.String("file_name", fileName),
			)
			continue
		}

		filePath := s.videoFilePath(fileName)
		if _, err := os.Stat(filePath); err != nil {
			s.warn("portfolio video file is unavailable; video not sent",
				zap.String("chat_hash", chatFingerprint(chatID)),
				zap.String("file_name", fileName),
				zap.String("file_path", filePath),
				zap.Error(err),
			)
			continue
		}

		caption := ""
		if explicitCaption, ok := captionsByFile[fileName]; ok {
			caption = strings.TrimSpace(explicitCaption)
		} else if offer, ok := OfferByVideo(fileName); ok {
			caption = strings.TrimSpace(offer.Caption(language))
		}

		messageID, err := s.sendCustomerWhatsAppFile(ctx, chatID, filePath, caption)
		if err != nil {
			s.warn("portfolio video send failed", zap.String("file_name", fileName), zap.Error(err))
			return sent, err
		}
		s.info("portfolio video sent",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("file_name", fileName),
			zap.String("message_id", strings.TrimSpace(messageID)),
		)
		incrementOutgoingCount(ctx)
		sent++
		if err := s.store.LogOutgoingGreenAPIMessage(context.WithoutCancel(ctx), chatID, messageID, "file", caption); err != nil {
			return sent, err
		}
		if offer, ok := OfferByVideo(fileName); ok {
			if err := s.store.RecordOutgoingPackageMessage(context.WithoutCancel(ctx), chatID, messageID, packageKey(offer.Level), fileName, caption); err != nil {
				return sent, err
			}
		}
		if err := s.store.MarkVideoSent(context.WithoutCancel(ctx), chatID, fileName); err != nil {
			return sent, err
		}
	}
	return sent, nil
}

func (s *Service) notifyAdminsIfNeeded(ctx context.Context, chatID string, stage string) {
	if len(s.adminChatIDs) == 0 {
		return
	}
	if isUnsafeCustomerWhatsAppChatID(chatID) {
		s.info("admin notification skipped because source chat is a WhatsApp group",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("stage", stage),
		)
		return
	}
	if s.isAutomationSuppressed(chatID) {
		s.logAutomationSuppressionSkip("admin notification skipped because chat is in automation suppression list",
			chatID,
			zap.String("stage", stage),
		)
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
	sentAny := false
	for _, adminChatID := range s.adminChatIDs {
		if !canSendToWhatsAppChat(adminChatID, WhatsAppPurposeManagerNotification, s.adminChatIDs) {
			s.blockOutgoingWhatsAppGroupMessage(adminChatID, WhatsAppPurposeManagerNotification)
			continue
		}
		if err := s.sendManagerWhatsAppMessage(ctx, adminChatID, message); err != nil {
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
		sentAny = true
	}
	if !sentAny {
		return
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
		"Объём роликов: "+valueOrDash(lead.VideoQuantity),
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
		"менде", "сизде", "сізде", "жарнама", "техникасы", "ролик керек", "максат", "мақсат",
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

func (s *Service) refreshLanguageForCurrentText(ctx context.Context, chatID string, current string, text string) (string, error) {
	detected := strings.TrimSpace(s.detectLanguage(text))
	if detected == "" {
		detected = strings.TrimSpace(current)
	}
	if detected == "" {
		detected = "ru"
	}
	if strings.TrimSpace(current) == detected {
		return detected, nil
	}
	if err := s.store.UpdateLanguage(ctx, chatID, detected); err != nil {
		return "", err
	}
	return detected, nil
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
	sentVideoFiles := make([]string, 0, len(conversation.SentVideoFiles))
	for fileName := range conversation.SentVideoFiles {
		fileName = strings.TrimSpace(fileName)
		if fileName != "" {
			sentVideoFiles = append(sentVideoFiles, fileName)
		}
	}
	sort.Strings(sentVideoFiles)

	payload := struct {
		Stage                string              `json:"stage"`
		LeadStatus           string              `json:"lead_status"`
		Language             string              `json:"language"`
		Lead                 LeadState           `json:"lead"`
		CompletedFields      []string            `json:"completed_fields"`
		AskedFields          []string            `json:"asked_fields"`
		SentVideos           []int               `json:"sent_videos"`
		SentVideoFiles       []string            `json:"sent_video_files"`
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
		SentVideoFiles:       sentVideoFiles,
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
		VideoLevel4: {},
	}
	seen := make(map[string]struct{}, len(files))
	result := make([]string, 0, len(files))
	for _, fileName := range files {
		fileName = normalizeVideoFileForSend(fileName, allowed)
		if fileName == "" {
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

func normalizeVideoFileForSend(fileName string, allowedPackageVideos map[string]struct{}) string {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return ""
	}
	if aiWorkPath := normalizeAIWorkVideoPath(fileName); aiWorkPath != "" {
		return aiWorkPath
	}
	base := strings.TrimSpace(filepath.Base(fileName))
	if _, ok := allowedPackageVideos[base]; ok {
		return base
	}
	return ""
}

func (s *Service) videoFilePath(fileName string) string {
	if aiWorkPath := normalizeAIWorkVideoPath(fileName); aiWorkPath != "" {
		return aiWorkPath
	}
	return filepath.Join(s.videoDir, strings.TrimSpace(filepath.Base(fileName)))
}

func needsPortfolioExamplesBeforeFormatQuestion(message string) bool {
	message = strings.TrimSpace(message)
	if message == "" {
		return false
	}
	for _, question := range []string{
		FormatQuestionText("ru"),
		FormatQuestionText("kk"),
		FormatQuestionText("en"),
		"Қай формат ұнады?",
		"Which format do you like?",
	} {
		if question != "" && strings.Contains(message, question) {
			return true
		}
	}
	return false
}

func missingPortfolioExampleVideos(conversation Conversation) []string {
	missing := make([]string, 0, 3)
	for _, item := range []struct {
		level    int
		fileName string
	}{
		{level: 1, fileName: VideoLevel1},
		{level: 2, fileName: VideoLevel2},
		{level: 3, fileName: VideoLevel3},
	} {
		fileName := item.fileName
		if _, ok := conversation.SentVideoFiles[fileName]; ok {
			continue
		}
		if conversation.SentVideos[item.level] {
			continue
		}
		missing = append(missing, fileName)
	}
	return dedupeVideos(missing)
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
		zap.String("stop_reason", strings.TrimSpace(conversation.StopReason)),
		zap.String("stopped_by", strings.TrimSpace(conversation.StoppedBy)),
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

	if containsAny(normalized, []string{"ниша", "niche", "қай ниша", "сала", "что продаете", "что продаёте", "что продвигаем", "что именно продвигаем"}) {
		add(fieldNiche)
	}
	if containsAny(normalized, []string{"кто ваша аудитория", "ваша аудитория", "кто ваш клиент", "кто клиенты", "target audience"}) {
		add(fieldTargetAudience)
	}
	if containsAny(normalized, []string{"цель", "мақсат", "goal", "заяв", "продаж", "узнаваем", "leads", "sales", "awareness"}) {
		add(fieldGoal)
	}
	if containsAny(normalized, []string{"instagram", "tiktok", "facebook", "whatsapp", "сайт", "website", "референс", "reference", "площад", "платформ", "қай жерде", "where will you use"}) {
		add(fieldPlatform)
	}
	if containsAny(normalized, []string{"срок", "мерзім", "timeline", "когда", "deadline"}) {
		add(fieldDeadline)
	}
	if containsAny(normalized, []string{"ии-ролик", "ai ролик", "ai video", "бұрын ai", "previously used"}) {
		add(fieldPreviousAIAds)
	}
	if containsAny(normalized, []string{"какой формат вам понравился", "какой формат понравился", "какой формат ближе", "which format do you like", "what format did you like"}) {
		add(fieldLikedFormats)
	} else if containsAny(normalized, []string{
		"какой пакет", "какой формат берем", "какой формат берём", "выберите подходящий формат",
		"выберите формат", "қай формат", "which package", "choose your format", "choose package",
		"test", "basic", "standard", "тест", "базов", "стандарт", "premium", "премиум",
	}) {
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

func isIncomingMediaContext(msg IncomingMessage) bool {
	messageType := strings.TrimSpace(msg.TypeMessage)
	switch messageType {
	case "imageMessage", "videoMessage", "audioMessage", "voiceMessage", "documentMessage", "stickerMessage":
		return true
	default:
		return false
	}
}

func isIncomingAudioMessage(msg IncomingMessage) bool {
	switch strings.TrimSpace(msg.TypeMessage) {
	case "audioMessage", "voiceMessage":
		return true
	default:
		return false
	}
}

func mediaIncomingText(messageType string, text string) string {
	text = strings.TrimSpace(text)
	if text != "" {
		return text
	}
	messageType = strings.TrimSpace(messageType)
	if messageType == "" {
		messageType = "media"
	}
	return "[" + messageType + "]"
}

func mediaFallbackStage(conversation Conversation) string {
	switch {
	case conversation.Stage == StageBriefRequested || conversation.QuestionnaireSent || conversation.Lead.BriefRequested:
		return StageBriefRequested
	case conversation.QuestionnaireOfferSent:
		return ClientStateAwaitingQuestionnaireConfirm
	case conversation.PackagesSent || conversation.Lead.OfferSent || conversation.SentPortfolio || conversation.Lead.PortfolioSent:
		return ClientStatePackagesPresented
	default:
		return ClientStateAwaitingQualification
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
	return containsPortfolioRequest(normalized) || hasAny(normalized, []string{
		"көрсет", "покаж", "отправ", "жібер",
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

// qualificationMissingFields: the first qualification stage collects only the
// niche and the video goal. Launch timing is never asked here; it is stored
// only when the customer volunteers it.
func qualificationMissingFields(lead LeadState) []string {
	missing := make([]string, 0, 2)
	if !isValidNiche(lead.Niche) {
		missing = append(missing, fieldNiche)
	}
	if !isValidGoal(lead.Goal) {
		missing = append(missing, fieldGoal)
	}
	return missing
}

func qualificationFollowupText(language string, conversation Conversation) string {
	lead := conversation.Lead
	missing := qualificationMissingFields(lead)
	if len(missing) == 0 {
		return packagesPresentedFallbackText(language)
	}
	nicheKnown := isValidNiche(lead.Niche)
	goalKnown := isValidGoal(lead.Goal)
	switch normalizeLanguageCode(language) {
	case "kk":
		switch {
		case nicheKnown && !goalKnown:
			return fmt.Sprintf("Түсіндім, %s. Роликтің мақсаты қандай: өтінім, сату немесе танымалдық?", strings.TrimSpace(lead.Niche))
		case goalKnown && !nicheKnown:
			return fmt.Sprintf("Түсіндім, мақсат — %s. Не сатасыз / қай ниша екенін жазыңыз.", strings.TrimSpace(lead.Goal))
		default:
			return "Не сатасыз және роликтің мақсаты қандай: өтінім, сату немесе танымалдық?"
		}
	case "en":
		switch {
		case nicheKnown && !goalKnown:
			return fmt.Sprintf("Got it, %s. What is the video goal: leads, sales, or awareness?", strings.TrimSpace(lead.Niche))
		case goalKnown && !nicheKnown:
			return fmt.Sprintf("Got it, the goal is %s. What do you sell / what is your niche?", strings.TrimSpace(lead.Goal))
		default:
			return "Please share what you sell and the video goal: leads, sales, or awareness?"
		}
	default:
		switch {
		case nicheKnown && !goalKnown:
			return fmt.Sprintf("Понял, %s. Какая цель ролика: заявки, продажи или узнаваемость?", strings.TrimSpace(lead.Niche))
		case goalKnown && !nicheKnown:
			return fmt.Sprintf("Понял, цель — %s. Подскажите, пожалуйста, что продаёте / какая у вас ниша?", strings.TrimSpace(lead.Goal))
		default:
			return "Подскажите, пожалуйста, что продаёте и какая цель ролика: заявки, продажи или узнаваемость?"
		}
	}
}

func qualificationFollowupAskedFields(message string, fallback []string) []string {
	asked := fieldsAskedByMessage(message, ClientStateAwaitingQualification)
	if len(asked) > 0 {
		fallback = normalizeFieldList(fallback)
		allowed := make(map[string]bool, len(fallback))
		for _, field := range fallback {
			allowed[field] = true
		}
		filtered := make([]string, 0, len(asked))
		for _, field := range asked {
			if allowed[field] {
				filtered = append(filtered, field)
			}
		}
		if len(filtered) > 0 {
			return normalizeFieldList(filtered)
		}
	}
	return normalizeFieldList(fallback)
}

func leadNicheLocationPhrase(lead LeadState) string {
	niche := strings.TrimSpace(lead.Niche)
	if niche == "" {
		return "по задаче"
	}
	city := strings.TrimSpace(lead.City)
	if city == "" || strings.Contains(normalizeForAnalysis(niche), normalizeForAnalysis(city)) {
		return "ниша — " + niche
	}
	return "ниша — " + niche + " в " + city
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

func hasPartialQualificationSignal(conversation Conversation, analysis CustomerAnalysis) bool {
	if analysis.Niche != nil && isValidNiche(*analysis.Niche) {
		return true
	}
	if analysis.Goal != nil && isValidGoal(*analysis.Goal) {
		return true
	}
	if analysis.ProductOrService != nil && strings.TrimSpace(*analysis.ProductOrService) != "" {
		return true
	}
	if analysis.CampaignContext != nil && strings.TrimSpace(*analysis.CampaignContext) != "" {
		return true
	}
	lead := conversation.Lead
	return isValidNiche(lead.Niche) ||
		isValidGoal(lead.Goal) ||
		strings.TrimSpace(lead.ProductOrService) != "" ||
		strings.TrimSpace(lead.CampaignContext) != ""
}

func (s *Service) shouldSendRelevantAIWorkExamples(conversation Conversation, analysis CustomerAnalysis) bool {
	if conversation.Stage == StageBriefRequested || conversation.QuestionnaireSent || conversation.Lead.BriefRequested || conversation.QuestionnaireOfferSent {
		return false
	}
	switch analysis.Intent {
	case IntentPriceQuestion,
		IntentPackageQuestion,
		IntentPackageSelection,
		IntentQuantityDiscountQuestion,
		IntentHumanRequest,
		IntentReadyToOrder,
		IntentBriefAnswer,
		IntentMute,
		IntentDefer,
		IntentNegativeReaction,
		IntentFrustration:
		return false
	}
	if len(qualificationMissingFields(conversation.Lead)) > 0 {
		return false
	}
	selection := selectAIWorkExamples(conversation.Lead, analysis, aiWorkExamplesLimit())
	if len(selection.Videos) == 0 {
		return false
	}
	for _, video := range selection.Videos {
		if _, sent := conversation.SentVideoFiles[video.Path]; !sent {
			return true
		}
	}
	return false
}

func (s *Service) handleNegativeSelection(ctx context.Context, chatID string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	if s.shouldSendRelevantAIWorkExamples(conversation, analysis) {
		intro := negativeSelectionRelevantExamplesText(language, conversation.Lead)
		return s.sendRelevantAIWorkExamples(ctx, chatID, language, conversation, analysis, intro)
	}
	if missing := qualificationMissingFields(conversation.Lead); len(missing) > 0 {
		message := negativeSelectionMissingText(language, conversation.Lead)
		return s.sendAndRemember(ctx, chatID, message, ClientStateAwaitingQualification, selectedLevelFromConversation(conversation), qualificationFollowupAskedFields(message, missing)...)
	}
	return s.sendAndRemember(ctx, chatID, negativeSelectionFallbackText(language), replyStageForConversation(conversation), selectedLevelFromConversation(conversation), fieldPackageInterest)
}

func (s *Service) sendRelevantAIWorkExamples(ctx context.Context, chatID string, language string, conversation Conversation, analysis CustomerAnalysis, introOverride string) error {
	selection := selectAIWorkExamples(conversation.Lead, analysis, aiWorkExamplesLimit())
	if len(selection.Videos) == 0 {
		return nil
	}

	files := make([]string, 0, len(selection.Videos))
	captions := make(map[string]string, len(selection.Videos))
	for _, video := range selection.Videos {
		if _, sent := conversation.SentVideoFiles[video.Path]; sent {
			continue
		}
		files = append(files, video.Path)
		captions[video.Path] = aiWorkCaption(video, language, selection.Exact)
	}
	if len(files) == 0 {
		return nil
	}

	intro := strings.TrimSpace(introOverride)
	if intro == "" {
		intro = relevantAIWorkIntroText(language, conversation.Lead, selection)
	}
	s.info("selected portfolio tags and videos",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("action", "send_relevant_examples"),
		zap.Strings("portfolio_tags", selection.Tags),
		zap.Strings("video_files", files),
		zap.Bool("exact_match", selection.Exact),
	)
	if err := s.sendAndRemember(ctx, chatID, intro, StagePortfolioSent, selectedLevelFromConversation(conversation)); err != nil {
		return err
	}
	_, err := s.sendVideosWithCaptions(ctx, chatID, files, language, false, captions)
	return err
}

func relevantAIWorkIntroText(language string, lead LeadState, selection AIWorkSelection) string {
	label := aiWorkSelectionLabel(selection, language)
	summary := aiWorkLeadSummary(lead)
	need := aiWorkNeedSummary(lead, selection)
	switch normalizeLanguageCode(language) {
	case "kk":
		if summary != "" && need != "" {
			return "Түсіндім: " + summary + ", " + need + ". Қазір осы бағытқа жақын мысалдарды жіберемін."
		}
		if summary != "" {
			return "Түсіндім: " + summary + ". Қазір " + label + " бойынша жақын мысалдарды жіберемін."
		}
		return "Түсіндім. Қазір сіздің бағытыңызға жақын мысалдарды жіберемін."
	case "en":
		if summary != "" && need != "" {
			return "Got it: " + summary + ", " + need + ". I will send relevant examples now."
		}
		if summary != "" {
			return "Got it: " + summary + ". I will send relevant " + label + " examples now."
		}
		return "Got it. I will send the closest relevant examples now."
	default:
		if summary != "" && need != "" {
			return "Понял вас: " + summary + ", " + need + ". Сейчас отправлю подходящие примеры по " + label + ". После этого смогу предложить формат и цену."
		}
		if summary != "" {
			return "Понял вас: " + summary + ". Сейчас отправлю подходящие примеры по " + label + ". После этого смогу предложить формат и цену."
		}
		return "Понял вас. Сейчас отправлю ближайшие подходящие примеры. После этого смогу предложить формат и цену."
	}
}

func negativeSelectionRelevantExamplesText(language string, lead LeadState) string {
	summary := aiWorkLeadSummary(lead)
	switch normalizeLanguageCode(language) {
	case "kk":
		if summary != "" {
			return "Түсіндім, ол форматтардың ешқайсысы жақын емес. Онда " + summary + " бағытына жақынырақ мысалдарды жіберемін."
		}
		return "Түсіндім, ол форматтардың ешқайсысы жақын емес. Онда жақынырақ мысалдарды жіберемін."
	case "en":
		if summary != "" {
			return "Got it, none of those formats fit. I will send examples closer to " + summary + "."
		}
		return "Got it, none of those formats fit. I will send closer examples."
	default:
		if summary != "" {
			return "Понял, из тех форматов ничего не выбрали. Тогда отправлю примеры ближе под вашу задачу: " + summary + "."
		}
		return "Понял, из тех форматов ничего не выбрали. Тогда отправлю примеры ближе под вашу задачу."
	}
}

func negativeSelectionMissingText(language string, lead LeadState) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсіндім, ол форматтардан ешқайсысы жақын емес. Дұрысын ұсыну үшін нақтылайын: " + lowerFirst(qualificationFollowupText(language, Conversation{Lead: lead}))
	case "en":
		return "Got it, none of those formats fit. To suggest the right one: " + lowerFirst(qualificationFollowupText(language, Conversation{Lead: lead}))
	default:
		return "Понял, из этих форматов ничего не выбрали. Чтобы подобрать точнее: " + lowerFirst(qualificationFollowupText(language, Conversation{Lead: lead}))
	}
}

func negativeSelectionFallbackText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсіндім, ол форматтарды бекітпейміз. Жақынырақ стиль немесе пакет ұсыну үшін міндетіңізге қарай қайта қараймын."
	case "en":
		return "Got it, we will not lock those formats. I can suggest a closer style or package for your task."
	default:
		return "Понял, эти форматы не фиксируем. Могу предложить другой стиль или пакет ближе под вашу задачу."
	}
}

func aiWorkLeadSummary(lead LeadState) string {
	for _, value := range []string{lead.ProductOrService, lead.Niche} {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func aiWorkNeedSummary(lead LeadState, selection AIWorkSelection) string {
	tags := normalizePortfolioTags(selection.Tags)
	hasDrone := stringInSlice("drone", tags)
	hasVisualization := stringInSlice("visualization", tags)
	if hasDrone && hasVisualization {
		return "нужна визуализация перспектив и съёмка с дрона для продажи"
	}
	if goal := strings.TrimSpace(lead.Goal); goal != "" {
		return "цель — " + goal
	}
	return ""
}

func aiWorkSelectionLabel(selection AIWorkSelection, language string) string {
	if len(selection.Videos) > 0 {
		return aiWorkCategoryLabel(selection.Videos[0].Category, language)
	}
	return "вашей нише"
}

func stringInSlice(value string, values []string) bool {
	for _, item := range values {
		if strings.TrimSpace(item) == value {
			return true
		}
	}
	return false
}

func (s *Service) handleFoodExamplesRequest(ctx context.Context, chatID string, language string, conversation Conversation) error {
	missing := qualificationMissingFields(conversation.Lead)
	stage := ClientStateAwaitingQualification
	if conversation.PackagesSent || conversation.Lead.OfferSent || conversation.SentPortfolio || conversation.Lead.PortfolioSent {
		stage = ClientStatePackagesPresented
	}
	if conversation.QuestionnaireOfferSent {
		stage = ClientStateAwaitingQuestionnaireConfirm
	}
	if len(missing) == 0 {
		message := foodExamplesReadyText(language, conversation.Lead)
		if !conversation.PackagesSent && !conversation.Lead.OfferSent && !conversation.SentPortfolio && !conversation.Lead.PortfolioSent {
			if err := s.sendAndRemember(ctx, chatID, message, ClientStateAwaitingQualification, selectedLevelFromConversation(conversation)); err != nil {
				return err
			}
			latest, err := s.store.Snapshot(ctx, chatID)
			if err != nil {
				return err
			}
			return s.presentPortfolioAndPackages(ctx, chatID, language, latest, CustomerAnalysis{Intent: IntentPortfolioRequest})
		}
		return s.sendAndRemember(ctx, chatID, message, stage, selectedLevelFromConversation(conversation))
	}
	message := foodExamplesMissingText(language, conversation.Lead, missing)
	return s.sendAndRemember(ctx, chatID, message, stage, selectedLevelFromConversation(conversation), qualificationFollowupAskedFields(message, missing)...)
}

// handleCasesRequest answers "есть кейсы?" / "как отправите кейсы?" style
// questions. The question text itself is never stored as a niche. When the
// niche or the goal is still unknown, the bot answers the question and asks
// only for the missing fields; when both are known, it confirms and reuses the
// existing portfolio/package sending flow.
func (s *Service) handleCasesRequest(ctx context.Context, chatID string, text string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	missing := qualificationMissingFields(conversation.Lead)
	if len(missing) == 0 {
		if err := s.sendAndRemember(ctx, chatID, casesReadyText(language, conversation.Lead), ClientStateAwaitingQualification, selectedLevelFromConversation(conversation)); err != nil {
			return err
		}
		latest, err := s.store.Snapshot(ctx, chatID)
		if err != nil {
			return err
		}
		return s.presentPortfolioAndPackages(ctx, chatID, language, latest, analysis)
	}
	message := casesRequestQualificationText(language, conversation.Lead, missing, isCasesDeliveryQuestion(text))
	return s.sendAndRemember(ctx, chatID, message, ClientStateAwaitingQualification, selectedLevelFromConversation(conversation), qualificationFollowupAskedFields(message, missing)...)
}

func isCasesDeliveryQuestion(text string) bool {
	normalized := normalizeForAnalysis(text)
	if normalized == "" {
		return false
	}
	return containsAny(normalized, []string{"как", "куда", "калай", "қалай", "how", "where"}) &&
		containsAny(normalized, []string{"отправ", "пришл", "скин", "получ", "жибер", "жібер", "send", "get"})
}

func casesReadyText(language string, lead LeadState) string {
	niche := strings.TrimSpace(lead.Niche)
	switch normalizeLanguageCode(language) {
	case "kk":
		return fmt.Sprintf("Иә, %s бағытына жақын форматтағы мысалдарды жіберемін. Қазір ыңғайлы нұсқаларды таңдаймын.", niche)
	case "en":
		return fmt.Sprintf("Yes, I will send examples in a format close to your niche (%s). Picking the right ones now.", niche)
	default:
		return fmt.Sprintf("Да, отправим примеры по близкому формату для вашей ниши — %s. Сейчас подберу подходящие варианты.", niche)
	}
}

func casesRequestQualificationText(language string, lead LeadState, missing []string, deliveryQuestion bool) string {
	missing = normalizeFieldList(missing)
	languageCode := normalizeLanguageCode(language)

	var base string
	switch languageCode {
	case "kk":
		if deliveryQuestion {
			base = "Видео-мысалдар мен форматтарды осы WhatsApp чатына жіберемін."
		} else {
			base = "Иә, кейстерді осы чатқа жібере аламыз."
		}
	case "en":
		if deliveryQuestion {
			base = "We will send video examples and formats right here in WhatsApp."
		} else {
			base = "Yes, we can send cases right here."
		}
	default:
		if deliveryQuestion {
			base = "Отправим прямо сюда в WhatsApp видео-примеры и форматы."
		} else {
			base = "Да, кейсы можем отправить прямо сюда."
		}
	}

	if sameFields(missing, []string{fieldNiche, fieldGoal}) {
		switch languageCode {
		case "kk":
			return base + " Сізге жақынын таңдау үшін не сататыныңызды және роликтің мақсатын жазыңыз: өтінім, сату немесе танымалдық?"
		case "en":
			return base + " To pick the closest ones, please share what you sell and the video goal: leads, sales, or awareness?"
		default:
			return base + " Чтобы подобрать ближе к вашей задаче, подскажите, пожалуйста, что продаёте и какая цель ролика: заявки, продажи или узнаваемость?"
		}
	}
	return base + " " + qualificationFollowupText(language, Conversation{Lead: lead})
}

func (s *Service) handleMoreOptionsRequest(ctx context.Context, chatID string, language string, conversation Conversation) error {
	stage := StagePackageSuggested
	if conversation.QuestionnaireOfferSent {
		stage = ClientStateAwaitingQuestionnaireConfirm
	}
	return s.sendAndRemember(ctx, chatID, packageOptionsText(language), stage, selectedLevelFromConversation(conversation), fieldPackageInterest)
}

func foodExamplesReadyText(language string, lead LeadState) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Иә, тағам және фермер өнімдеріне AI ролик жасаймыз. Деректер жеткілікті, форматтарды көрсетемін."
	case "en":
		return "Yes, we can make AI videos for food and farm products. The details are enough; I will show formats."
	default:
		return "Да, для еды и фермерских продуктов AI-ролики делаем. Данных достаточно, покажу форматы."
	}
}

func foodExamplesMissingText(language string, lead LeadState, missing []string) string {
	missing = normalizeFieldList(missing)
	switch normalizeLanguageCode(language) {
	case "kk":
		if sameFields(missing, []string{fieldGoal}) {
			return "Иә, тағам және фермер өнімдеріне AI ролик жасаймыз. Роликтің мақсаты қандай: өтінім, сату немесе танымалдық?"
		}
		return "Иә, тағам бағытына AI ролик жасаймыз. Нақтылау үшін: " + missingFieldsLabel(language, missing) + "."
	case "en":
		if sameFields(missing, []string{fieldGoal}) {
			return "Yes, we can make AI videos for food and farm products. What is the video goal: leads, sales, or awareness?"
		}
		return "Yes, we can make food-product AI videos. Please clarify only: " + missingFieldsLabel(language, missing) + "."
	default:
		if sameFields(missing, []string{fieldGoal}) {
			return "Да, для еды и фермерских продуктов AI-ролики делаем. Подскажите цель ролика: заявки, продажи или узнаваемость?"
		}
		return "Да, для еды AI-ролики делаем. Уточните только: " + missingFieldsLabel(language, missing) + "."
	}
}

func (s *Service) handleFormatAdvice(ctx context.Context, chatID string, language string, conversation Conversation) error {
	message := FormatAdviceText(language)
	if followup := formatAdviceFollowupText(language, conversation); strings.TrimSpace(followup) != "" {
		message += "\n\n" + followup
	}
	stage := ClientStateAwaitingQualification
	if conversation.PackagesSent || conversation.Lead.OfferSent || conversation.SentPortfolio || conversation.Lead.PortfolioSent {
		stage = ClientStatePackagesPresented
	}
	if conversation.QuestionnaireOfferSent {
		stage = ClientStateAwaitingQuestionnaireConfirm
	}
	if conversation.Stage == StageBriefRequested || conversation.QuestionnaireSent || conversation.Lead.BriefRequested {
		stage = StageBriefRequested
	}
	return s.sendAndRemember(ctx, chatID, message, stage, selectedLevelFromConversation(conversation), qualificationMissingFields(conversation.Lead)...)
}

func formatAdviceFollowupText(language string, conversation Conversation) string {
	lead := conversation.Lead
	missing := qualificationMissingFields(lead)
	if len(missing) == 0 {
		switch normalizeLanguageCode(language) {
		case "kk":
			return "Аудитория мен қазіргі офферді қысқаша жазсаңыз, нақты формат ұсынамын."
		case "en":
			return "Send the audience and current offer briefly, and I will suggest the exact format."
		default:
			return "Напишите кратко аудиторию и текущий оффер — под это предложим точный формат."
		}
	}
	if isValidNiche(lead.Niche) && !isValidGoal(lead.Goal) {
		return singleMissingQuestion(language, fieldGoal, lead)
	}
	if !isValidNiche(lead.Niche) {
		switch normalizeLanguageCode(language) {
		case "kk":
			return "Нені продвигаем және мақсат қандай: өтінім, сату немесе танымалдық?"
		case "en":
			return "What exactly are we promoting, and what is the goal: leads, sales, or awareness?"
		default:
			return "Что именно продвигаем и какая цель: заявки, продажи или узнаваемость?"
		}
	}
	return askMissingFieldsReply(language, lead, limitFieldsToAsk(missing, 2))
}

func (s *Service) handleBusinessLink(ctx context.Context, chatID string, text string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	if conversation.Stage == StageBriefRequested || conversation.QuestionnaireSent || conversation.Lead.BriefRequested || conversation.QuestionnaireOfferSent {
		s.recordBriefMessage(chatID, text, analysis)
		return s.continueBriefAfterSavedMessage(ctx, chatID, language)
	}
	stage := ClientStateAwaitingQualification
	if conversation.PackagesSent || conversation.Lead.OfferSent || conversation.SentPortfolio || conversation.Lead.PortfolioSent {
		stage = ClientStatePackagesPresented
	}
	if missing := qualificationMissingFields(conversation.Lead); len(missing) > 0 {
		message := briefLinkAcknowledgedNextQuestion(language, qualificationFollowupText(language, conversation))
		return s.sendAndRemember(ctx, chatID, message, stage, selectedLevelFromConversation(conversation), qualificationFollowupAskedFields(message, missing)...)
	}
	message := linkReceivedWithKnownFieldsText(language, conversation.Lead)
	return s.sendAndRemember(ctx, chatID, message, stage, selectedLevelFromConversation(conversation), fieldBrief)
}

func (s *Service) handleFrustrationReply(ctx context.Context, chatID string, language string, conversation Conversation) error {
	latest, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	level := selectedLevelFromConversation(latest)
	if latest.Stage == StageBriefRequested || latest.QuestionnaireSent || latest.Lead.BriefRequested {
		status := briefCompletionStatus(latest)
		if status.complete {
			if managerQualificationForConversation(latest).Ready {
				return s.completeBriefAndHandoff(ctx, chatID, language, level)
			}
			return s.askMissingBeforeManager(ctx, chatID, language, latest, requiredLeadMissingFields(latest))
		}
		next := status.nextQuestion
		if strings.TrimSpace(next) == "" {
			next = BriefContextReturnText(language)
		}
		return s.sendAndRemember(ctx, chatID, frustrationNextQuestionText(language, latest.Lead, next), StageBriefRequested, level, fieldBrief)
	}
	if missing := qualificationMissingFields(latest.Lead); len(missing) > 0 {
		reply := qualificationFollowupText(language, latest)
		return s.sendAndRemember(ctx, chatID, frustrationNextQuestionText(language, latest.Lead, reply), ClientStateAwaitingQualification, level, qualificationFollowupAskedFields(reply, missing)...)
	}
	if managerQualificationForConversation(latest).Ready {
		return s.sendQualifiedLeadHandoff(ctx, chatID, language, level)
	}
	return s.askMissingBeforeManager(ctx, chatID, language, latest, requiredLeadMissingFields(latest))
}

func leadHasBusinessLink(lead LeadState) bool {
	if strings.TrimSpace(lead.WebsiteOrInstagram) != "" || len(lead.ReferenceLinks) > 0 {
		return true
	}
	return extractBusinessLink(strings.Join([]string{lead.Notes, lead.FreeText}, " ")) != ""
}

func isOptOutText(text string) bool {
	return isExplicitOptOutText(text)
}

func isClientDeferText(text string) bool {
	normalized := normalizeForAnalysis(text)
	if normalized == "" {
		return false
	}
	if asksForMoreOptions(normalized) || containsHumanRequest(normalized) || isMuteRequest(normalized) || isExplicitOptOutText(text) {
		return false
	}
	clean := strings.NewReplacer(",", " ", ".", " ", "!", " ", "?", " ", ":", " ", ";", " ").Replace(normalized)
	clean = strings.Join(strings.Fields(clean), " ")
	switch clean {
	case "подумаю", "я подумаю", "подумаем", "мы подумаем", "надо подумать", "нужно подумать",
		"позже", "потом", "кейін", "later", "not now",
		"пока не готов", "пока не готовы", "не готов", "не готовы",
		"хорошо понял", "хорошо поняла", "понял спасибо", "поняла спасибо",
		"спасибо позже", "рахмет кейін", "буду на связи", "будем на связи":
		return true
	}
	if strings.Contains(clean, "на днях") && containsAny(clean, []string{"отпиш", "напиш", "свяж"}) {
		return true
	}
	return containsAny(clean, []string{
		"позже напиш", "напишу позже", "напишем позже", "отпишусь позже", "отпишемся позже",
		"свяжусь позже", "свяжемся позже", "позже свяж", "потом напиш", "потом свяж",
		"я подумаю", "мы подумаем", "надо подумать", "нужно подумать", "подумаю над",
		"пока не готов", "пока не готовы", "не готов сейчас", "не готовы сейчас",
		"хорошо понял", "хорошо поняла", "понял спасибо", "поняла спасибо",
		"пока не реш", "пока не определ", "буду на связи", "будем на связи",
		"i will think", "i'll think", "will get back", "later", "not ready yet",
	})
}

func isExplicitOptOutText(text string) bool {
	if IsAdminStopCommand(text) {
		return true
	}
	normalized := normalizeForAnalysis(text)
	if normalized == "" {
		return false
	}
	clean := strings.Trim(normalized, " .,!?:;")
	if clean == "стоп" || clean == "stop" || clean == "unsubscribe" || clean == "отмена" || clean == "cancel" {
		return true
	}
	if clean == "не интересно" || clean == "not interested" || clean == "no thanks" {
		return true
	}
	if containsAny(normalized, []string{
		"не пишите", "больше не пишите", "не надо писать", "не надо пишите",
		"не отправляйте сообщения", "не присылайте сообщения", "отписаться", "отписка",
		"отпишите меня", "отписать меня", "отпишите от", "unsubscribe", "stop messaging",
		"do not message", "don't message", "жазбаңыз", "мазаламаңыз",
	}) {
		return true
	}
	return false
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
	case "да", "ок", "окей", "хорошо", "можно", "отправьте", "отправляйте", "давайте", "жду", "конечно", "принято", "иә", "иа", "жарайды", "жаксы", "жақсы", "yes", "ok", "okay", "sure":
		return true
	default:
		return containsAny(normalized, []string{"отправьте", "скиньте", "сбросьте", "жду", "жибер", "жібер", "send it", "send me"})
	}
}

func isGenericAcknowledgement(text string) bool {
	normalized := normalizeForAnalysis(text)
	clean := strings.Trim(normalized, " .,!?:;")
	switch clean {
	case "да", "ок", "окей", "хорошо", "понял", "поняла", "принял", "приняла", "принято", "спасибо", "рахмет",
		"иә", "иа", "жарайды", "жаксы", "жақсы", "yes", "ok", "okay", "thanks", "thank you":
		return true
	default:
		return false
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
