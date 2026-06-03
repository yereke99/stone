package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	defaultBaseURL   = "https://api.openai.com/v1"
	maxErrorBodySize = 4096
)

type Client struct {
	apiKey          string
	model           string
	maxOutputTokens int
	temperature     float64
	httpClient      *http.Client
	baseURL         string
}

type Message struct {
	Role    string
	Content string
}

type SalesResponse struct {
	Reply            string   `json:"reply"`
	Language         string   `json:"language"`
	Stage            string   `json:"stage"`
	RecommendedLevel int      `json:"recommended_level"`
	SendVideos       []string `json:"send_videos"`
	AskBrief         bool     `json:"ask_brief"`
	NeedHuman        bool     `json:"need_human"`
	LeadStatus       string   `json:"lead_status"`
	CompletedFields  []string `json:"completed_fields"`
	AskedFields      []string `json:"asked_fields"`
}

type CustomerUnderstanding struct {
	Language              string   `json:"language"`
	Intent                string   `json:"intent"`
	Niche                 *string  `json:"niche"`
	Goal                  *string  `json:"goal"`
	Deadline              *string  `json:"deadline"`
	Platform              *string  `json:"platform"`
	ProductOrService      *string  `json:"product_or_service"`
	WebsiteOrInstagram    *string  `json:"website_or_instagram"`
	PackageInterest       *string  `json:"package_interest"`
	AsksForFoodExamples   bool     `json:"asks_for_food_examples"`
	AsksForMoreOptions    bool     `json:"asks_for_more_options"`
	ReadyForQuestionnaire bool     `json:"ready_for_questionnaire"`
	NeedsManager          bool     `json:"needs_manager"`
	MissingFields         []string `json:"missing_fields"`
	Confidence            float64  `json:"confidence"`

	// Legacy fields are kept for old tests and deployments that still return the
	// previous nested analyzer shape. The live schema below now asks for the flat
	// lead-analyzer contract.
	Extracted   CustomerUnderstandingExtracted `json:"extracted,omitempty"`
	Sentiment   CustomerUnderstandingSentiment `json:"sentiment,omitempty"`
	StateUpdate CustomerUnderstandingState     `json:"state_update,omitempty"`
	ReplyPlan   CustomerUnderstandingReplyPlan `json:"reply_plan,omitempty"`
}

type CustomerUnderstandingExtracted struct {
	Niche           *string `json:"niche"`
	City            *string `json:"city"`
	Goal            *string `json:"goal"`
	Deadline        *string `json:"deadline"`
	Platform        *string `json:"platform"`
	TargetAudience  *string `json:"target_audience"`
	PackageInterest *string `json:"package_interest"`
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

func NewClient(apiKey string, model string, maxOutputTokens int, temperature float64, httpClient *http.Client) *Client {
	return &Client{
		apiKey:          strings.TrimSpace(apiKey),
		model:           strings.TrimSpace(model),
		maxOutputTokens: maxOutputTokens,
		temperature:     temperature,
		httpClient:      httpClient,
		baseURL:         defaultBaseURL,
	}
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

	payload := responseRequest{
		Model:           c.model,
		Input:           input,
		MaxOutputTokens: c.maxOutputTokens,
		Temperature:     c.temperature,
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(requestBody))
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
		return SalesResponse{}, c.statusError(resp.StatusCode, data)
	}

	outputText, err := extractOutputText(data)
	if err != nil {
		return SalesResponse{}, err
	}

	var result SalesResponse
	if err := json.Unmarshal([]byte(outputText), &result); err != nil {
		return SalesResponse{}, fmt.Errorf("parse model json response: %w", err)
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

	payload := responseRequest{
		Model:           c.model,
		Input:           input,
		MaxOutputTokens: minPositive(c.maxOutputTokens, 1200),
		Temperature:     analysisTemperature(c.temperature),
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(requestBody))
	if err != nil {
		return CustomerUnderstanding{}, fmt.Errorf("create customer understanding request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return CustomerUnderstanding{}, fmt.Errorf("call openai customer understanding api: %w", err)
	}
	defer closeBody(resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return CustomerUnderstanding{}, fmt.Errorf("read customer understanding response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return CustomerUnderstanding{}, c.statusError(resp.StatusCode, data)
	}

	outputText, err := extractOutputText(data)
	if err != nil {
		return CustomerUnderstanding{}, err
	}
	var result CustomerUnderstanding
	if err := json.Unmarshal([]byte(outputText), &result); err != nil {
		return CustomerUnderstanding{}, fmt.Errorf("parse customer understanding json response: %w", err)
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
		Temperature:     0,
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
		return HistoryGuardResponse{}, c.statusError(resp.StatusCode, data)
	}

	outputText, err := extractOutputText(data)
	if err != nil {
		return HistoryGuardResponse{}, err
	}
	var result HistoryGuardResponse
	if err := json.Unmarshal([]byte(outputText), &result); err != nil {
		return HistoryGuardResponse{}, fmt.Errorf("parse history guard model json response: %w", err)
	}
	return result, nil
}

type responseRequest struct {
	Model           string          `json:"model"`
	Input           []responseInput `json:"input"`
	Text            responseText    `json:"text"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Temperature     float64         `json:"temperature,omitempty"`
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
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"reply",
			"language",
			"stage",
			"recommended_level",
			"send_videos",
			"ask_brief",
			"need_human",
			"lead_status",
			"completed_fields",
			"asked_fields",
		},
		"properties": map[string]any{
			"reply": map[string]any{
				"type": "string",
			},
			"language": map[string]any{
				"type": "string",
				"enum": []string{"ru", "kk", "en"},
			},
			"stage": map[string]any{
				"type": "string",
				"enum": []string{
					"new_lead",
					"qualification",
					"platform_detected",
					"ai_experience_checked",
					"package_suggested",
					"package_selected",
					"portfolio_sent",
					"brief_requested",
					"brief_collected",
					"handoff_required",
					"muted",
					"greeting",
					"diagnosis",
					"offer",
					"portfolio",
					"objection",
					"closing",
					"offtopic",
				},
			},
			"recommended_level": map[string]any{
				"type":    "integer",
				"minimum": 0,
				"maximum": 3,
			},
			"send_videos": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
					"enum": []string{"video_level_1.mp4", "video_level_2.mp4", "video_level_3.mp4"},
				},
			},
			"ask_brief": map[string]any{
				"type": "boolean",
			},
			"need_human": map[string]any{
				"type": "boolean",
			},
			"lead_status": map[string]any{
				"type": "string",
				"enum": []string{"neutral", "new", "warm", "hot", "handoff_required", "closed", "muted"},
			},
			"completed_fields": fieldListSchema(),
			"asked_fields":     fieldListSchema(),
		},
	}
}

func customerUnderstandingSchema() map[string]any {
	nullableString := func() map[string]any {
		return map[string]any{"type": []string{"string", "null"}}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"language",
			"intent",
			"niche",
			"goal",
			"deadline",
			"platform",
			"product_or_service",
			"website_or_instagram",
			"package_interest",
			"asks_for_food_examples",
			"asks_for_more_options",
			"ready_for_questionnaire",
			"needs_manager",
			"missing_fields",
			"confidence",
		},
		"properties": map[string]any{
			"language": map[string]any{
				"type": "string",
				"enum": []string{"ru", "kk", "en", "mixed", "unknown"},
			},
			"intent": map[string]any{
				"type": "string",
				"enum": []string{
					"greeting",
					"qualification_answer",
					"asks_examples",
					"asks_packages",
					"asks_price",
					"asks_discount",
					"asks_deadline",
					"asks_human",
					"ready_to_order",
					"unclear",
					"irrelevant",
				},
			},
			"niche":                nullableString(),
			"goal":                 nullableString(),
			"deadline":             nullableString(),
			"platform":             nullableString(),
			"product_or_service":   nullableString(),
			"website_or_instagram": nullableString(),
			"package_interest": map[string]any{
				"type": []string{"string", "null"},
				"enum": []any{"test", "basic", "standard", "unknown", nil},
			},
			"asks_for_food_examples":  map[string]any{"type": "boolean"},
			"asks_for_more_options":   map[string]any{"type": "boolean"},
			"ready_for_questionnaire": map[string]any{"type": "boolean"},
			"needs_manager":           map[string]any{"type": "boolean"},
			"missing_fields": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
					"enum": []string{"niche", "goal", "deadline"},
				},
			},
			"confidence": map[string]any{
				"type":    "number",
				"minimum": 0,
				"maximum": 1,
			},
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

func analysisTemperature(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 0.2 {
		return 0.2
	}
	return value
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

func (c *Client) statusError(statusCode int, body []byte) error {
	bodyText := strings.TrimSpace(string(body))
	bodyText = strings.ReplaceAll(bodyText, c.apiKey, "[redacted]")
	if len(bodyText) > maxErrorBodySize {
		bodyText = bodyText[:maxErrorBodySize]
	}
	if bodyText == "" {
		return fmt.Errorf("openai responses api returned status %d", statusCode)
	}
	return fmt.Errorf("openai responses api returned status %d: %s", statusCode, bodyText)
}

func closeBody(body io.Closer) {
	_ = body.Close()
}
