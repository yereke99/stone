package bot

import "testing"

func TestValidateOutgoingReplyBlocksUnsafeLLMClaims(t *testing.T) {
	conversation := Conversation{
		Language: "ru",
		Stage:    ClientStateAwaitingQualification,
		Lead: LeadState{
			Niche: "недвижимость",
		},
	}
	tests := []struct {
		name   string
		text   string
		status string
	}{
		{
			name:   "casual tone",
			text:   "Супер, ща уточним цель ролика!",
			status: "failed_too_casual",
		},
		{
			name:   "unsupported price",
			text:   "Стоимость будет 30 000 тг, можем начать.",
			status: "failed_unsupported_price",
		},
		{
			name:   "real drone promise",
			text:   "Да, снимем реальную съёмку с дрона для участка.",
			status: "failed_real_drone_promise",
		},
		{
			name:   "false media claim",
			text:   "Я уже отправил вам примеры по недвижимости.",
			status: "failed_false_media_claim",
		},
		{
			name:   "deadline too early",
			text:   "Понял. Какая цель ролика и когда нужно запустить?",
			status: "failed_deadline_too_early",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validateOutgoingReply(tt.text, ClientStateAwaitingQualification, conversation)
			if !result.Prevented || result.Status != tt.status {
				t.Fatalf("validation = %#v, want prevented status %q", result, tt.status)
			}
		})
	}
}

func TestValidateOutgoingReplyAllowsOfficialPricesAndSafeDroneLanguage(t *testing.T) {
	conversation := Conversation{Language: "ru"}
	official := "У нас 3 формата: Test — 35 000 тг, Basic — 50 000 тг, Standard — от 75 000 тг."
	if result := validateOutgoingReply(official, ClientStatePackagesPresented, conversation); result.Prevented {
		t.Fatalf("official price reply was blocked: %#v", result)
	}
	greetingConversation := Conversation{Language: "ru"}
	if result := validateOutgoingReply(QualificationGreetingText("ru"), ClientStateAwaitingQualification, greetingConversation); result.Prevented {
		t.Fatalf("qualification greeting was blocked: %#v", result)
	}
	if result := validateOutgoingReply(QuestionnaireOfferText("ru"), ClientStateAwaitingQuestionnaireConfirm, Conversation{Language: "ru", QuestionnaireOfferSent: true}); result.Prevented {
		t.Fatalf("questionnaire offer was blocked: %#v", result)
	}
	drone := "Мы можем подготовить AI-визуализацию/ролик под продажу. Если нужна именно реальная съёмка с дрона, это лучше отдельно уточнить с менеджером."
	if result := validateOutgoingReply(drone, ClientStateAwaitingQualification, conversation); result.Prevented {
		t.Fatalf("safe drone reply was blocked: %#v", result)
	}
}
