package bot

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Greetings, confirmations, case/price questions, timing words and stop
// commands must never be extracted as a niche, even when the niche is the only
// missing lead field (the most aggressive extraction mode).
func TestNonBusinessMessagesAreNeverSavedAsNiche(t *testing.T) {
	leadMissingOnlyNiche := LeadState{
		Goal:     "рост продаж",
		Deadline: "на этой неделе",
	}
	tests := []struct {
		text       string
		wantIntent string
	}{
		{text: "ДОБРОЕ УТРО", wantIntent: IntentGreeting},
		{text: "Доброе утро"},
		{text: "Добрый день"},
		{text: "Здравствуйте", wantIntent: IntentGreeting},
		{text: "да"},
		{text: "ок"},
		{text: "принято"},
		{text: "понял"},
		{text: "хорошо"},
		{text: "Есть кейсы?", wantIntent: IntentPortfolioRequest},
		{text: "Как отправите кейсы?", wantIntent: IntentPortfolioRequest},
		{text: "Хронометраж видео какой", wantIntent: IntentFAQ},
		{text: "Сколько секунд ролик?", wantIntent: IntentFAQ},
		{text: "Сейчас"},
		{text: "Завтра"},
		{text: "Бот собираюсь"},
		{text: "стоп"},
		{text: "stop"},
		{text: "Сколько стоит?", wantIntent: IntentPriceQuestion},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			for _, lead := range []LeadState{{}, leadMissingOnlyNiche} {
				analysis := AnalyzeCustomerMessage(tt.text, lead, "ru")
				if analysis.Niche != nil {
					t.Fatalf("AnalyzeCustomerMessage(%q) extracted niche %q, want none", tt.text, *analysis.Niche)
				}
				updated := lead
				updated.ApplyAnalysis(analysis)
				if updated.Niche != lead.Niche {
					t.Fatalf("ApplyAnalysis(%q) changed niche from %q to %q", tt.text, lead.Niche, updated.Niche)
				}
				if tt.wantIntent != "" && lead.Niche == "" && analysis.Intent != tt.wantIntent {
					t.Fatalf("AnalyzeCustomerMessage(%q) intent = %q, want %q", tt.text, analysis.Intent, tt.wantIntent)
				}
			}
		})
	}
}

func TestBusinessMessagesAreExtracted(t *testing.T) {
	leadMissingOnlyNiche := LeadState{Goal: "рост продаж"}

	nicheTests := []struct {
		text string
		lead LeadState
	}{
		{text: "мебель", lead: leadMissingOnlyNiche},
		{text: "ТВ зоны", lead: LeadState{}},
		{text: "продаём обувь", lead: LeadState{}},
		{text: "салон красоты", lead: LeadState{}},
		{text: "у нас мебельная ниша, в том числе ТВ зона", lead: LeadState{}},
	}
	for _, tt := range nicheTests {
		t.Run("niche/"+tt.text, func(t *testing.T) {
			analysis := AnalyzeCustomerMessage(tt.text, tt.lead, "ru")
			if analysis.Niche == nil || strings.TrimSpace(*analysis.Niche) == "" {
				t.Fatalf("AnalyzeCustomerMessage(%q) did not extract a niche", tt.text)
			}
		})
	}

	goalTests := []string{"Продажи", "Хочу заявки", "узнаваемость", "хочу больше клиентов"}
	for _, text := range goalTests {
		t.Run("goal/"+text, func(t *testing.T) {
			analysis := AnalyzeCustomerMessage(text, LeadState{Niche: "мебель"}, "ru")
			if analysis.Goal == nil || strings.TrimSpace(*analysis.Goal) == "" {
				t.Fatalf("AnalyzeCustomerMessage(%q) did not extract a goal", text)
			}
			if analysis.Niche != nil && *analysis.Niche != "мебель" {
				t.Fatalf("AnalyzeCustomerMessage(%q) overwrote niche with %q", text, *analysis.Niche)
			}
		})
	}
}

// The first qualification stage requires only the niche and the goal: launch
// timing must never be a missing/required field anymore.
func TestFirstQualificationRequiresOnlyNicheAndGoal(t *testing.T) {
	if missing := (LeadState{}).MissingCoreFields(); !sameFields(missing, []string{fieldNiche, fieldGoal}) {
		t.Fatalf("MissingCoreFields() = %#v, want niche/goal only", missing)
	}
	if missing := qualificationMissingFields(LeadState{Niche: "мебель"}); !sameFields(missing, []string{fieldGoal}) {
		t.Fatalf("qualificationMissingFields() = %#v, want goal only", missing)
	}
	lead := LeadState{Niche: "мебель", Goal: "рост продаж", SelectedPackage: "standard"}
	if !leadHasRequiredManagerFields(lead) {
		t.Fatal("lead with niche/goal/package must be manager-ready without a deadline")
	}
}

// No first-stage qualification prompt may ask about launch timing.
func TestFirstStagePromptsDoNotAskDeadline(t *testing.T) {
	forbidden := []string{
		"срок", "Срок", "когда", "Когда", "deadline", "timeline",
		"мерзім", "қашан", "Қашан", "when do you need", "when to launch",
	}
	prompts := map[string]string{}
	for _, language := range []string{"ru", "kk", "en"} {
		prompts["greeting/"+language] = QualificationGreetingText(language)
		prompts["questions/"+language] = QualificationQuestionsText(language)
		prompts["reengagement/"+language] = LegacyReengagementClarificationText(language)
		prompts["fallback/"+language] = FallbackText(language)
		prompts["followup-both/"+language] = qualificationFollowupText(language, Conversation{})
		prompts["followup-goal/"+language] = qualificationFollowupText(language, Conversation{Lead: LeadState{Niche: "мебель"}})
		prompts["followup-niche/"+language] = qualificationFollowupText(language, Conversation{Lead: LeadState{Goal: "рост продаж"}})
		prompts["food-goal/"+language] = foodExamplesMissingText(language, LeadState{Niche: "еда"}, []string{fieldGoal})
		prompts["cases-both/"+language] = casesRequestQualificationText(language, LeadState{}, []string{fieldNiche, fieldGoal}, false)
		prompts["cases-delivery/"+language] = casesRequestQualificationText(language, LeadState{}, []string{fieldNiche, fieldGoal}, true)
	}
	for name, prompt := range prompts {
		for _, word := range forbidden {
			if strings.Contains(prompt, word) {
				t.Fatalf("first-stage prompt %q asks for timing (%q): %q", name, word, prompt)
			}
		}
	}
}

func TestGreetingDoesNotSaveNicheAndAsksQualification(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-greeting-no-niche"

	sendText(t, service, chatID, "ДОБРОЕ УТРО")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "" {
		t.Fatalf("greeting was saved as niche: %q", conversation.Lead.Niche)
	}
	if len(sender.messages) != 1 || sender.messages[0] != QualificationGreetingText("ru") {
		t.Fatalf("greeting reply = %#v, want qualification greeting", sender.messages)
	}
}

func TestGoalAnswerAfterKnownNicheDoesNotRepeatNicheQuestion(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-goal-after-niche"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
		conversation.Lead.Niche = "мебель"
	})

	sendText(t, service, chatID, "Продажи")

	conversation := snapshotConversation(t, store, chatID)
	if !isValidGoal(conversation.Lead.Goal) {
		t.Fatalf("goal was not saved: %#v", conversation.Lead)
	}
	if conversation.Lead.Niche != "мебель" {
		t.Fatalf("niche was overwritten: %q", conversation.Lead.Niche)
	}
	for _, message := range sender.messages {
		lower := strings.ToLower(message)
		if strings.Contains(lower, "какая ниша") || strings.Contains(lower, "что продаёте") || strings.Contains(lower, "для какой ниши") {
			t.Fatalf("bot asked niche again after goal answer: %q", message)
		}
	}
}

func TestQuotedConstructionReplyUsesTypedTextAndPreservesGoal(t *testing.T) {
	sender := &fakeSender{fileMessageIDs: []string{"test-video-id", "basic-video-id", "standard-video-id"}}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-quoted-construction-answer"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Language = "ru"
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
		conversation.Lead.Goal = "получать заявки"
	})

	currentText := "Плиточные клея, шпаклёвка, штукатурка и т.д.\nОсновные клиенты строительные компании, частники!"
	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		IDMessage:       "quoted-construction-answer",
		ChatID:          chatID,
		TypeMessage:     "quotedMessage",
		Text:            currentText,
		QuotedMessageID: "old-qualification-question",
		QuotedType:      "textMessage",
		QuotedText:      "Чтобы предложить точный формат, напишите, пожалуйста, что именно продвигаем, кто ваша аудитория и какая цель: заявки, продажи или узнаваемость.",
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	conversation := snapshotConversation(t, store, chatID)
	niche := normalizeForAnalysis(conversation.Lead.Niche)
	for _, want := range []string{"плиточные клея", "шпаклевка", "штукатурка"} {
		if !strings.Contains(niche, want) {
			t.Fatalf("niche = %q, want it to contain %q", conversation.Lead.Niche, want)
		}
	}
	if conversation.Lead.Goal != "получать заявки" {
		t.Fatalf("goal = %q, want preserved known goal", conversation.Lead.Goal)
	}
	audience := normalizeForAnalysis(conversation.Lead.TargetAudience)
	if !strings.Contains(audience, "строительные компании") || !strings.Contains(audience, "частники") {
		t.Fatalf("audience = %q, want construction companies and private customers", conversation.Lead.TargetAudience)
	}
	if len(sender.files) != 3 {
		t.Fatalf("qualified quoted reply should send package examples, files=%#v", sender.files)
	}
	for _, message := range sender.messages {
		lower := strings.ToLower(message)
		if strings.Contains(lower, "что продаёте") ||
			strings.Contains(lower, "что продаете") ||
			strings.Contains(lower, "какая у вас ниша") {
			t.Fatalf("bot asked niche again after quoted typed answer: %q", message)
		}
	}
}

func TestDurationQuestionAnswersFAQAndDoesNotPolluteLead(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-duration-question"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Language = "ru"
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
	})

	sendText(t, service, chatID, "Хронометраж видео какой")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "" || conversation.Lead.Goal != "" {
		t.Fatalf("duration question polluted lead fields: %#v", conversation.Lead)
	}
	if len(sender.files) != 0 {
		t.Fatalf("duration FAQ should not send videos before qualification: %#v", sender.files)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("messages = %#v, want one FAQ reply", sender.messages)
	}
	reply := sender.messages[0]
	if !strings.Contains(reply, "30–45 секунд") {
		t.Fatalf("duration reply = %q, want 30–45 секунд answer", reply)
	}
	if strings.Contains(strings.ToLower(reply), "понял, хронометраж") {
		t.Fatalf("duration question was echoed as qualification data: %q", reply)
	}
}

func TestCasesRequestWithoutQualificationAnswersAndAsksFields(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-cases-unqualified"

	sendText(t, service, chatID, "Есть кейсы?")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "" {
		t.Fatalf("cases question was saved as niche: %q", conversation.Lead.Niche)
	}
	if len(sender.files) != 0 {
		t.Fatalf("videos were sent before qualification: %#v", sender.files)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("messages = %#v, want a single cases answer", sender.messages)
	}
	reply := sender.messages[0]
	if !strings.Contains(reply, "кейсы можем отправить") || !strings.Contains(reply, "что продаёте") || !strings.Contains(reply, "цель") {
		t.Fatalf("cases reply did not answer and ask niche/goal: %q", reply)
	}
}

func TestCasesDeliveryQuestionAnswersAndAsksFields(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-cases-delivery"

	sendText(t, service, chatID, "Как отправите кейсы?")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "" {
		t.Fatalf("delivery question was saved as niche: %q", conversation.Lead.Niche)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("messages = %#v, want a single delivery answer", sender.messages)
	}
	reply := sender.messages[0]
	if !strings.Contains(reply, "Отправим прямо сюда") || !strings.Contains(reply, "цель") {
		t.Fatalf("delivery reply did not answer and ask qualification: %q", reply)
	}
}

func TestCasesRequestWithQualifiedLeadSendsExamples(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-cases-qualified"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
		conversation.Lead.Niche = "мебель"
		conversation.Lead.Goal = "рост продаж"
	})

	sendText(t, service, chatID, "Есть кейсы?")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "мебель" || !isValidGoal(conversation.Lead.Goal) {
		t.Fatalf("qualified lead fields were damaged: %#v", conversation.Lead)
	}
	if len(sender.files) != 3 {
		t.Fatalf("examples were not sent for a qualified lead: %#v", sender.files)
	}
	first := sender.messages[0]
	if !strings.Contains(first, "отправим примеры") || !strings.Contains(first, "мебель") {
		t.Fatalf("cases acknowledgement is wrong: %q", first)
	}
	for _, message := range sender.messages {
		lower := strings.ToLower(message)
		if strings.Contains(lower, "что продаёте") || strings.Contains(lower, "какая у вас ниша") {
			t.Fatalf("bot re-asked qualification for a qualified lead: %q", message)
		}
	}
}

// After a manual admin stop the customer must be ignored completely: no AI
// call, no reply, no media, no follow-up.
func TestStoppedChatIgnoresCustomerSilently(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	ai := &fakeAI{}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	chatID := "77043330000@c.us"

	if err := store.MarkManualStop(context.Background(), chatID, "stop-message-id", time.Now().UTC(), StoppedByManualAdmin); err != nil {
		t.Fatalf("MarkManualStop() error = %v", err)
	}

	for _, text := range []string{"Здравствуйте", "Есть кейсы?", "мебель, цель продажи"} {
		sendText(t, service, chatID, text)
	}

	if ai.analysisCalled || ai.called {
		t.Fatalf("AI was called for a stopped chat: analysis=%v sales=%v", ai.analysisCalled, ai.called)
	}
	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("stopped chat received automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
	conversation := snapshotConversation(t, store, chatID)
	if !conversation.Stopped || conversation.StopReason != StopReasonManualAdminStop || conversation.StoppedBy != StoppedByManualAdmin {
		t.Fatalf("stop state was lost: stopped=%v reason=%q by=%q", conversation.Stopped, conversation.StopReason, conversation.StoppedBy)
	}
	if conversation.StopMessageID != "stop-message-id" || conversation.StoppedAt.IsZero() {
		t.Fatalf("stop metadata was lost: id=%q at=%v", conversation.StopMessageID, conversation.StoppedAt)
	}
	if !conversation.NextFollowupAt.IsZero() || strings.TrimSpace(conversation.FollowupStage) != "" {
		t.Fatalf("follow-up survived manual stop: at=%v stage=%q", conversation.NextFollowupAt, conversation.FollowupStage)
	}
}

func TestTimingWordsAreNotSavedAsNicheInFlow(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-timing-words"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
		conversation.Lead.Goal = "рост продаж"
	})

	for _, text := range []string{"Сейчас", "Завтра"} {
		store.Update(chatID, func(conversation *Conversation) {
			conversation.LastReplyAt = time.Now().Add(-10 * time.Second)
		})
		sendText(t, service, chatID, text)
		conversation := snapshotConversation(t, store, chatID)
		if conversation.Lead.Niche != "" {
			t.Fatalf("timing word %q was saved as niche: %q", text, conversation.Lead.Niche)
		}
	}
}
