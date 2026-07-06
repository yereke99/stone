package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const (
	defaultBaseURL                 = "https://api.openai.com/v1"
	responsesEndpoint              = "/responses"
	defaultAnalyzerMaxOutputTokens = 1500
	maxErrorBodySize               = 4096
)

type Client struct {
	apiKey                  string
	model                   string
	maxOutputTokens         int
	analyzerMaxOutputTokens int
	temperature             float64
	httpClient              *http.Client
	baseURL                 string
}

type APIError struct {
	Operation       string
	Model           string
	Endpoint        string
	StatusCode      int
	MaxOutputTokens int
	Err             error
}

type APIErrorDetails struct {
	Operation       string
	Model           string
	Endpoint        string
	StatusCode      int
	MaxOutputTokens int
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{"openai " + strings.TrimSpace(e.Operation) + " failed"}
	if e.Model != "" {
		parts = append(parts, "model="+e.Model)
	}
	if e.Endpoint != "" {
		parts = append(parts, "endpoint="+e.Endpoint)
	}
	if e.StatusCode > 0 {
		parts = append(parts, "status="+strconv.Itoa(e.StatusCode))
	}
	if e.MaxOutputTokens > 0 {
		parts = append(parts, "max_output_tokens="+strconv.Itoa(e.MaxOutputTokens))
	}
	if e.Err != nil {
		parts = append(parts, "error="+e.Err.Error())
	}
	return strings.Join(parts, " ")
}

func (e *APIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func ErrorDetails(err error) (APIErrorDetails, bool) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr == nil {
		return APIErrorDetails{}, false
	}
	return APIErrorDetails{
		Operation:       apiErr.Operation,
		Model:           apiErr.Model,
		Endpoint:        apiErr.Endpoint,
		StatusCode:      apiErr.StatusCode,
		MaxOutputTokens: apiErr.MaxOutputTokens,
	}, true
}

func SafeErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return ""
	}
	if len(message) > maxErrorBodySize {
		message = message[:maxErrorBodySize]
	}
	return message
}

type Message struct {
	Role    string
	Content string
}

type SalesResponse struct {
	Intent            string                         `json:"intent"`
	MessageMeaning    string                         `json:"message_meaning"`
	ShouldUpdateState bool                           `json:"should_update_state"`
	ExtractedFields   CustomerUnderstandingExtracted `json:"extracted_fields"`
	DoNotOverwrite    []string                       `json:"do_not_overwrite_fields"`
	AnsweredQuestions []CustomerAnsweredQuestion     `json:"answered_questions"`
	MissingFields     []string                       `json:"missing_fields"`
	RecommendedAction string                         `json:"recommended_action"`
	ReplyText         string                         `json:"reply_text"`
	NextAction        string                         `json:"next_action"`
	PortfolioTags     []string                       `json:"portfolio_tags"`
	NeedsHuman        bool                           `json:"needs_human"`
	Confidence        float64                        `json:"confidence"`
	Reply             string                         `json:"reply"`
	Language          string                         `json:"language"`
	Stage             string                         `json:"stage"`
	RecommendedLevel  int                            `json:"recommended_level"`
	SendVideos        []string                       `json:"send_videos"`
	AskBrief          bool                           `json:"ask_brief"`
	NeedHuman         bool                           `json:"need_human"`
	LeadStatus        string                         `json:"lead_status"`
	CompletedFields   []string                       `json:"completed_fields"`
	AskedFields       []string                       `json:"asked_fields"`
}

type ReplyTextResponse struct {
	ReplyText string `json:"reply_text"`
}

type CustomerUnderstanding struct {
	Language              string                     `json:"language"`
	Intent                string                     `json:"intent"`
	MessageMeaning        string                     `json:"message_meaning"`
	ShouldUpdateState     bool                       `json:"should_update_state"`
	Niche                 *string                    `json:"niche"`
	Goal                  *string                    `json:"goal"`
	Deadline              *string                    `json:"deadline"`
	Platform              *string                    `json:"platform"`
	ProductOrService      *string                    `json:"product_or_service"`
	StrongSide            *string                    `json:"strong_side"`
	TargetAudience        *string                    `json:"target_audience"`
	Offer                 *string                    `json:"offer"`
	WebsiteOrInstagram    *string                    `json:"website_or_instagram"`
	ReferenceLinks        []string                   `json:"reference_links"`
	Budget                *string                    `json:"budget"`
	Quantity              *string                    `json:"quantity"`
	VideoQuantity         *string                    `json:"video_quantity"`
	PackageInterest       *string                    `json:"package_interest"`
	SelectedPackage       *string                    `json:"selected_package"`
	LikedFormats          []string                   `json:"liked_formats"`
	VoicePreference       *string                    `json:"voice_preference"`
	CopyrightConcern      *string                    `json:"copyright_concern"`
	CampaignContext       *string                    `json:"campaign_context"`
	HookIdea              *string                    `json:"hook_idea"`
	City                  *string                    `json:"city"`
	AsksForFoodExamples   bool                       `json:"asks_for_food_examples"`
	AsksForMoreOptions    bool                       `json:"asks_for_more_options"`
	ReadyForQuestionnaire bool                       `json:"ready_for_questionnaire"`
	NeedsManager          bool                       `json:"needs_manager"`
	NeedsHuman            bool                       `json:"needs_human"`
	MissingFields         []string                   `json:"missing_fields"`
	DoNotOverwrite        []string                   `json:"do_not_overwrite_fields"`
	RecommendedAction     string                     `json:"recommended_action"`
	PortfolioTags         []string                   `json:"portfolio_tags"`
	AnsweredQuestions     []CustomerAnsweredQuestion `json:"answered_questions"`
	ReplyText             string                     `json:"reply_text"`
	NextAction            string                     `json:"next_action"`
	Confidence            float64                    `json:"confidence"`

	// Legacy fields are kept for old tests and deployments that still return the
	// previous nested analyzer shape. The live schema below now asks for the flat
	// lead-analyzer contract.
	ExtractedFields CustomerUnderstandingExtracted `json:"extracted_fields,omitempty"`
	Extracted       CustomerUnderstandingExtracted `json:"extracted,omitempty"`
	Sentiment       CustomerUnderstandingSentiment `json:"sentiment,omitempty"`
	StateUpdate     CustomerUnderstandingState     `json:"state_update,omitempty"`
	ReplyPlan       CustomerUnderstandingReplyPlan `json:"reply_plan,omitempty"`
}

type CustomerUnderstandingExtracted struct {
	Niche              *string  `json:"niche"`
	ProductOrService   *string  `json:"product_or_service"`
	TargetAudience     *string  `json:"target_audience"`
	Goal               *string  `json:"goal"`
	Deadline           *string  `json:"deadline"`
	Quantity           *string  `json:"quantity"`
	VideoQuantity      *string  `json:"video_quantity"`
	Budget             *string  `json:"budget"`
	ReferenceLinks     []string `json:"reference_links"`
	LikedFormats       []string `json:"liked_formats"`
	SelectedPackage    *string  `json:"selected_package"`
	PackageInterest    *string  `json:"package_interest"`
	VoicePreference    *string  `json:"voice_preference"`
	CopyrightConcern   *string  `json:"copyright_concern"`
	CampaignContext    *string  `json:"campaign_context"`
	HookIdea           *string  `json:"hook_idea"`
	City               *string  `json:"city"`
	WebsiteOrInstagram *string  `json:"website_or_instagram"`
	BusinessLink       *string  `json:"business_link"`
	Platform           *string  `json:"platform"`
	StrongSide         *string  `json:"strong_side"`
	Offer              *string  `json:"offer"`
}

type CustomerAnsweredQuestion struct {
	BotQuestion    string  `json:"bot_question"`
	CustomerAnswer string  `json:"customer_answer"`
	Field          string  `json:"field"`
	Confidence     float64 `json:"confidence"`
}

type CustomerUnderstandingSentiment struct {
	Negative    bool `json:"negative"`
	Frustrated  bool `json:"frustrated"`
	WantsToStop bool `json:"wants_to_stop"`
}

type CustomerUnderstandingState struct {
	ShouldSave             bool `json:"should_save"`
	ShouldHandoffToManager bool `json:"should_handoff_to_manager"`
	ShouldStopAutomation   bool `json:"should_stop_automation"`
}

type CustomerUnderstandingReplyPlan struct {
	AcknowledgeKnownFields bool    `json:"acknowledge_known_fields"`
	AskOnlyMissingFields   bool    `json:"ask_only_missing_fields"`
	NextMissingField       *string `json:"next_missing_field"`
	SafeReply              string  `json:"safe_reply"`
}

type HistoryGuardResponse struct {
	ChatType                    string                  `json:"chat_type"`
	ShouldAutoStartFunnel       bool                    `json:"should_auto_start_funnel"`
	ShouldSendSoftClarification bool                    `json:"should_send_soft_clarification"`
	Confidence                  float64                 `json:"confidence"`
	Reason                      string                  `json:"reason"`
	Summary                     string                  `json:"summary"`
	KnownFields                 HistoryGuardKnownFields `json:"known_fields"`
}

type HistoryGuardKnownFields struct {
	Niche           string `json:"niche"`
	Goal            string `json:"goal"`
	Deadline        string `json:"deadline"`
	PackageInterest string `json:"package_interest"`
}

func NewClient(apiKey string, model string, maxOutputTokens int, analyzerMaxOutputTokens int, temperature float64, httpClient *http.Client) *Client {
	return &Client{
		apiKey:                  strings.TrimSpace(apiKey),
		model:                   strings.TrimSpace(model),
		maxOutputTokens:         maxOutputTokens,
		analyzerMaxOutputTokens: analyzerMaxOutputTokens,
		temperature:             temperature,
		httpClient:              httpClient,
		baseURL:                 defaultBaseURL,
	}
}

func (c *Client) AnalyzerMaxOutputTokens() int {
	if c == nil || c.analyzerMaxOutputTokens <= 0 {
		return defaultAnalyzerMaxOutputTokens
	}
	return c.analyzerMaxOutputTokens
}

func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.model)
}

func (c *Client) ResponsesEndpoint() string {
	if c == nil {
		return ""
	}
	return c.endpointURL()
}

func (c *Client) GenerateSalesReply(ctx context.Context, systemPrompt string, messages []Message) (SalesResponse, error) {
	input := make([]responseInput, 0, len(messages)+1)
	input = append(input, newInput("system", systemPrompt))
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		content := strings.TrimSpace(message.Content)
		if role == "" || content == "" {
			continue
		}
		input = append(input, newInput(role, content))
	}

	model := strings.TrimSpace(os.Getenv("BOT_LLM_REPLY_MODEL"))
	if model == "" {
		model = c.model
	}
	maxOutputTokens := c.maxOutputTokens
	if raw := firstEnv("LLM_REPLY_MAX_OUTPUT_TOKENS", "BOT_LLM_REPLY_MAX_TOKENS"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			maxOutputTokens = value
		}
	}
	payload := responseRequest{
		Model:           model,
		Input:           input,
		MaxOutputTokens: maxOutputTokens,
		Store:           false,
		Text: responseText{
			Format: responseFormat{
				Type:   "json_schema",
				Name:   "stone_sales_response",
				Strict: true,
				Schema: salesResponseSchema(),
			},
		},
	}

	requestBody, err := json.Marshal(payload)
	if err != nil {
		return SalesResponse{}, fmt.Errorf("marshal openai responses request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL(), bytes.NewReader(requestBody))
	if err != nil {
		return SalesResponse{}, fmt.Errorf("create openai responses request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return SalesResponse{}, fmt.Errorf("call openai responses api: %w", err)
	}
	defer closeBody(resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return SalesResponse{}, fmt.Errorf("read openai responses response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return SalesResponse{}, c.statusError("sales_reply", model, maxOutputTokens, resp.StatusCode, data)
	}

	outputText, err := extractOutputText(data)
	if err != nil {
		return SalesResponse{}, c.apiError("sales_reply", model, maxOutputTokens, resp.StatusCode, err)
	}

	var result SalesResponse
	if err := json.Unmarshal([]byte(outputText), &result); err != nil {
		return SalesResponse{}, c.apiError("sales_reply", model, maxOutputTokens, resp.StatusCode, fmt.Errorf("parse model json response: %w", err))
	}

	return result, nil
}

func (c *Client) GenerateReplyText(ctx context.Context, systemPrompt string, messages []Message) (ReplyTextResponse, error) {
	input := make([]responseInput, 0, len(messages)+1)
	input = append(input, newInput("system", systemPrompt))
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		content := strings.TrimSpace(message.Content)
		if role == "" || content == "" {
			continue
		}
		input = append(input, newInput(role, content))
	}

	model := strings.TrimSpace(os.Getenv("BOT_LLM_REPLY_MODEL"))
	if model == "" {
		model = c.model
	}
	maxOutputTokens := c.maxOutputTokens
	if raw := firstEnv("LLM_REPLY_MAX_OUTPUT_TOKENS", "BOT_LLM_REPLY_MAX_TOKENS"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			maxOutputTokens = value
		}
	}
	payload := responseRequest{
		Model:           model,
		Input:           input,
		MaxOutputTokens: maxOutputTokens,
		Store:           false,
		Text: responseText{
			Format: responseFormat{
				Type:   "json_schema",
				Name:   "stone_reply_text",
				Strict: true,
				Schema: replyTextResponseSchema(),
			},
		},
	}

	requestBody, err := json.Marshal(payload)
	if err != nil {
		return ReplyTextResponse{}, fmt.Errorf("marshal openai reply text request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL(), bytes.NewReader(requestBody))
	if err != nil {
		return ReplyTextResponse{}, fmt.Errorf("create openai reply text request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ReplyTextResponse{}, c.apiError("reply_text", model, maxOutputTokens, 0, fmt.Errorf("call openai reply text api: %w", err))
	}
	defer closeBody(resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ReplyTextResponse{}, c.apiError("reply_text", model, maxOutputTokens, resp.StatusCode, fmt.Errorf("read openai reply text response: %w", err))
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return ReplyTextResponse{}, c.statusError("reply_text", model, maxOutputTokens, resp.StatusCode, data)
	}

	outputText, err := extractOutputText(data)
	if err != nil {
		return ReplyTextResponse{}, c.apiError("reply_text", model, maxOutputTokens, resp.StatusCode, err)
	}

	var result ReplyTextResponse
	if err := json.Unmarshal([]byte(outputText), &result); err != nil {
		return ReplyTextResponse{}, c.apiError("reply_text", model, maxOutputTokens, resp.StatusCode, fmt.Errorf("parse reply text json response: %w", err))
	}

	return result, nil
}

func (c *Client) AnalyzeCustomerMessage(ctx context.Context, systemPrompt string, messages []Message) (CustomerUnderstanding, error) {
	input := make([]responseInput, 0, len(messages)+1)
	input = append(input, newInput("system", systemPrompt))
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		content := strings.TrimSpace(message.Content)
		if role == "" || content == "" {
			continue
		}
		input = append(input, newInput(role, content))
	}

	maxOutputTokens := c.AnalyzerMaxOutputTokens()
	payload := responseRequest{
		Model:           c.model,
		Input:           input,
		MaxOutputTokens: maxOutputTokens,
		Store:           false,
		Text: responseText{
			Format: responseFormat{
				Type:   "json_schema",
				Name:   "stone_customer_understanding",
				Strict: true,
				Schema: customerUnderstandingSchema(),
			},
		},
	}

	requestBody, err := json.Marshal(payload)
	if err != nil {
		return CustomerUnderstanding{}, fmt.Errorf("marshal customer understanding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL(), bytes.NewReader(requestBody))
	if err != nil {
		return CustomerUnderstanding{}, fmt.Errorf("create customer understanding request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return CustomerUnderstanding{}, c.apiError("customer_understanding", c.model, maxOutputTokens, 0, fmt.Errorf("call openai customer understanding api: %w", err))
	}
	defer closeBody(resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return CustomerUnderstanding{}, c.apiError("customer_understanding", c.model, maxOutputTokens, resp.StatusCode, fmt.Errorf("read customer understanding response: %w", err))
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return CustomerUnderstanding{}, c.statusError("customer_understanding", c.model, maxOutputTokens, resp.StatusCode, data)
	}

	outputText, err := extractOutputText(data)
	if err != nil {
		return CustomerUnderstanding{}, c.apiError("customer_understanding", c.model, maxOutputTokens, resp.StatusCode, err)
	}
	var result CustomerUnderstanding
	if err := json.Unmarshal([]byte(outputText), &result); err != nil {
		return CustomerUnderstanding{}, c.apiError("customer_understanding", c.model, maxOutputTokens, resp.StatusCode, fmt.Errorf("parse customer understanding json response: %w", err))
	}
	return result, nil
}

func (c *Client) ClassifyHistoryGuard(ctx context.Context, payload string) (HistoryGuardResponse, error) {
	systemPrompt := strings.TrimSpace(`You classify whether a WhatsApp chat is safe to start as a new cold lead for Stone Production.
Use only the compact sanitized JSON provided by the backend.
Return strict JSON. If prior history exists, do not classify as a completely new client.
Prefer safety when uncertain.`)
	input := []responseInput{
		newInput("system", systemPrompt),
		newInput("user", strings.TrimSpace(payload)),
	}
	request := responseRequest{
		Model:           c.model,
		Input:           input,
		MaxOutputTokens: minPositive(c.maxOutputTokens, 400),
		Store:           false,
		Text: responseText{
			Format: responseFormat{
				Type:   "json_schema",
				Name:   "stone_history_guard_response",
				Strict: true,
				Schema: historyGuardResponseSchema(),
			},
		},
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		return HistoryGuardResponse{}, fmt.Errorf("marshal history guard request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(requestBody))
	if err != nil {
		return HistoryGuardResponse{}, fmt.Errorf("create history guard request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return HistoryGuardResponse{}, fmt.Errorf("call openai history guard api: %w", err)
	}
	defer closeBody(resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return HistoryGuardResponse{}, fmt.Errorf("read history guard response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return HistoryGuardResponse{}, c.statusError("history_guard", c.model, request.MaxOutputTokens, resp.StatusCode, data)
	}

	outputText, err := extractOutputText(data)
	if err != nil {
		return HistoryGuardResponse{}, c.apiError("history_guard", c.model, request.MaxOutputTokens, resp.StatusCode, err)
	}
	var result HistoryGuardResponse
	if err := json.Unmarshal([]byte(outputText), &result); err != nil {
		return HistoryGuardResponse{}, c.apiError("history_guard", c.model, request.MaxOutputTokens, resp.StatusCode, fmt.Errorf("parse history guard model json response: %w", err))
	}
	return result, nil
}

type responseRequest struct {
	Model           string          `json:"model"`
	Input           []responseInput `json:"input"`
	Text            responseText    `json:"text"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	Store           bool            `json:"store"`
}

type responseInput struct {
	Role    string            `json:"role"`
	Content []responseContent `json:"content"`
}

type responseContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responseText struct {
	Format responseFormat `json:"format"`
}

type responseFormat struct {
	Type   string         `json:"type"`
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type responsePayload struct {
	OutputText string           `json:"output_text"`
	Output     []responseOutput `json:"output"`
}

type responseOutput struct {
	Type    string                  `json:"type"`
	Content []responseOutputContent `json:"content"`
}

type responseOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func newInput(role string, text string) responseInput {
	return responseInput{
		Role: role,
		Content: []responseContent{
			{Type: "input_text", Text: text},
		},
	}
}

func extractOutputText(data []byte) (string, error) {
	var payload responsePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("decode openai responses response: %w", err)
	}

	if text := strings.TrimSpace(payload.OutputText); text != "" {
		return text, nil
	}

	for _, output := range payload.Output {
		for _, content := range output.Content {
			if content.Type == "output_text" || content.Type == "text" {
				if text := strings.TrimSpace(content.Text); text != "" {
					return text, nil
				}
			}
		}
	}

	return "", fmt.Errorf("openai responses response does not contain output text")
}

func salesResponseSchema() map[string]any {
	return conversationDecisionSchema("stone_sales_response")
}

func replyTextResponseSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"reply_text"},
		"properties": map[string]any{
			"reply_text": map[string]any{"type": "string"},
		},
	}
}

func conversationDecisionSchema(name string) map[string]any {
	_ = name
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"intent",
			"message_meaning",
			"should_update_state",
			"extracted_fields",
			"do_not_overwrite_fields",
			"answered_questions",
			"missing_fields",
			"recommended_action",
			"reply_text",
			"next_action",
			"portfolio_tags",
			"needs_human",
			"confidence",
		},
		"properties": map[string]any{
			"intent":                  conversationIntentSchema(),
			"message_meaning":         map[string]any{"type": "string"},
			"should_update_state":     map[string]any{"type": "boolean"},
			"extracted_fields":        extractedFieldsSchema(),
			"do_not_overwrite_fields": businessFieldListSchema(),
			"answered_questions":      answeredQuestionsSchema(),
			"missing_fields":          businessFieldListSchema(),
			"recommended_action":      recommendedActionSchema(),
			"reply_text":              map[string]any{"type": "string"},
			"next_action":             nextActionSchema(),
			"portfolio_tags":          stringArraySchema(),
			"needs_human":             map[string]any{"type": "boolean"},
			"confidence": map[string]any{
				"type":    "number",
				"minimum": 0,
				"maximum": 1,
			},
		},
	}
}

func customerUnderstandingSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"language",
			"intent",
			"message_meaning",
			"should_update_state",
			"extracted_fields",
			"do_not_overwrite_fields",
			"answered_questions",
			"missing_fields",
			"recommended_action",
			"reply_text",
			"next_action",
			"portfolio_tags",
			"needs_human",
			"confidence",
		},
		"properties": map[string]any{
			"language": map[string]any{
				"type": "string",
				"enum": []string{"ru", "kk", "en", "mixed", "unknown"},
			},
			"intent":                  conversationIntentSchema(),
			"message_meaning":         map[string]any{"type": "string"},
			"should_update_state":     map[string]any{"type": "boolean"},
			"extracted_fields":        extractedFieldsSchema(),
			"do_not_overwrite_fields": businessFieldListSchema(),
			"answered_questions":      answeredQuestionsSchema(),
			"missing_fields":          businessFieldListSchema(),
			"recommended_action":      recommendedActionSchema(),
			"reply_text":              map[string]any{"type": "string"},
			"next_action":             nextActionSchema(),
			"portfolio_tags":          stringArraySchema(),
			"needs_human":             map[string]any{"type": "boolean"},
			"confidence": map[string]any{
				"type":    "number",
				"minimum": 0,
				"maximum": 1,
			},
		},
	}
}

func nullableStringSchema() map[string]any {
	return map[string]any{"type": []string{"string", "null"}}
}

func nullablePackageSchema() map[string]any {
	return map[string]any{
		"type": []string{"string", "null"},
		"enum": []any{"test", "basic", "standard", "needs_manager_recommendation", "unknown", nil},
	}
}

func stringArraySchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "string",
		},
	}
}

func conversationIntentSchema() map[string]any {
	return map[string]any{
		"type": "string",
		"enum": []string{
			"qualification_answer",
			"business_link",
			"reference_link",
			"price_question",
			"discount_question",
			"quantity_answer",
			"case_request",
			"niche_specific_case_request",
			"feasibility_question",
			"format_preference",
			"negative_selection",
			"confusion",
			"objection",
			"voice_question",
			"copyright_question",
			"package_selection",
			"human_request",
			"stop_or_opt_out",
			"greeting",
			"defer",
			"other",
		},
	}
}

func nextActionSchema() map[string]any {
	return map[string]any{
		"type": "string",
		"enum": []string{"send_text", "send_cases", "send_video", "send_relevant_examples", "ask_next_question", "handoff", "no_reply"},
	}
}

func recommendedActionSchema() map[string]any {
	return map[string]any{
		"type": "string",
		"enum": []string{
			"send_text",
			"send_relevant_examples",
			"ask_goal",
			"ask_next_question",
			"send_price_options",
			"send_questionnaire",
			"answer_question",
			"handoff",
			"stop_bot",
			"no_reply",
		},
	}
}

func businessFieldListSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "string",
			"enum": []string{
				"niche",
				"product_or_service",
				"target_audience",
				"goal",
				"deadline",
				"quantity",
				"video_quantity",
				"budget",
				"reference_links",
				"liked_formats",
				"selected_package",
				"package_interest",
				"voice_preference",
				"copyright_concern",
				"campaign_context",
				"hook_idea",
				"city",
				"website_or_instagram",
			},
		},
	}
}

func answeredQuestionsSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"bot_question", "customer_answer", "field", "confidence"},
			"properties": map[string]any{
				"bot_question":    map[string]any{"type": "string"},
				"customer_answer": map[string]any{"type": "string"},
				"field":           map[string]any{"type": "string"},
				"confidence": map[string]any{
					"type":    "number",
					"minimum": 0,
					"maximum": 1,
				},
			},
		},
	}
}

func extractedFieldsSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"niche",
			"product_or_service",
			"target_audience",
			"goal",
			"deadline",
			"quantity",
			"video_quantity",
			"budget",
			"reference_links",
			"liked_formats",
			"selected_package",
			"package_interest",
			"voice_preference",
			"copyright_concern",
			"campaign_context",
			"hook_idea",
			"city",
			"website_or_instagram",
			"business_link",
			"platform",
			"strong_side",
			"offer",
		},
		"properties": map[string]any{
			"niche":                nullableStringSchema(),
			"product_or_service":   nullableStringSchema(),
			"target_audience":      nullableStringSchema(),
			"goal":                 nullableStringSchema(),
			"deadline":             nullableStringSchema(),
			"quantity":             nullableStringSchema(),
			"video_quantity":       nullableStringSchema(),
			"budget":               nullableStringSchema(),
			"reference_links":      stringArraySchema(),
			"liked_formats":        stringArraySchema(),
			"selected_package":     nullablePackageSchema(),
			"package_interest":     nullablePackageSchema(),
			"voice_preference":     nullableStringSchema(),
			"copyright_concern":    nullableStringSchema(),
			"campaign_context":     nullableStringSchema(),
			"hook_idea":            nullableStringSchema(),
			"city":                 nullableStringSchema(),
			"website_or_instagram": nullableStringSchema(),
			"business_link":        nullableStringSchema(),
			"platform":             nullableStringSchema(),
			"strong_side":          nullableStringSchema(),
			"offer":                nullableStringSchema(),
		},
	}
}

func historyGuardResponseSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"chat_type",
			"should_auto_start_funnel",
			"should_send_soft_clarification",
			"confidence",
			"reason",
			"summary",
			"known_fields",
		},
		"properties": map[string]any{
			"chat_type": map[string]any{
				"type": "string",
				"enum": []string{"new_client", "legacy_existing", "legacy_processed", "legacy_reengagement", "unknown"},
			},
			"should_auto_start_funnel": map[string]any{
				"type": "boolean",
			},
			"should_send_soft_clarification": map[string]any{
				"type": "boolean",
			},
			"confidence": map[string]any{
				"type":    "number",
				"minimum": 0,
				"maximum": 1,
			},
			"reason": map[string]any{
				"type": "string",
			},
			"summary": map[string]any{
				"type": "string",
			},
			"known_fields": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"niche", "goal", "deadline", "package_interest"},
				"properties": map[string]any{
					"niche": map[string]any{
						"type": "string",
					},
					"goal": map[string]any{
						"type": "string",
					},
					"deadline": map[string]any{
						"type": "string",
					},
					"package_interest": map[string]any{
						"type": "string",
					},
				},
			},
		},
	}
}

func minPositive(a int, b int) int {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func fieldListSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "string",
			"enum": []string{"niche", "goal", "platform", "deadline", "ai_experience", "package", "brief"},
		},
	}
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func (c *Client) endpointURL() string {
	baseURL := defaultBaseURL
	if c != nil && strings.TrimSpace(c.baseURL) != "" {
		baseURL = strings.TrimRight(strings.TrimSpace(c.baseURL), "/")
	}
	return baseURL + responsesEndpoint
}

func (c *Client) apiError(operation string, model string, maxOutputTokens int, statusCode int, err error) error {
	return &APIError{
		Operation:       operation,
		Model:           strings.TrimSpace(model),
		Endpoint:        c.endpointURL(),
		StatusCode:      statusCode,
		MaxOutputTokens: maxOutputTokens,
		Err:             err,
	}
}

func (c *Client) statusError(operation string, model string, maxOutputTokens int, statusCode int, body []byte) error {
	bodyText := strings.TrimSpace(string(body))
	bodyText = strings.ReplaceAll(bodyText, c.apiKey, "[redacted]")
	if len(bodyText) > maxErrorBodySize {
		bodyText = bodyText[:maxErrorBodySize]
	}
	if bodyText == "" {
		return c.apiError(operation, model, maxOutputTokens, statusCode, fmt.Errorf("openai responses api returned status %d", statusCode))
	}
	return c.apiError(operation, model, maxOutputTokens, statusCode, fmt.Errorf("openai responses api returned status %d: %s", statusCode, bodyText))
}

func closeBody(body io.Closer) {
	_ = body.Close()
}
