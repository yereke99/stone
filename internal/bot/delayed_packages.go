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
	s.info("delayed package scheduler started",
		zap.Duration("after", options.After),
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
					s.warn("delayed package scheduler tick failed", zap.Error(err))
				}
			}
		}
	}()
}

func (s *Service) ProcessDueDelayedPackages(ctx context.Context, now time.Time) error {
	options := s.autoPackages.options
	if !options.Enabled || options.After <= 0 {
		return nil
	}
	candidates, err := s.store.DelayedPackageCandidates(ctx, now, options.After, defaultDelayedPackageLimit)
	if err != nil {
		return err
	}
	for _, conversation := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.sendDelayedPackagesForConversation(ctx, conversation, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) sendDelayedPackagesForConversation(ctx context.Context, conversation Conversation, now time.Time) error {
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
	if !shouldSendDelayedPackages(latest, now, s.autoPackages.options.After) {
		s.info("delayed package skipped",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("reason", delayedPackageSkipReason(latest, now, s.autoPackages.options.After)),
		)
		return nil
	}
	language := latest.Language
	if language == "" {
		language = "ru"
	}
	if err := s.sendVideos(ctx, chatID, []string{VideoLevel1, VideoLevel2, VideoLevel3}, language, false); err != nil {
		return err
	}
	if err := s.store.MarkAutoPackagesSent(context.WithoutCancel(ctx), chatID, now.UTC()); err != nil {
		return err
	}
	s.info("delayed package videos sent",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.Int("video_count", 3),
	)
	return nil
}

func shouldSendDelayedPackages(conversation Conversation, now time.Time, after time.Duration) bool {
	if after <= 0 || now.IsZero() {
		return false
	}
	if conversation.AutoPackagesSentAt.IsZero() == false {
		return false
	}
	if conversation.HistoryCheckedAt.IsZero() || conversation.HistoryClassification != HistoryClassificationNewClient || conversation.HistoryDetected {
		return false
	}
	if conversation.DoNotAutoStart || conversation.LegacyExisting || conversation.LegacyProcessed || conversation.LegacyReengagement {
		return false
	}
	if conversation.AutomationClosed || conversation.HandedOffToOwner || !conversation.TransferredAt.IsZero() ||
		conversation.Stopped || conversation.OptOut || conversation.Stage == ClientStateHandedOff ||
		conversation.Stage == ClientStateStopped || conversation.Stage == ClientStateOptOut {
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
	greetingSentAt := conversation.InitialGreetingSentAt
	if greetingSentAt.IsZero() {
		greetingSentAt = conversation.LastReplyAt
	}
	if greetingSentAt.IsZero() || now.Sub(greetingSentAt) < after {
		return false
	}
	return conversation.LastIncomingAt.IsZero() || !conversation.LastIncomingAt.After(greetingSentAt)
}

func delayedPackageSkipReason(conversation Conversation, now time.Time, after time.Duration) string {
	switch {
	case !conversation.AutoPackagesSentAt.IsZero():
		return "already_sent"
	case conversation.HistoryCheckedAt.IsZero() || conversation.HistoryClassification != HistoryClassificationNewClient || conversation.HistoryDetected:
		return "not_confirmed_new_client"
	case conversation.DoNotAutoStart || conversation.LegacyExisting || conversation.LegacyProcessed || conversation.LegacyReengagement:
		return "legacy_or_do_not_auto_start"
	case conversation.AutomationClosed || conversation.HandedOffToOwner || !conversation.TransferredAt.IsZero():
		return "automation_closed"
	case conversation.Stopped || conversation.OptOut:
		return "stopped_or_opt_out"
	case conversation.PackagesSent || conversation.Lead.OfferSent || conversation.SentPortfolio || conversation.Lead.PortfolioSent:
		return "packages_already_sent"
	case !conversation.InitialMessageSent && !conversation.Lead.HasBeenGreeted:
		return "initial_greeting_not_sent"
	case conversation.Stage != ClientStateAwaitingQualification:
		return "not_waiting_for_qualification"
	case initialGreetingTime(conversation).IsZero() || now.Sub(initialGreetingTime(conversation)) < after:
		return "not_due_yet"
	case conversation.LastIncomingAt.After(initialGreetingTime(conversation)):
		return "client_replied_after_greeting"
	default:
		return "not_eligible"
	}
}

func initialGreetingTime(conversation Conversation) time.Time {
	if !conversation.InitialGreetingSentAt.IsZero() {
		return conversation.InitialGreetingSentAt
	}
	return conversation.LastReplyAt
}
