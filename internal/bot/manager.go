package bot

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"
)

type Reply struct {
	Text string
}

type Manager struct {
	store     StateStore
	portfolio PortfolioLinks
	logger    *zap.Logger
}

func NewManager(store StateStore, portfolio PortfolioLinks, logger *zap.Logger) *Manager {
	return &Manager{
		store:     store,
		portfolio: portfolio,
		logger:    logger,
	}
}

func (m *Manager) HandleIncoming(ctx context.Context, userID string, text string) ([]Reply, error) {
	userID = strings.TrimSpace(userID)
	text = strings.TrimSpace(text)
	if userID == "" {
		return nil, errors.New("user id is required")
	}
	if text == "" {
		return nil, nil
	}

	state, exists, err := m.store.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !exists {
		state = State{
			UserID:    userID,
			Language:  DetectLanguage(text),
			Step:      StepWelcome,
			UpdatedAt: time.Now().UTC(),
		}
	}

	replies := m.nextReplies(&state, text, exists)
	state.UpdatedAt = time.Now().UTC()

	if err := validateMessagePolicy(replies); err != nil {
		m.logger.Error("bot message policy violation", zap.Error(err), zap.String("user_id", userID))
		return nil, err
	}
	if err := m.store.Save(ctx, state); err != nil {
		return nil, err
	}

	return replies, nil
}

func (m *Manager) nextReplies(state *State, text string, exists bool) []Reply {
	analysis := AnalyzeCustomerMessage(text, state.Lead, string(state.Language))
	state.Lead.ApplyAnalysis(analysis)
	if analysis.PreviousAIAds != nil {
		state.UsedAIVideos = analysis.PreviousAIAds
	}
	state.Lead = refreshLeadStatus(state.Lead, string(state.Step))

	if !exists || state.Step == StepWelcome {
		if !analysis.HasBusinessSignal() {
			state.Step = StepDiagnosticsGoal
			state.Lead.HasBeenGreeted = true
			state.Lead = refreshLeadStatus(state.Lead, StageQualification)
			return singleReply(welcomeMessage(state.Language))
		}
	}

	if analysis.Intent == IntentRefusal {
		state.Step = StepClosing
		return singleReply(refusalText(string(state.Language)))
	}
	if analysis.Intent == IntentPriceQuestion || containsAny(normalize(text), []string{"цена", "стоимость", "сколько", "прайс", "қанша", "баға", "price", "cost"}) {
		level := requestedLevelFromText(text)
		if level == 0 {
			level = analysis.SelectedLevel
		}
		if level == 0 && state.Lead.SelectedPackage != "" {
			level = selectedLevelFromLead(state.Lead)
		}
		alreadyOffered := state.Lead.OfferSent || state.Lead.SelectedPackage != ""
		state.Step = StepOffer
		state.Lead.OfferSent = true
		state.Lead = refreshLeadStatus(state.Lead, StagePackageSuggested)
		if level > 0 {
			return singleReply(packagePriceText(string(state.Language), level))
		}
		if alreadyOffered {
			return singleReply(shortPriceReminderText(string(state.Language)))
		}
		return singleReply(PriceText(string(state.Language)))
	}
	if containsObjection(text) {
		return singleReply(objectionMessage(state.Language))
	}
	if containsPortfolioRequest(text) {
		state.Lead.PortfolioSent = true
		state.Lead = refreshLeadStatus(state.Lead, StagePortfolioSent)
		return repliesFromTexts(portfolioMessages(state.Language, m.portfolio))
	}
	if containsReadySignal(text) {
		state.Step = StepClosing
		state.Lead.BriefRequested = true
		state.Lead = refreshLeadStatus(state.Lead, StageBriefRequested)
		return singleReply(questionnaireMessage(state.Language))
	}

	conversation := Conversation{
		ChatID:        state.UserID,
		Stage:         string(state.Step),
		SelectedLevel: selectedLevelFromLead(state.Lead),
		Lead:          state.Lead,
	}
	if reply, ok := buildReturningLeadReply(string(state.Language), conversation, analysis); ok {
		state.Step = stepFromStage(reply.stage)
		state.Lead = applyReplyStateToLead(state.Lead, reply.stage, reply.level)
		return singleReply(reply.text)
	}

	if reply, ok := buildLeadReply(string(state.Language), state.Lead, analysis, conversation); ok {
		state.Step = stepFromStage(reply.stage)
		state.Lead = applyReplyStateToLead(state.Lead, reply.stage, reply.level)
		return singleReply(reply.text)
	}

	switch state.Step {
	case StepDiagnosticsGoal:
		state.Step = StepDiagnosticsPlatform
		return singleReply(askGoalMessage(state.Language))
	case StepDiagnosticsPlatform:
		state.Step = StepExpertise
		return singleReply(askPlatformMessage(state.Language))
	case StepExpertise:
		state.Step = StepOffer
		return singleReply(askUsedAIMessage(state.Language))
	case StepOffer:
		usedBefore := parseUsedAIVideos(text)
		if usedBefore == nil {
			return singleReply(clarifyUsedAIMessage(state.Language))
		}
		state.UsedAIVideos = usedBefore
		state.Step = StepPortfolio
		state.Lead.OfferSent = true
		state.Lead = refreshLeadStatus(state.Lead, StagePackageSuggested)
		return singleReply(offerMessage(state.Language, *usedBefore))
	case StepPortfolio:
		state.Step = StepClosing
		return singleReply(portfolioPromptMessage(state.Language))
	default:
		state.Step = StepClosing
		return singleReply(questionnaireMessage(state.Language))
	}
}

func stepFromStage(stage string) Step {
	switch stage {
	case StageQualification:
		return StepDiagnosticsGoal
	case StageDiagnosis, StagePlatformDetected, StageAIExperienceChecked:
		return StepExpertise
	case StageOffer, StagePackageSuggested:
		return StepOffer
	case StagePortfolio, StagePortfolioSent:
		return StepPortfolio
	case StageClosing, StagePackageSelected, StageBriefRequested, StageBriefCollected:
		return StepClosing
	default:
		return StepDiagnosticsGoal
	}
}

func selectedLevelFromLead(lead LeadState) int {
	switch lead.SelectedPackage {
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

func applyReplyStateToLead(lead LeadState, stage string, level int) LeadState {
	if stage == StagePortfolioSent || stage == StagePortfolio {
		lead.PortfolioSent = true
	}
	if stage == StageOffer || stage == StagePackageSuggested || stage == StageAIExperienceChecked {
		lead.OfferSent = true
	}
	if stage == StageBriefRequested {
		lead.BriefRequested = true
		if level > 0 {
			lead.SelectedPackage = packageKey(level)
		}
	}
	if stage == StageBriefCollected {
		lead.BriefRequested = true
		lead.BriefCompleted = true
		lead.ContactBriefReady = true
	}
	return refreshLeadStatus(lead, stage)
}

func singleReply(text string) []Reply {
	return []Reply{{Text: text}}
}

func repliesFromTexts(texts []string) []Reply {
	replies := make([]Reply, 0, len(texts))
	for _, text := range texts {
		replies = append(replies, Reply{Text: text})
	}
	return replies
}

func parseUsedAIVideos(text string) *bool {
	normalized := normalize(text)
	negative := containsAny(normalized, []string{
		"нет", "никогда", "не использ", "не тест", "no", "never", "not yet", "not used", "жоқ", "қолданба",
	})
	if negative {
		value := false
		return &value
	}

	positive := containsAny(normalized, []string{
		"да", "использ", "тест", "yes", "used", "already", "иә", "қолдандым", "болды",
	})
	if positive {
		value := true
		return &value
	}

	return nil
}

func containsObjection(text string) bool {
	return containsAny(normalize(text), []string{
		"дорого", "подумаю", "подумаем", "позже", "expensive", "costly", "think", "i will think",
		"too much", "қымбат", "ойлан", "кейін",
	})
}

func containsPortfolioRequest(text string) bool {
	return containsAny(normalize(text), []string{
		"портфолио", "пример", "примеры", "кейс", "кейсы", "portfolio", "example", "examples",
		"case", "cases", "мысал",
	})
}

func containsReadySignal(text string) bool {
	return containsAny(normalize(text), []string{
		"готов", "готовы", "начнем", "начнём", "начать", "давайте", "берем", "берём", "оформ",
		"беру", "супер беру", "заказываю", "хочу заказать", "хочу начать", "ready", "start", "let's",
		"lets", "take it", "дайын", "бастайық",
	})
}

func containsAny(text string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(text, candidate) {
			return true
		}
	}
	return false
}

func normalize(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}
