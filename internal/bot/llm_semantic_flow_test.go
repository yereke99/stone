package bot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yereke99/stone/internal/openai"
)

type capturingDecisionAI struct {
	response openai.CustomerUnderstanding
	messages []openai.Message
	calls    int
}

func (ai *capturingDecisionAI) AnalyzeCustomerMessage(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.CustomerUnderstanding, error) {
	ai.calls++
	ai.messages = append([]openai.Message(nil), messages...)
	return ai.response, nil
}

func (ai *capturingDecisionAI) GenerateReplyText(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.ReplyTextResponse, error) {
	return openai.ReplyTextResponse{ReplyText: ""}, nil
}

func semanticDecision(reply string) openai.CustomerUnderstanding {
	return openai.CustomerUnderstanding{
		Language:            "ru",
		Intent:              "qualification_answer",
		MessageMeaning:      "semantic test decision",
		ShouldUpdateState:   true,
		RecommendedAction:   "send_text",
		NextAction:          "send_text",
		CustomerReply:       reply,
		ReplyText:           reply,
		Confidence:          0.95,
		MissingFields:       []string{},
		PortfolioTags:       []string{},
		PortfolioSearchTags: []string{},
		UnresolvedQuestions: []string{},
	}
}

func TestProductionFlowSendsLatestTenRoleOrderedMessagesToLLM(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	response := semanticDecision("Келесі аптаға белгілеп қойдым.")
	response.Language = "kk"
	response.LeadUpdates.Deadline = testString("келесі апта")
	response.ExtractedFields.Deadline = testString("келесі апта")
	ai := &capturingDecisionAI{response: response}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	chatID := "chat-history-context"

	base := time.Now().Add(-20 * time.Minute)
	for i := 0; i < 6; i++ {
		store.Update(chatID, func(conversation *Conversation) {
			conversation.Messages = append(conversation.Messages,
				ChatMessage{Role: "user", Content: "client " + strconvItoa(i), CreatedAt: base.Add(time.Duration(i*2) * time.Minute)},
				ChatMessage{Role: "assistant", Content: "bot " + strconvItoa(i), CreatedAt: base.Add(time.Duration(i*2+1) * time.Minute)},
			)
		})
	}

	sendText(t, service, chatID, "Келесі аптаға.")

	if ai.calls != 1 || len(ai.messages) != 1 {
		t.Fatalf("AI calls/messages = %d/%d", ai.calls, len(ai.messages))
	}
	var payload struct {
		CurrentMessage struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"current_message"`
		RecentMessages []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"recent_messages"`
		KnownState json.RawMessage `json:"known_state"`
	}
	if err := json.Unmarshal([]byte(ai.messages[0].Content), &payload); err != nil {
		t.Fatalf("payload json error: %v\n%s", err, ai.messages[0].Content)
	}
	if payload.CurrentMessage.Role != "client" || payload.CurrentMessage.Text != "Келесі аптаға." {
		t.Fatalf("current message = %#v", payload.CurrentMessage)
	}
	if len(payload.RecentMessages) != 10 {
		t.Fatalf("recent messages = %d, want 10: %#v", len(payload.RecentMessages), payload.RecentMessages)
	}
	if payload.RecentMessages[0].Text != "client 1" || payload.RecentMessages[9].Text != "bot 5" {
		t.Fatalf("recent messages not chronological latest 10: %#v", payload.RecentMessages)
	}
	for i, message := range payload.RecentMessages {
		if i%2 == 0 && message.Role != "client" {
			t.Fatalf("message %d role = %q, want client", i, message.Role)
		}
		if i%2 == 1 && message.Role != "bot" {
			t.Fatalf("message %d role = %q, want bot", i, message.Role)
		}
	}
	if !json.Valid(payload.KnownState) || len(payload.KnownState) == 0 {
		t.Fatal("known_state was not included as valid JSON")
	}
}

func TestLLMDecisionUpdatesManyLeadFieldsInOneProductionMessage(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	reply := "Понял: сеть кофеен в Алматы, вертикальный ролик для Instagram, цель — увеличить доставку."
	response := semanticDecision(reply)
	response.LeadUpdates.BusinessNiche = testString("сеть кофеен")
	response.LeadUpdates.GeographicMarket = testString("Алматы")
	response.LeadUpdates.ProductOrService = testString("кофе и доставка")
	response.LeadUpdates.AdvertisingGoal = testString("увеличить заказы на доставку")
	response.LeadUpdates.DesiredVideoFormat = testString("короткий вертикальный")
	response.LeadUpdates.DistributionPlatform = testString("Instagram")
	response.LeadUpdates.Deadline = testString("следующая неделя")
	response.LeadUpdates.Budget = testString("около 150 000 тенге")
	response.LeadUpdates.NumberOfVideos = testString("1 ролик")
	response.ExtractedFields.Niche = testString("сеть кофеен")
	response.ExtractedFields.ProductOrService = testString("кофе и доставка")
	response.ExtractedFields.Goal = testString("увеличить заказы на доставку")
	response.ExtractedFields.Platform = testString("Instagram")
	response.ExtractedFields.Deadline = testString("следующая неделя")
	response.ExtractedFields.Budget = testString("около 150 000 тенге")
	response.ExtractedFields.VideoQuantity = testString("1 ролик")
	response.ConfirmedFields = []string{"niche", "product_or_service", "goal", "distribution_platform", "deadline", "budget", "video_quantity", "desired_video_format"}
	ai := &capturingDecisionAI{response: response}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)

	sendText(t, service, "chat-multi-field", "У нас сеть кофеен в Алматы. Хотим короткий вертикальный ролик для Instagram, чтобы увеличить доставку. Желательно запустить на следующей неделе. Бюджет около 150 000 тенге.")

	conversation := snapshotConversation(t, store, "chat-multi-field")
	lead := conversation.Lead
	if lead.Niche != "сеть кофеен" || lead.City != "Алматы" || lead.ProductOrService != "кофе и доставка" ||
		lead.Goal != "увеличить заказы на доставку" || lead.DesiredVideoFormat != "короткий вертикальный" ||
		lead.DistributionPlatform != "Instagram" || lead.Deadline != "следующая неделя" ||
		lead.Budget != "около 150 000 тенге" || lead.VideoQuantity != "1" {
		t.Fatalf("lead fields not updated together: %#v", lead)
	}
	if got := sender.messages[len(sender.messages)-1]; got != reply || strings.Contains(strings.ToLower(got), "какая у вас ниша") {
		t.Fatalf("customer reply not LLM/non-repeating: %q", got)
	}
}

func TestLLMDecisionDoesNotStoreBotPriceAsClientBudget(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	ai := &capturingDecisionAI{response: semanticDecision("Хорошо, если удобно, напишите продукт и задачу ролика.")}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	chatID := "chat-price-not-budget"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Messages = append(conversation.Messages, ChatMessage{Role: "assistant", Content: "Стоимость начинается от 35 000 тенге."})
		conversation.LastReplyText = "Стоимость начинается от 35 000 тенге."
	})

	sendText(t, service, chatID, "Хорошо.")

	if budget := snapshotConversation(t, store, chatID).Lead.Budget; budget != "" {
		t.Fatalf("bot price was stored as client budget: %q", budget)
	}
}

func TestLLMDecisionSeparatesContactFromCompanyAndABATSURegression(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	reply := "ABAT SU бойынша ақпаратты алдым. Ерекшеліктерді сақтадым, менеджерге қысқаша жеткіземін."
	response := semanticDecision(reply)
	response.Language = "kk"
	response.LeadUpdates.CompanyName = testString("ABAT SU")
	response.LeadUpdates.BrandName = testString("ABAT SU")
	response.LeadUpdates.BusinessNiche = testString("filtered bottled drinking water production and sales")
	response.LeadUpdates.ProductOrService = testString("filtered bottled drinking water")
	response.LeadUpdates.ProductFeatures = []string{"multi-stage filtration system", "produced in accordance with sanitary requirements"}
	response.LeadUpdates.AdvertisingGoal = nil
	response.LeadUpdates.Deadline = nil
	response.LeadUpdates.QuestionnaireStatus = testString("completed")
	response.LeadUpdates.ReadinessForManagerHandoff = true
	response.LeadUpdates.RecommendedNextStep = testString("Manager should contact the client and recommend a format.")
	response.ExtractedFields.Niche = testString("filtered bottled drinking water production and sales")
	response.ExtractedFields.ProductOrService = testString("filtered bottled drinking water")
	response.ReadyForManager = true
	response.RecommendedAction = "handoff"
	response.NextAction = "handoff"
	response.QuestionnaireStatus = "completed"
	response.ManagerSummary = "Company ABAT SU produces and sells filtered bottled drinking water. Product features: multi-stage filtration and sanitary compliance. Advertising goal and deadline are not specified."
	response.UnresolvedQuestions = []string{"advertising goal", "deadline"}
	ai := &capturingDecisionAI{response: response}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil, "77019519013@c.us")

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		ChatID:     "chat-abat-su",
		SenderName: "Ulan U.D.",
		Text:       "Біз – ABAT SU. Көпсатылы сүзгілеу жүйесі. Санитарлық талаптарға сәйкес дайындалады.",
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	conversation := snapshotConversation(t, store, "chat-abat-su")
	lead := conversation.Lead
	if lead.ContactName != "Ulan U.D." || lead.CompanyName != "ABAT SU" || lead.BrandName != "ABAT SU" {
		t.Fatalf("contact/company split failed: %#v", lead)
	}
	if lead.Goal != "" || lead.Deadline != "" {
		t.Fatalf("goal/deadline should remain unknown: %#v", lead)
	}
	if len(lead.ProductFeatures) != 2 {
		t.Fatalf("product features not stored: %#v", lead.ProductFeatures)
	}
	adminMessage := sender.messages[len(sender.messages)-1]
	for _, want := range []string{
		"Компания / бренд: ABAT SU",
		"Ниша: filtered bottled drinking water production and sales",
		"Продукт / услуга: filtered bottled drinking water",
		"Цель: не указано",
		"Срок: не указано",
		"Особенности продукта: multi-stage filtration system; produced in accordance with sanitary requirements",
		"Нерешённые вопросы: advertising goal; deadline",
	} {
		if !strings.Contains(adminMessage, want) {
			t.Fatalf("admin message missing %q:\n%s", want, adminMessage)
		}
	}
	if strings.Contains(adminMessage, "questionnaire_confirmation") {
		t.Fatalf("questionnaire state contradiction in admin message:\n%s", adminMessage)
	}
}

func strconvItoa(value int) string {
	return string(rune('0' + value))
}
