package bot

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yereke99/stone/internal/openai"
	"go.uber.org/zap"
)

type llmConversationReplyResult struct {
	Response openai.SalesResponse
	Called   bool
	Usable   bool
	Status   string
}

func (s *Service) maybeGenerateConversationReply(ctx context.Context, chatID string, msg IncomingMessage, text string, language string, conversation Conversation, analysis CustomerAnalysis) llmConversationReplyResult {
	if !s.llmReply.Enabled || s.ai == nil || strings.TrimSpace(text) == "" {
		return llmConversationReplyResult{}
	}
	if isOptOutText(text) || isClientDeferText(text) {
		return llmConversationReplyResult{}
	}

	payload := conversationReplyPayload(msg, text, language, conversation, analysis)
	timeout := s.llmReply.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	replyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	response, err := s.ai.GenerateSalesReply(replyCtx, SystemPrompt, []openai.Message{
		{Role: "user", Content: payload},
	})
	result := llmConversationReplyResult{Called: true, Response: response, Status: "ok"}
	if err != nil {
		result.Status = "error"
		if replyCtx.Err() != nil {
			result.Status = "timeout"
		}
		s.warn("openai conversation reply failed; using safe state-machine fallback",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("incoming_message_id", strings.TrimSpace(msg.IDMessage)),
			zap.String("current_stage", conversation.Stage),
			zap.String("openai_reply_status", result.Status),
			zap.Error(err),
		)
		return result
	}

	reply := salesResponseReplyText(response)
	result.Usable = reply != "" && response.NextAction != "no_reply"
	s.info("openai conversation reply generated",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("incoming_message_id", strings.TrimSpace(msg.IDMessage)),
		zap.String("current_stage", conversation.Stage),
		zap.Bool("openai_reply_called", true),
		zap.String("openai_reply_status", result.Status),
		zap.String("intent", strings.TrimSpace(response.Intent)),
		zap.String("next_action", strings.TrimSpace(response.NextAction)),
		zap.Bool("dry_run", s.llmReply.DryRun),
		zap.String("final_reply_preview", previewText(reply, 180)),
	)
	return result
}

func conversationReplyPayload(msg IncomingMessage, text string, language string, conversation Conversation, analysis CustomerAnalysis) string {
	understanding := json.RawMessage(customerUnderstandingPayload(msg, text, language, conversation))
	if !json.Valid(understanding) {
		understanding = json.RawMessage(`{}`)
	}
	payload := struct {
		Context          json.RawMessage  `json:"context"`
		CurrentAnalysis  CustomerAnalysis `json:"current_analysis"`
		KnownState       json.RawMessage  `json:"known_state"`
		MissingFields    []string         `json:"missing_fields_after_analysis"`
		ReplyConstraints []string         `json:"reply_constraints"`
	}{
		Context:         understanding,
		CurrentAnalysis: analysis,
		KnownState:      json.RawMessage(conversationPromptJSON(conversation)),
		MissingFields:   qualificationMissingFields(conversation.Lead),
		ReplyConstraints: []string{
			"answer_latest_direct_question_first",
			"ask_at_most_one_next_question",
			"do_not_repeat_known_fields",
			"use_only_official_prices",
			"do_not_invent_discounts",
			"do_not_promise_exact_celebrity_voice_or_actor_likeness_without_rights",
			"do_not_claim_media_was_sent",
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return customerUnderstandingPayload(msg, text, language, conversation)
	}
	return string(data)
}

func salesResponseReplyText(response openai.SalesResponse) string {
	if text := strings.TrimSpace(response.ReplyText); text != "" {
		return text
	}
	return strings.TrimSpace(response.Reply)
}

func (s *Service) applyLLMExtractedFields(ctx context.Context, chatID string, response openai.SalesResponse, language string, conversation Conversation) error {
	analysis := analysisFromOpenAIExtracted(response.ExtractedFields, language)
	if !analysis.HasBusinessSignal() {
		return nil
	}
	lead := conversation.Lead
	lead.ApplyAnalysis(analysis)
	return s.store.UpdateLead(ctx, chatID, lead)
}

func analysisFromOpenAIExtracted(fields openai.CustomerUnderstandingExtracted, language string) CustomerAnalysis {
	understanding := openai.CustomerUnderstanding{
		Language:        language,
		Intent:          "qualification_answer",
		ExtractedFields: fields,
		Confidence:      1,
	}
	analysis, ok := customerUnderstandingToAnalysis(understanding, LeadState{}, language)
	if !ok {
		return CustomerAnalysis{Intent: IntentOther}
	}
	return analysis
}

func previewText(value string, max int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if max <= 0 || len([]rune(value)) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max])
}
