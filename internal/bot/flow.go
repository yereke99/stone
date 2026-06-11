package bot

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	minBotWords = 5
	maxBotWords = 35
)

type PortfolioLinks struct {
	TestURL     string
	BasicURL    string
	StandardURL string
}

func welcomeMessage(language Language) string {
	switch language {
	case LanguageKZ:
		return "48 сағатта түсірілімсіз AI жарнамалық ролик жасаймыз. Баға 35 000 тг бастап. Не сататыныңызды және ролик мақсатын жазыңыз."
	case LanguageEN:
		return "We create AI ad videos in 48 hours without filming. From 35,000 KZT. Share what you sell and the video goal."
	default:
		return "Делаем AI-рекламные ролики за 48 часов без съёмки. Стоимость от 35 000 тг. Подскажите, что продаёте и какая цель ролика."
	}
}

func askGoalMessage(language Language) string {
	switch language {
	case LanguageKZ:
		return "Негізгі мақсат қазір лидтер ме, әлде сатылым ба? Осыған қарай форматты нақтылаймыз."
	case LanguageEN:
		return "Is your main goal leads or direct sales? This helps us choose the right creative format."
	default:
		return "Главная цель сейчас — заявки или продажи? Так точнее подберём формат ролика и оффер."
	}
}

func askPlatformMessage(language Language) string {
	switch language {
	case LanguageKZ:
		return "Роликті қай жерде қолданасыз: Instagram, TikTok, Facebook, WhatsApp, сайт немесе басқа платформа?"
	case LanguageEN:
		return "Where will you use the video: Instagram, TikTok, Facebook, WhatsApp, website, or another platform?"
	default:
		return "Где планируете запускать ролик: Instagram, TikTok, Facebook, WhatsApp, сайт или другая площадка?"
	}
}

func askUsedAIMessage(language Language) string {
	switch language {
	case LanguageKZ:
		return "Бұрын жарнамада AI роликтерді қолдандыңыз ба?"
	case LanguageEN:
		return "Have you used AI videos in advertising before?"
	default:
		return "Использовали ли вы ИИ-ролики в рекламе ранее?"
	}
}

func clarifyUsedAIMessage(language Language) string {
	switch language {
	case LanguageKZ:
		return "Нақтылау үшін айтыңыз: AI роликтерді бұрын жарнамада қолдандыңыз ба?"
	case LanguageEN:
		return "Please clarify: have you used AI videos in advertising before?"
	default:
		return "Подскажите, использовали ли ИИ-ролики в рекламе ранее?"
	}
}

func offerMessage(language Language, usedBefore bool) string {
	if usedBefore {
		switch language {
		case LanguageKZ:
			return "Онда мақсатқа сай формат таңдаймыз: Test 35 000 тг, Basic 50 000 тг немесе Standard 75 000 тг."
		case LanguageEN:
			return "Then we choose by goal: Test 35,000 KZT, Basic 50,000 KZT, or Standard 75,000 KZT."
		default:
			return "Тогда подберём формат под цель: тестовый 35 000 тг, базовый 50 000 тг или стандарт 75 000 тг."
		}
	}

	switch language {
	case LanguageKZ:
		return "Бұрын қолданбасаңыз, 35 000 тг тестілік формат ұсынамыз. Basic немесе Standard алдында креативті тексереміз."
	case LanguageEN:
		return "If not, start with Test format for 35,000 KZT. We validate the creative before Basic or Standard."
	default:
		return "Тогда рекомендую тестовый формат за 35 000 тг — проверить креатив перед Базовым или Стандартом."
	}
}

func portfolioPromptMessage(language Language) string {
	switch language {
	case LanguageKZ:
		return "Портфолионы формат бойынша жіберемін немесе бірден қысқа бриф қабылдай аламын."
	case LanguageEN:
		return "I can send portfolio links by format or take a short brief now."
	default:
		return "Могу отправить портфолио по форматам или сразу принять короткий бриф."
	}
}

func questionnaireMessage(language Language) string {
	switch language {
	case LanguageKZ:
		return "Қысқа бриф: 1) не сатасыз; 2) мақсат; 3) аудитория; 4) сайт немесе Instagram."
	case LanguageEN:
		return "Short brief: 1) what you sell; 2) goal; 3) audience; 4) website or Instagram."
	default:
		return "Отлично. Короткий бриф: 1) что продаёте; 2) цель ролика; 3) аудитория; 4) сайт или Instagram."
	}
}

func objectionMessage(language Language) string {
	switch language {
	case LanguageKZ:
		return "Иә, сондықтан AI енгіздік - 48 сағатта минималды бюджетпен нәтиже алуыңыз үшін."
	case LanguageEN:
		return "Yes, that is why we use AI: you get a result in 48 hours with a minimal budget."
	default:
		return "Да, и именно поэтому мы внедрили ИИ — чтобы вы получали результат за 48 часов с минимальным бюджетом."
	}
}

func portfolioMessages(language Language, links PortfolioLinks) []string {
	return []string{portfolioLinksMessage(string(language), links, 0)}
}

func portfolioMessage(language Language, label string, url string) string {
	if strings.TrimSpace(url) == "" {
		switch language {
		case LanguageKZ:
			return fmt.Sprintf("%s портфолио сілтемесі әлі конфигурацияда орнатылмаған.", label)
		case LanguageEN:
			return fmt.Sprintf("%s portfolio link is not configured yet.", label)
		default:
			return fmt.Sprintf("%s портфолио пока не настроено в конфигурации.", label)
		}
	}

	switch language {
	case LanguageKZ:
		return fmt.Sprintf("%s портфолиосын мына жерден көре аласыз: %s", label, url)
	case LanguageEN:
		return fmt.Sprintf("View the %s portfolio here: %s", label, url)
	default:
		return fmt.Sprintf("Посмотрите %s портфолио здесь: %s", label, url)
	}
}

func validateMessagePolicy(messages []Reply) error {
	for _, message := range messages {
		count := wordCount(message.Text)
		if count < minBotWords || count > maxBotWords {
			return fmt.Errorf("bot message has %d words, allowed range is %d-%d: %q", count, minBotWords, maxBotWords, message.Text)
		}
	}
	return nil
}

func wordCount(text string) int {
	count := 0
	for _, field := range strings.Fields(text) {
		clean := strings.TrimFunc(field, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if clean == "" || isNumericToken(clean) {
			continue
		}
		count++
	}
	return count
}

func isNumericToken(token string) bool {
	for _, r := range token {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
