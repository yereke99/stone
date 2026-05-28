package bot

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	defaultDelayedPackageInterval = time.Minute
	defaultDelayedPackageLimit    = 100
)

const (
	followupStageSendPackages          = "send_packages_after_greeting"
	followupStageQuestionnaireOffer    = "questionnaire_offer_after_packages"
	followupStageQuestionnaireReminder = "questionnaire_reminder_24h"
	followupStageWeeklyDiscount        = "weekly_discount_1w"
	followupStageWeeklyDiscountSent    = "weekly_discount_sent"
)

const (
	packageSelectionFollowupAfter = time.Hour
	questionnaireReminderAfter    = 24 * time.Hour
	weeklyDiscountFollowupAfter   = 7 * 24 * time.Hour
)

type DelayedPackageOptions struct {
	Enabled       bool
	After         time.Duration
	CheckInterval time.Duration
}

type delayedPackageRuntime struct {
	options DelayedPackageOptions
}

func (s *Service) SetDelayedPackageOptions(options DelayedPackageOptions) {
	if options.After <= 0 {
		options.After = 15 * time.Minute
	}
	if options.CheckInterval <= 0 {
		options.CheckInterval = defaultDelayedPackageInterval
	}
	s.autoPackages = delayedPackageRuntime{options: options}
}

func (s *Service) StartDelayedPackageScheduler(ctx context.Context) {
	options := s.autoPackages.options
	if !options.Enabled || options.After <= 0 {
		return
	}
	if options.CheckInterval <= 0 {
		options.CheckInterval = defaultDelayedPackageInterval
	}
	s.info("follow-up scheduler started",
		zap.Duration("packages_after", options.After),
		zap.Duration("interval", options.CheckInterval),
	)
	go func() {
		ticker := time.NewTicker(options.CheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := s.ProcessDueDelayedPackages(ctx, now.UTC()); err != nil {
					s.warn("follow-up scheduler tick failed", zap.Error(err))
				}
			}
		}
	}()
}

func (s *Service) ProcessDueDelayedPackages(ctx context.Context, now time.Time) error {
	return s.ProcessDueFollowups(ctx, now)
}

func (s *Service) ProcessDueFollowups(ctx context.Context, now time.Time) error {
	options := s.autoPackages.options
	if !options.Enabled || options.After <= 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	candidates, err := s.store.DueFollowupCandidates(ctx, now.UTC(), defaultDelayedPackageLimit)
	if err != nil {
		return err
	}
	for _, conversation := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.processDueFollowupForConversation(ctx, conversation, now.UTC()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) sendDelayedPackagesForConversation(ctx context.Context, conversation Conversation, now time.Time) error {
	return s.processDueFollowupForConversation(ctx, conversation, now)
}

func (s *Service) processDueFollowupForConversation(ctx context.Context, conversation Conversation, now time.Time) error {
	chatID := strings.TrimSpace(conversation.ChatID)
	if chatID == "" {
		return nil
	}
	unlock, err := s.lockChat(ctx, chatID)
	if err != nil {
		return err
	}
	defer unlock()

	latest, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if !shouldSendFollowup(latest, now, s.autoPackages.options.After) {
		s.info("delayed follow-up skipped",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("stage", strings.TrimSpace(latest.FollowupStage)),
			zap.String("reason", followupSkipReason(latest, now, s.autoPackages.options.After)),
		)
		if shouldClearIrrelevantFollowup(latest, now, s.autoPackages.options.After) {
			return s.store.CancelFollowup(context.WithoutCancel(ctx), chatID)
		}
		return nil
	}

	language := latest.Language
	if language == "" {
		language = "ru"
	}
	stage := strings.TrimSpace(latest.FollowupStage)
	switch stage {
	case followupStageSendPackages:
		if err := s.sendPackageVideosAndAskFormat(ctx, chatID, language, true, now.UTC()); err != nil {
			return err
		}
		if err := s.store.MarkAutoPackagesSent(context.WithoutCancel(ctx), chatID, now.UTC()); err != nil {
			return err
		}
	case followupStageQuestionnaireOffer:
		if err := s.sendQuestionnaireOffer(ctx, chatID, language, selectedLevelFromConversation(latest)); err != nil {
			return err
		}
	case followupStageQuestionnaireReminder:
		if err := s.sendQuestionnaireReminder(ctx, chatID, language, selectedLevelFromConversation(latest), now.UTC()); err != nil {
			return err
		}
	case followupStageWeeklyDiscount:
		if err := s.sendWeeklyDiscountFollowup(ctx, chatID, language, selectedLevelFromConversation(latest), now.UTC()); err != nil {
			return err
		}
	default:
		return s.store.CancelFollowup(context.WithoutCancel(ctx), chatID)
	}
	s.info("delayed follow-up sent",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("stage", stage),
	)
	return nil
}

func shouldSendDelayedPackages(conversation Conversation, now time.Time, after time.Duration) bool {
	return strings.TrimSpace(conversation.FollowupStage) == followupStageSendPackages &&
		shouldSendFollowup(conversation, now, after)
}

func shouldSendFollowup(conversation Conversation, now time.Time, after time.Duration) bool {
	if after <= 0 || now.IsZero() {
		return false
	}
	if strings.TrimSpace(conversation.FollowupStage) == "" || conversation.NextFollowupAt.IsZero() || conversation.NextFollowupAt.After(now) {
		return false
	}
	if isConversationClosedForAutomation(conversation) || shouldSilenceForStoredHistory(conversation) {
		return false
	}
	if !conversation.FollowupReferenceAt.IsZero() && conversation.LastIncomingAt.After(conversation.FollowupReferenceAt) {
		return false
	}

	switch strings.TrimSpace(conversation.FollowupStage) {
	case followupStageSendPackages:
		return shouldSendPackageFollowup(conversation, now, after)
	case followupStageQuestionnaireOffer:
		return shouldSendQuestionnaireOfferFollowup(conversation)
	case followupStageQuestionnaireReminder:
		return shouldSendQuestionnaireReminderFollowup(conversation)
	case followupStageWeeklyDiscount:
		return shouldSendWeeklyDiscountFollowup(conversation)
	default:
		return false
	}
}

func shouldSendPackageFollowup(conversation Conversation, now time.Time, after time.Duration) bool {
	if !conversation.AutoPackagesSentAt.IsZero() {
		return false
	}
	if conversation.PackagesSent || conversation.Lead.OfferSent || conversation.SentPortfolio || conversation.Lead.PortfolioSent {
		return false
	}
	if !conversation.InitialMessageSent && !conversation.Lead.HasBeenGreeted {
		return false
	}
	if conversation.Stage != ClientStateAwaitingQualification {
		return false
	}
	greetingSentAt := initialGreetingTime(conversation)
	if greetingSentAt.IsZero() || now.Sub(greetingSentAt) < after {
		return false
	}
	return conversation.LastIncomingAt.IsZero() || !conversation.LastIncomingAt.After(greetingSentAt)
}

func shouldSendQuestionnaireOfferFollowup(conversation Conversation) bool {
	if !(conversation.PackagesSent || conversation.Lead.OfferSent || conversation.SentPortfolio || conversation.Lead.PortfolioSent) {
		return false
	}
	if conversation.QuestionnaireOfferSent || conversation.QuestionnaireSent || conversation.Lead.BriefRequested {
		return false
	}
	return conversation.Stage == ClientStatePackagesPresented
}

func shouldSendQuestionnaireReminderFollowup(conversation Conversation) bool {
	if !conversation.QuestionnaireOfferSent || conversation.QuestionnaireSent {
		return false
	}
	return conversation.Stage == ClientStateAwaitingQuestionnaireConfirm
}

func shouldSendWeeklyDiscountFollowup(conversation Conversation) bool {
	if conversation.QuestionnaireSent || conversation.Lead.BriefRequested {
		return false
	}
	return conversation.QuestionnaireOfferSent || conversation.PackagesSent || conversation.SentPortfolio
}

func delayedPackageSkipReason(conversation Conversation, now time.Time, after time.Duration) string {
	return followupSkipReason(conversation, now, after)
}

func followupSkipReason(conversation Conversation, now time.Time, after time.Duration) string {
	switch {
	case strings.TrimSpace(conversation.FollowupStage) == "" || conversation.NextFollowupAt.IsZero():
		return "no_scheduled_followup"
	case conversation.NextFollowupAt.After(now):
		return "not_due_yet"
	case isConversationClosedForAutomation(conversation):
		return "automation_closed"
	case shouldSilenceForStoredHistory(conversation):
		return "history_guard_silenced"
	case !conversation.FollowupReferenceAt.IsZero() && conversation.LastIncomingAt.After(conversation.FollowupReferenceAt):
		return "client_replied_after_reference"
	case strings.TrimSpace(conversation.FollowupStage) == followupStageSendPackages && !conversation.AutoPackagesSentAt.IsZero():
		return "already_sent"
	case strings.TrimSpace(conversation.FollowupStage) == followupStageSendPackages && (conversation.PackagesSent || conversation.Lead.OfferSent || conversation.SentPortfolio || conversation.Lead.PortfolioSent):
		return "packages_already_sent"
	case strings.TrimSpace(conversation.FollowupStage) == followupStageSendPackages && !conversation.InitialMessageSent && !conversation.Lead.HasBeenGreeted:
		return "initial_greeting_not_sent"
	case strings.TrimSpace(conversation.FollowupStage) == followupStageSendPackages && conversation.Stage != ClientStateAwaitingQualification:
		return "not_waiting_for_qualification"
	case strings.TrimSpace(conversation.FollowupStage) == followupStageQuestionnaireOffer && conversation.QuestionnaireOfferSent:
		return "questionnaire_offer_already_sent"
	case strings.TrimSpace(conversation.FollowupStage) == followupStageQuestionnaireReminder && conversation.QuestionnaireSent:
		return "questionnaire_already_sent"
	default:
		return "not_eligible"
	}
}

func shouldClearIrrelevantFollowup(conversation Conversation, now time.Time, after time.Duration) bool {
	if strings.TrimSpace(conversation.FollowupStage) == "" {
		return false
	}
	if conversation.NextFollowupAt.IsZero() || conversation.NextFollowupAt.After(now) {
		return false
	}
	return !shouldSendFollowup(conversation, now, after)
}

func initialGreetingTime(conversation Conversation) time.Time {
	if !conversation.InitialGreetingSentAt.IsZero() {
		return conversation.InitialGreetingSentAt
	}
	return conversation.LastReplyAt
}
