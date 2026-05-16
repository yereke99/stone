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
