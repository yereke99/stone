package bot

import "strings"

const (
	StopReasonManualAdminStop      = "manual_admin_stop"
	StopReasonManualOverride       = "manual_override"
	StopReasonModeratorStopCommand = "moderator_stop_command"

	StoppedByManualAdmin    = "manual_admin"
	StoppedByModeratorPhone = "moderator_phone"
)

func isConversationClosedForAutomation(conversation Conversation) bool {
	state := strings.TrimSpace(conversation.Stage)
	if conversation.OptOut ||
		conversation.Stopped ||
		conversation.AutomationClosed ||
		conversation.HandedOffToOwner ||
		!conversation.TransferredAt.IsZero() ||
		!conversation.AdminNotifiedAt.IsZero() ||
		!conversation.AdminOperatorNotifiedAt.IsZero() ||
		conversation.DoNotAutoStart ||
		conversation.LegacyProcessed ||
		normalizeLeadStatus(conversation.LeadStatus) == LeadStatusMuted ||
		normalizeLeadStatus(conversation.Lead.LeadStatus) == LeadStatusMuted {
		return true
	}
	switch state {
	case ClientStateHandedOff,
		ClientStateStopped,
		ClientStateOptOut,
		ClientStateLegacyExisting,
		ClientStateLegacyProcessed,
		ClientStateHistoryCheckFailed,
		StageMuted:
		return true
	default:
		return false
	}
}

func isConversationManuallyStopped(conversation Conversation) bool {
	switch strings.TrimSpace(conversation.StopReason) {
	case StopReasonManualAdminStop, StopReasonManualOverride, StopReasonModeratorStopCommand:
		return conversation.Stopped
	default:
		return false
	}
}

func canSendAutomationToConversation(conversation Conversation) bool {
	if isConversationClosedForAutomation(conversation) {
		return false
	}
	if shouldSilenceForStoredHistory(conversation) {
		return false
	}
	return true
}
