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
		return "Өтінішіңізге рақмет 🙌 48 сағатта түсірілімсіз жарнамалық AI ролик жасаймыз. Жарнамаға дайын. Баға 35 000 тг бастап. Нишаңыз, мақсатыңыз, мерзіміңіз қандай?"
	case LanguageEN:
		return "Thanks for reaching out 🙌 We create AI ad videos in 48 hours without filming, ready for launch. Pricing starts from 35,000 KZT. Please share your niche, goal, and timeline."
	default:
		return "Спасибо за обращение 🙌 Делаем ИИ рекламные ролики за 48 часов без съёмки, под запуск рекламы. Стоимость от 35 000 тг. Чтобы понять, подойдём ли мы вам, подскажите: 1) В какой нише работаете? 2) Какая цель? 3) Сроки?"
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
		return "Жарнаманы қай платформада іске қосуды жоспарлайсыз: Instagram, TikTok, YouTube немесе басқа ма?"
	case LanguageEN:
		return "Which platform will you advertise on: Instagram, TikTok, YouTube, or another channel?"
	default:
		return "Где планируете запускать рекламу: Instagram, TikTok, YouTube или другая площадка?"
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
			return "Тогда подберём формат под задачу: Test 35 000 тг, Basic 50 000 тг или Standard 75 000 тг."
		}
	}

	switch language {
	case LanguageKZ:
		return "Бұрын қолданбасаңыз, Test format 35 000 тг ұсынамыз. Креативті Basic 50 000 тг немесе Standard 75 000 тг алдында тексереміз."
	case LanguageEN:
		return "If not, start with Test format for 35,000 KZT. We validate the creative before Basic 50,000 KZT or Standard 75,000 KZT."
	default:
		return "Если ранее не тестировали ИИ-ролики, начните с Test format за 35 000 тг. Проверим креатив перед Basic 50 000 тг или Standard 75 000 тг."
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
		return "Қысқа бриф: 1) өнім және құндылық; 2) аудитория ауырсынуы; 3) оффер. Сайт немесе Instagram жіберіңіз."
	case LanguageEN:
		return "Brief questionnaire: 1) product and value; 2) audience pains; 3) offer. Please send website or Instagram too."
	default:
		return "Заполните коротко: 1) продукт и ценность; 2) боли аудитории; 3) оффер. Также пришлите сайт или Instagram."
	}
}

func objectionMessage(language Language) string {
	switch language {
	case LanguageKZ:
		return "Иә, сондықтан AI енгіздік - 48 сағатта минималды бюджетпен нәтиже алуыңыз үшін."
	case LanguageEN:
		return "Yes, that is why we use AI: you get a result in 48 hours with a minimal budget."
	default:
		return "Да, и именно поэтому мы внедрили ИИ — чтобы вы получали результат за 48 часов с минимальным бюджетом"
	}
}

func portfolioMessages(language Language, links PortfolioLinks) []string {
	return []string{
		portfolioMessage(language, "Test format", links.TestURL),
		portfolioMessage(language, "Basic format", links.BasicURL),
		portfolioMessage(language, "Standard format", links.StandardURL),
	}
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
