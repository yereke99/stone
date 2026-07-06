package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnalyzeCustomerMessageUsesDedicatedTokenBudgetAndOmitsTemperature(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != responsesEndpoint {
			t.Fatalf("path = %q, want %q", r.URL.Path, responsesEndpoint)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		output := `{"language":"ru","intent":"qualification_answer","message_meaning":"client gave niche","should_update_state":true,"extracted_fields":{"niche":"туризм","product_or_service":null,"target_audience":null,"goal":null,"deadline":null,"quantity":null,"video_quantity":null,"budget":null,"reference_links":[],"liked_formats":[],"selected_package":null,"package_interest":null,"voice_preference":null,"copyright_concern":null,"campaign_context":null,"hook_idea":null,"city":null,"website_or_instagram":null,"business_link":null,"platform":null,"strong_side":null,"offer":null},"do_not_overwrite_fields":[],"answered_questions":[],"missing_fields":["goal"],"recommended_action":"ask_goal","reply_text":"Понял вас. Какая цель ролика?","next_action":"ask_next_question","portfolio_tags":["tourism"],"needs_human":false,"confidence":1}`
		_ = json.NewEncoder(w).Encode(map[string]string{"output_text": output})
	}))
	defer server.Close()

	client := NewClient("test-key", "gpt-5.5", 350, 1500, 0.3, server.Client())
	client.baseURL = server.URL

	result, err := client.AnalyzeCustomerMessage(context.Background(), "system", []Message{{Role: "user", Content: "туризм"}})
	if err != nil {
		t.Fatalf("AnalyzeCustomerMessage() error = %v", err)
	}
	if result.ExtractedFields.Niche == nil || *result.ExtractedFields.Niche != "туризм" {
		t.Fatalf("niche = %#v", result.ExtractedFields.Niche)
	}
	if got := int(request["max_output_tokens"].(float64)); got != 1500 {
		t.Fatalf("max_output_tokens = %d, want 1500", got)
	}
	if _, ok := request["temperature"]; ok {
		t.Fatalf("analyzer request included temperature: %#v", request["temperature"])
	}
}

func TestAnalyzeCustomerMessageHTTPErrorCarriesSanitizedMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"unsupported parameter: temperature"}}`, http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient("secret-key", "gpt-5.5", 350, 1500, 0.3, server.Client())
	client.baseURL = server.URL

	_, err := client.AnalyzeCustomerMessage(context.Background(), "system", []Message{{Role: "user", Content: "hello"}})
	if err == nil {
		t.Fatal("AnalyzeCustomerMessage() error = nil")
	}
	details, ok := ErrorDetails(err)
	if !ok {
		t.Fatalf("ErrorDetails() not available for %T: %v", err, err)
	}
	if details.Model != "gpt-5.5" || details.StatusCode != http.StatusBadRequest || details.MaxOutputTokens != 1500 {
		t.Fatalf("details = %#v", details)
	}
	if !strings.Contains(details.Endpoint, responsesEndpoint) {
		t.Fatalf("endpoint = %q, want responses endpoint", details.Endpoint)
	}
	if strings.Contains(SafeErrorMessage(err), "secret-key") {
		t.Fatalf("safe error leaked API key: %s", SafeErrorMessage(err))
	}
}

func TestGenerateReplyTextUsesReplyTextOnlySchema(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"output_text": `{"reply_text":"Понял вас. Какая цель ролика?"}`})
	}))
	defer server.Close()

	client := NewClient("test-key", "gpt-5.5", 350, 1500, 0.3, server.Client())
	client.baseURL = server.URL

	result, err := client.GenerateReplyText(context.Background(), "system", []Message{{Role: "user", Content: "payload"}})
	if err != nil {
		t.Fatalf("GenerateReplyText() error = %v", err)
	}
	if result.ReplyText != "Понял вас. Какая цель ролика?" {
		t.Fatalf("reply_text = %q", result.ReplyText)
	}
	text := request["text"].(map[string]any)
	format := text["format"].(map[string]any)
	if format["name"] != "stone_reply_text" {
		t.Fatalf("schema name = %v, want stone_reply_text", format["name"])
	}
	schema := format["schema"].(map[string]any)
	required := schema["required"].([]any)
	if len(required) != 1 || required[0] != "reply_text" {
		t.Fatalf("required = %#v, want only reply_text", required)
	}
	properties := schema["properties"].(map[string]any)
	if len(properties) != 1 {
		t.Fatalf("properties = %#v, want only reply_text", properties)
	}
}

func TestAnalyzeCustomerMessageTruncatedJSONReturnsParseErrorDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"output_text": `{"language":"ru","intent":"qualification_answer"`})
	}))
	defer server.Close()

	client := NewClient("test-key", "gpt-5.5", 350, 1500, 0.3, server.Client())
	client.baseURL = server.URL

	_, err := client.AnalyzeCustomerMessage(context.Background(), "system", []Message{{Role: "user", Content: "туризм"}})
	if err == nil {
		t.Fatal("AnalyzeCustomerMessage() error = nil")
	}
	details, ok := ErrorDetails(err)
	if !ok {
		t.Fatalf("ErrorDetails() not available for %T: %v", err, err)
	}
	if details.StatusCode != http.StatusOK || details.MaxOutputTokens != 1500 {
		t.Fatalf("details = %#v", details)
	}
	if !strings.Contains(SafeErrorMessage(err), "parse customer understanding json response") {
		t.Fatalf("safe error = %q", SafeErrorMessage(err))
	}
}
