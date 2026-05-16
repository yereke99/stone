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

const (
	StageNewLead             = "new_lead"
	StageQualification       = "qualification"
	StagePlatformDetected    = "platform_detected"
	StageAIExperienceChecked = "ai_experience_checked"
	StagePackageSuggested    = "package_suggested"
	StagePackageSelected     = "package_selected"
	StagePortfolioSent       = "portfolio_sent"
	StageBriefRequested      = "brief_requested"
	StageBriefCollected      = "brief_collected"
	StageHandoffRequired     = "handoff_required"
	StageMuted               = "muted"

	StageLanguage  = "language"
	StageDiagnosis = "diagnosis"
	StageOffer     = "offer"
	StagePortfolio = "portfolio"
	StageObjection = "objection"
	StageClosing   = "closing"
	StageOfftopic  = "offtopic"
)

const (
	LeadStatusNeutral         = "neutral"
	LeadStatusNew             = "new"
	LeadStatusWarm            = "warm"
	LeadStatusHot             = "hot"
	LeadStatusClosed          = "closed"
	LeadStatusHandoffRequired = "handoff_required"
	LeadStatusMuted           = "muted"
)

type State struct {
	UserID       string
	Language     Language
	Step         Step
	UsedAIVideos *bool
	Lead         LeadState
	UpdatedAt    time.Time
}

type StateStore interface {
	Get(ctx context.Context, userID string) (State, bool, error)
	Save(ctx context.Context, state State) error
}
