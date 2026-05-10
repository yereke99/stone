package bot

import (
	"context"
	"time"
)

type Step string

const (
	StepWelcome             Step = "welcome"
	StepDiagnosticsGoal     Step = "diagnostics_goal"
	StepDiagnosticsPlatform Step = "diagnostics_platform"
	StepExpertise           Step = "expertise"
	StepOffer               Step = "offer"
	StepPortfolio           Step = "portfolio"
	StepClosing             Step = "closing"
)

type State struct {
	UserID       string
	Language     Language
	Step         Step
	UsedAIVideos *bool
	UpdatedAt    time.Time
}

type StateStore interface {
	Get(ctx context.Context, userID string) (State, bool, error)
	Save(ctx context.Context, state State) error
}
