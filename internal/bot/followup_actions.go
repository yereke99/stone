package bot

import (
	"context"
	"time"

	"go.uber.org/zap"
)

func (s *Service) scheduleFollowup(ctx context.Context, chatID string, stage string, after time.Duration, referenceAt time.Time) error {
	if !s.autoPackages.options.Enabled || after <= 0 {
		return nil
	}
	if isUnsafeCustomerWhatsAppChatID(chatID) {
		s.info("follow-up schedule skipped because chat is a WhatsApp group",
			zap.String("chat_hash", chatFingerprint(chatID)),
		)
		return nil
	}
	if s.isAutomationSuppressed(chatID) {
		s.logAutomationSuppressionSkip("follow-up schedule skipped because chat is in automation suppression list", chatID)
		return nil
	}
	if referenceAt.IsZero() {
		referenceAt = time.Now().UTC()
	}
	return s.store.ScheduleFollowup(context.WithoutCancel(ctx), chatID, stage, referenceAt.Add(after), referenceAt)
}

func (s *Service) cancelFollowups(ctx context.Context, chatID string) error {
	return s.store.CancelFollowup(context.WithoutCancel(ctx), chatID)
}

func (s *Service) sendGreetingAndSchedule(ctx context.Context, chatID string, language string) error {
	if err := s.sendAndRemember(ctx, chatID, QualificationGreetingText(language), ClientStateAwaitingQualification, 0, fieldNiche, fieldGoal); err != nil {
		return err
	}
	return s.scheduleFollowup(ctx, chatID, followupStageSendPackages, s.autoPackages.options.After, time.Now().UTC())
}

func (s *Service) sendPackageVideosAndAskFormat(ctx context.Context, chatID string, language string, fromFollowup bool, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := s.sendVideos(ctx, chatID, []string{VideoLevel1, VideoLevel2, VideoLevel3}, language, false); err != nil {
		return err
	}
	return s.sendFormatQuestionAndSchedule(ctx, chatID, language, 0, now.UTC())
}

func (s *Service) sendFormatQuestionAndSchedule(ctx context.Context, chatID string, language string, level int, referenceAt time.Time) error {
	if referenceAt.IsZero() {
		referenceAt = time.Now().UTC()
	}
	if err := s.sendAndRemember(ctx, chatID, FormatQuestionText(language), ClientStatePackagesPresented, level, fieldPackageInterest); err != nil {
		return err
	}
	return s.scheduleFollowup(ctx, chatID, followupStageQuestionnaireOffer, packageSelectionFollowupAfter, referenceAt.UTC())
}

func (s *Service) sendQuestionnaireReminder(ctx context.Context, chatID string, language string, level int, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := s.sendAndRemember(ctx, chatID, QuestionnaireReminderText(language), ClientStateAwaitingQuestionnaireConfirm, level); err != nil {
		return err
	}
	return s.scheduleFollowup(ctx, chatID, followupStageWeeklyDiscount, weeklyDiscountFollowupAfter, now.UTC())
}

func (s *Service) sendWeeklyDiscountFollowup(ctx context.Context, chatID string, language string, level int, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := s.sendVideos(ctx, chatID, []string{VideoLevel3}, language, false); err != nil {
		return err
	}
	if err := s.sendAndRemember(ctx, chatID, WeeklyDiscountFollowupText(language), ClientStatePackagesPresented, level); err != nil {
		return err
	}
	return s.store.MarkFollowupSent(context.WithoutCancel(ctx), chatID, followupStageWeeklyDiscountSent, now.UTC())
}
