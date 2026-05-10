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
	if !exists || state.Step == StepWelcome {
		state.Step = StepDiagnosticsGoal
		return singleReply(welcomeMessage(state.Language))
	}

	if containsObjection(text) {
		return singleReply(objectionMessage(state.Language))
	}
	if containsPortfolioRequest(text) {
		return repliesFromTexts(portfolioMessages(state.Language, m.portfolio))
	}
	if containsReadySignal(text) {
		state.Step = StepClosing
		return singleReply(questionnaireMessage(state.Language))
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
		return singleReply(offerMessage(state.Language, *usedBefore))
	case StepPortfolio:
		state.Step = StepClosing
		return singleReply(portfolioPromptMessage(state.Language))
	default:
		state.Step = StepClosing
		return singleReply(questionnaireMessage(state.Language))
	}
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
		"готов", "готовы", "начнем", "начнём", "начать", "хочу", "давайте", "ready", "start",
		"let's", "lets", "дайын", "бастайық",
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
