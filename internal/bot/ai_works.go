package bot

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	AIWorksDir        = "ai-works"
	maxAIWorkExamples = 2
)

type AIWorkVideo struct {
	Category string
	FileName string
	Path     string
	Tags     []string
}

type AIWorkSelection struct {
	Videos []AIWorkVideo
	Tags   []string
	Exact  bool
}

var aiWorksByCategory = map[string][]string{
	"app": {
		"initaxi.mp4",
	},
	"auto": {
		"car_sale_2.mp4",
		"car_sale.mp4",
	},
	"b2b": {
		"computer_repair.mp4",
		"septic_tank.mp4",
		"supermarket.mp4",
	},
	"event": {
		"ensemble.mp4",
		"vocal.mp4",
	},
	"fashion": {
		"fashion.mp4",
		"snowboard.mp4",
		"suit.mp4",
	},
	"factories": {
		"presentation.mp4",
		"safety_briefing.mp4",
		"thermal_power_plant.mp4",
	},
	"horeca": {
		"burger.mp4",
		"oreo.mp4",
		"tea.mp4",
	},
	"interior": {
		"curtains_2.mp4",
		"curtains.mp4",
		"fence.mp4",
		"gates.mp4",
		"interior_2.mp4",
		"interior.mp4",
		"renovation.mp4",
		"smart_home.mp4",
		"stretch_ceiling_2.mp4",
		"stretch_ceiling_3.mp4",
		"stretch_ceiling.mp4",
		"window.mp4",
	},
	"led_ekran": {
		"ai_parking_led_rus.mp4",
		"auto.mp4",
		"digital_tenge.mp4",
		"sapsan_1.mp4",
		"skyvision_2.mp4",
	},
	"logistics": {
		"logistics.mp4",
	},
	"medicine": {
		"neurolab_25_sec_rus.mp4",
		"sunkar_final.mp4",
		"whatsapp_video_2026_06_19_15_28_52.mp4",
	},
	"real_estate": {
		"realtor_land.mp4",
		"real_estate_2.mp4",
		"real_estate_3.mp4",
		"real_estate_4.mp4",
		"real_estate_5.mp4",
		"real_estate.mp4",
		"realtor_2.mp4",
		"realtor_3.mp4",
		"realtor.mp4",
	},
	"retail": {
		"chemicals.mp4",
		"fireplaces.mp4",
		"generator.mp4",
		"perfume_2.mp4",
		"perfume.mp4",
		"smart_speaker.mp4",
		"spf.mp4",
	},
	"sport": {
		"football_2.mp4",
		"football.mp4",
	},
	"tourism": {
		"almaty_tour.mp4",
		"east_kazakhstan_presentation.mp4",
		"talgar_fairytale.mp4",
		"umrah.mp4",
	},
}

var aiWorkCategoryTags = map[string][]string{
	"app":         {"app", "mobile_app", "taxi", "service"},
	"auto":        {"auto", "car", "cars", "dealership", "detailing"},
	"b2b":         {"b2b", "business", "service", "repair", "supermarket", "wholesale"},
	"event":       {"event", "concert", "show", "performance"},
	"fashion":     {"fashion", "clothing", "clothes", "apparel", "brand"},
	"factories":   {"factory", "factories", "production", "industrial", "plant", "equipment", "machinery", "construction"},
	"horeca":      {"food", "restaurant", "horeca", "cafe", "burger", "tea", "product", "fmcg", "grocery", "dairy"},
	"interior":    {"interior", "renovation", "home", "construction", "repair", "furniture", "kitchen"},
	"led_ekran":   {"led", "screen", "digital", "outdoor_ads"},
	"logistics":   {"logistics", "delivery", "transport"},
	"medicine":    {"medicine", "medical", "clinic", "healthcare"},
	"real_estate": {"real_estate", "property", "land", "realtor", "apartment", "construction", "drone", "visualization"},
	"retail":      {"retail", "shop", "store", "product", "perfume", "generator", "chemicals"},
	"sport":       {"sport", "football", "fitness"},
	"tourism":     {"tourism", "travel", "hotel", "resort", "tour", "umrah"},
}

func selectAIWorkExamples(lead LeadState, analysis CustomerAnalysis, limit int) AIWorkSelection {
	if limit < 0 {
		limit = maxAIWorkExamples
	}
	tags := aiWorkTagsForLead(lead, analysis)
	return selectAIWorkExamplesByTags(tags, limit)
}

func selectAIWorkExamplesByTags(tags []string, limit int) AIWorkSelection {
	if limit < 0 {
		limit = maxAIWorkExamples
	}
	tags = normalizePortfolioTags(tags)
	if len(tags) == 0 {
		return AIWorkSelection{}
	}

	type scoredVideo struct {
		video AIWorkVideo
		score int
		order int
	}
	scored := make([]scoredVideo, 0, 32)
	order := 0
	for _, portfolioCase := range portfolioCaseCatalogue() {
		if !portfolioCase.IsActive {
			order++
			continue
		}
		video := aiWorkVideoFromCase(portfolioCase)
		score := aiWorkMatchScore(tags, portfolioCase.Tags)
		if semanticCloseCase(tags, portfolioCase.Tags) {
			score++
		}
		if score == 0 {
			order++
			continue
		}
		scored = append(scored, scoredVideo{video: video, score: score, order: order})
		order++
	}
	if len(scored) == 0 {
		return AIWorkSelection{}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].order < scored[j].order
	})

	capacity := limit
	if capacity <= 0 || capacity > len(scored) {
		capacity = len(scored)
	}
	videos := make([]AIWorkVideo, 0, capacity)
	for _, item := range scored {
		videos = append(videos, item.video)
		if limit > 0 && len(videos) >= limit {
			break
		}
	}
	return AIWorkSelection{
		Videos: videos,
		Tags:   tags,
		Exact:  scored[0].score >= 2 || categoryTagMatched(tags, videos[0].Category),
	}
}

func aiWorkExamplesLimit() int {
	raw := strings.TrimSpace(os.Getenv("AI_WORK_EXAMPLES_LIMIT"))
	if raw == "" {
		return maxAIWorkExamples
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return maxAIWorkExamples
	}
	return value
}

func aiWorkTagsForLead(lead LeadState, analysis CustomerAnalysis) []string {
	tags := normalizePortfolioTags(analysis.PortfolioTags)
	for _, text := range []string{
		lead.Niche,
		lead.ProductOrService,
		lead.Goal,
		lead.CampaignContext,
		lead.HookIdea,
		lead.Notes,
		lead.FreeText,
	} {
		tags = append(tags, portfolioTagsFromText(text)...)
	}
	if analysis.Niche != nil {
		tags = append(tags, portfolioTagsFromText(*analysis.Niche)...)
	}
	if analysis.ProductOrService != nil {
		tags = append(tags, portfolioTagsFromText(*analysis.ProductOrService)...)
	}
	if analysis.Goal != nil {
		tags = append(tags, portfolioTagsFromText(*analysis.Goal)...)
	}
	if analysis.CampaignContext != nil {
		tags = append(tags, portfolioTagsFromText(*analysis.CampaignContext)...)
	}
	return normalizePortfolioTags(tags)
}

func portfolioTagsFromText(text string) []string {
	normalized := normalizeForAnalysis(text)
	if normalized == "" {
		return nil
	}
	tags := make([]string, 0, 4)
	add := func(values ...string) {
		tags = append(tags, values...)
	}
	if containsAny(normalized, []string{"земель", "участ", "земля", "land plot", "land"}) {
		add("real_estate", "property", "land")
	}
	if containsAny(normalized, []string{"недвиж", "риелтор", "realtor", "real estate", "property", "квартир", "дом", "коттедж", "жк", "застройщик"}) {
		add("real_estate", "property")
	}
	if containsAny(normalized, []string{"строитель", "construction", "новострой", "жилой комплекс"}) {
		add("real_estate", "construction")
	}
	if containsAny(normalized, []string{"дрон", "drone", "аэросъем", "аеросъем", "съемка с дрона", "съемка", "снимать"}) {
		add("drone")
	}
	if containsAny(normalized, []string{"визуализац", "перспектив", "visualization", "render", "рендер"}) {
		add("visualization")
	}
	if containsAny(normalized, []string{"туризм", "турист", "путешеств", "тур ", "туры", "отель", "гостиниц", "resort", "travel", "tourism", "hotel", "umrah"}) {
		add("tourism", "travel")
	}
	if containsAny(normalized, []string{"авто", "машин", "автосалон", "car", "cars", "dealership"}) {
		add("auto", "car")
	}
	if containsAny(normalized, []string{"автохим", "авто хим", "автокосмет", "детейлинг", "detailing", "auto chemicals", "car care"}) {
		add("auto", "detailing", "chemicals", "product")
	}
	if containsAny(normalized, []string{"мебел", "кухн", "шкаф", "диван", "furniture", "kitchen"}) {
		add("interior", "furniture", "home", "product")
	}
	if containsAny(normalized, []string{"одеж", "бренд одежды", "fashion", "clothing", "clothes", "apparel"}) {
		add("fashion", "clothing")
	}
	if containsAny(normalized, []string{"детская одеж", "балалар киім", "балалар киімі", "children clothing", "kids clothes"}) {
		add("fashion", "clothing", "children")
	}
	if containsAny(normalized, []string{"ресторан", "кафе", "еда", "доставка еды", "бургер", "чай", "food", "restaurant", "horeca"}) {
		add("food", "restaurant", "horeca")
	}
	if containsAny(normalized, []string{"сливочн", "молочн", "молоко", "масло", "сыры", "сыров", "творог", "кефир", "йогурт", "dairy", "butter", "cheese", "milk"}) {
		add("food", "dairy", "product", "fmcg")
	}
	if containsAny(normalized, []string{"продукты питания", "продуктов питания", "продукт питания", "бакале", "grocery", "fmcg", "напитк", "снек", "кондитер"}) {
		add("food", "product", "fmcg", "grocery")
	}
	if containsAny(normalized, []string{"оптом", "оптов", "опт ", "wholesale", "дистрибь", "дистриб"}) || normalized == "опт" {
		add("wholesale", "b2b", "business")
	}
	if containsAny(normalized, []string{"медицин", "клиник", "стоматолог", "стоматология", "имплантац", "medicine", "medical", "clinic", "dentistry", "dental"}) {
		add("medicine", "medical", "dentistry")
	}
	if containsAny(normalized, []string{"логист", "доставка", "груз", "transport", "logistics"}) {
		add("logistics")
	}
	if containsAny(normalized, []string{"погрузчик", "спецтехника", "спец техника", "loader", "forklift", "construction equipment", "industrial equipment"}) {
		add("construction", "industrial", "equipment", "machinery")
	}
	if containsAny(normalized, []string{"барбершоп", "барбер шоп", "barbershop", "barber shop", "barber", "шаштараз", "салон красоты", "beauty", "salon"}) {
		add("barbershop", "beauty", "salon")
	}
	return normalizePortfolioTags(tags)
}

func normalizePortfolioTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		tag = strings.ReplaceAll(tag, "-", "_")
		tag = strings.ReplaceAll(tag, " ", "_")
		switch tag {
		case "realestate", "real_estate", "estate", "property", "realty", "realtor", "land", "apartment":
			tag = mapPortfolioAlias(tag, "real_estate")
		case "restaurant", "restaurants", "cafe", "food", "foods", "horeca":
			if tag != "restaurant" && tag != "food" {
				tag = "horeca"
			}
		case "travel", "tour", "tours", "hotel", "resort":
			tag = "tourism"
		case "car", "cars", "dealership":
			tag = "auto"
		case "clothes", "clothing", "apparel":
			tag = "fashion"
		case "medical", "healthcare", "clinic":
			tag = "medicine"
		case "dentistry", "dental", "стоматология", "имплантация":
			tag = "medicine"
		case "machinery":
			tag = "equipment"
		case "kitchens", "kitchen_furniture", "custom_furniture", "мебель", "кухни", "кухня":
			tag = "furniture"
		case "детская_одежда", "kids_clothes", "children_clothing":
			tag = "fashion"
		case "auto_chemicals", "автохимия", "автохим", "car_care":
			tag = "chemicals"
		case "сливочное_масло", "масло", "молочная_продукция", "молочные_продукты", "молочка", "dairy_products", "butter", "cheese", "milk":
			tag = "dairy"
		case "продукты_питания", "продукты", "еда", "питание", "food_products":
			tag = "food"
		case "опт", "оптом", "оптовые_продажи", "оптовая_торговля", "опт_продажи":
			tag = "wholesale"
		case "товар", "товары", "продукция", "товарная_реклама", "product_advertising", "product_ads", "goods":
			tag = "product"
		case "фмсж", "фмсг":
			tag = "fmcg"
		case "бакалея", "grocery_store", "супермаркет":
			tag = "grocery"
		}
		if tag == "" {
			continue
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result
}

func mapPortfolioAlias(original string, fallback string) string {
	switch original {
	case "land", "property", "realtor", "apartment":
		return original
	default:
		return fallback
	}
}

func aiWorkVideo(category string, fileName string) AIWorkVideo {
	category = strings.TrimSpace(category)
	fileName = strings.TrimSpace(filepath.Base(fileName))
	tags := append([]string{category}, aiWorkCategoryTags[category]...)
	switch fileName {
	case "realtor_land.mp4":
		tags = append(tags, "land", "drone", "visualization", "property", "realtor")
	case "real_estate.mp4", "real_estate_2.mp4", "real_estate_3.mp4", "real_estate_4.mp4", "real_estate_5.mp4":
		tags = append(tags, "property", "visualization", "apartment", "construction")
	case "realtor.mp4", "realtor_2.mp4", "realtor_3.mp4":
		tags = append(tags, "realtor", "property", "sales")
	case "almaty_tour.mp4", "east_kazakhstan_presentation.mp4", "talgar_fairytale.mp4", "umrah.mp4":
		tags = append(tags, "tourism", "travel", "tour", "destination")
	case "car_sale.mp4", "car_sale_2.mp4":
		tags = append(tags, "auto", "car", "sales")
	case "burger.mp4", "oreo.mp4", "tea.mp4":
		tags = append(tags, "food", "restaurant", "horeca", "product", "fmcg")
	case "supermarket.mp4":
		tags = append(tags, "food", "grocery", "fmcg", "retail", "product", "wholesale")
	case "chemicals.mp4":
		tags = append(tags, "chemicals", "product", "retail", "auto", "detailing")
	case "curtains.mp4", "curtains_2.mp4", "interior.mp4", "interior_2.mp4", "renovation.mp4", "smart_home.mp4":
		tags = append(tags, "interior", "home", "furniture", "renovation")
	case "window.mp4", "gates.mp4", "fence.mp4", "stretch_ceiling.mp4", "stretch_ceiling_2.mp4", "stretch_ceiling_3.mp4":
		tags = append(tags, "interior", "home", "construction", "repair")
	case "presentation.mp4", "safety_briefing.mp4", "thermal_power_plant.mp4":
		tags = append(tags, "industrial", "equipment", "machinery", "construction")
	}
	return AIWorkVideo{
		Category: category,
		FileName: fileName,
		Path:     aiWorkPath(category, fileName),
		Tags:     normalizePortfolioTags(tags),
	}
}

func aiWorkVideoFromCase(item PortfolioCase) AIWorkVideo {
	videoPath := normalizeAIWorkVideoPath(item.VideoPath)
	if videoPath == "" {
		return AIWorkVideo{}
	}
	parts := strings.Split(videoPath, "/")
	if len(parts) != 3 {
		return AIWorkVideo{}
	}
	return AIWorkVideo{
		Category: parts[1],
		FileName: parts[2],
		Path:     videoPath,
		Tags:     normalizePortfolioTags(item.Tags),
	}
}

func semanticCloseCase(requested []string, candidate []string) bool {
	requested = normalizePortfolioTags(requested)
	candidate = normalizePortfolioTags(candidate)
	pairs := [][2]string{
		{"chemicals", "auto"},
		{"detailing", "auto"},
		{"furniture", "interior"},
		{"kitchen", "interior"},
		{"children", "fashion"},
		{"dentistry", "medicine"},
		{"clinic", "medicine"},
	}
	for _, pair := range pairs {
		if stringInSlice(pair[0], requested) && stringInSlice(pair[1], candidate) {
			return true
		}
		if stringInSlice(pair[1], requested) && stringInSlice(pair[0], candidate) {
			return true
		}
	}
	return false
}

func aiWorkPath(category string, fileName string) string {
	return filepath.ToSlash(filepath.Join(AIWorksDir, category, filepath.Base(fileName)))
}

func aiWorkMatchScore(requested []string, videoTags []string) int {
	requested = normalizePortfolioTags(requested)
	videoTags = normalizePortfolioTags(videoTags)
	score := 0
	for _, tag := range requested {
		for _, videoTag := range videoTags {
			if tag == videoTag {
				score++
				break
			}
		}
	}
	return score
}

func categoryTagMatched(tags []string, category string) bool {
	category = strings.TrimSpace(category)
	for _, tag := range normalizePortfolioTags(tags) {
		if tag == category {
			return true
		}
		for _, categoryTag := range aiWorkCategoryTags[category] {
			if tag == categoryTag {
				return true
			}
		}
	}
	return false
}

func sortedAIWorkCategories() []string {
	categories := make([]string, 0, len(aiWorksByCategory))
	for category := range aiWorksByCategory {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	return categories
}

func normalizeAIWorkVideoPath(value string) string {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
	clean = strings.TrimPrefix(clean, "./")
	if !strings.HasPrefix(clean, AIWorksDir+"/") {
		return ""
	}
	parts := strings.Split(clean, "/")
	if len(parts) != 3 {
		return ""
	}
	category := strings.TrimSpace(parts[1])
	fileName := strings.TrimSpace(filepath.Base(parts[2]))
	if category == "" || fileName == "" || !strings.HasSuffix(strings.ToLower(fileName), ".mp4") {
		return ""
	}
	for _, allowed := range aiWorksByCategory[category] {
		if fileName == allowed {
			return aiWorkPath(category, fileName)
		}
	}
	return ""
}

func aiWorkCaption(video AIWorkVideo, language string, exact bool) string {
	label := aiWorkCategoryLabel(video.Category, language)
	switch normalizeLanguageCode(language) {
	case "kk":
		if exact {
			return "Мынау сіздің бағытыңызға жақын мысал: " + label + "."
		}
		return "Дәл сондай кейс жоқ, бірақ бағытыңызға жақын мысал: " + label + "."
	case "en":
		if exact {
			return "Relevant example for your direction: " + label + "."
		}
		return "No exact case yet, but this is the closest related example: " + label + "."
	default:
		if exact {
			return "Подходящий пример под вашу задачу: " + label + "."
		}
		return "Точного кейса нет, отправляю ближайший по смыслу пример: " + label + "."
	}
}

func aiWorkCategoryLabel(category string, language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		switch category {
		case "real_estate":
			return "жылжымайтын мүлік / жер"
		case "tourism":
			return "туризм"
		case "auto":
			return "авто"
		case "fashion":
			return "киім / fashion"
		case "horeca":
			return "тамақ / ресторан"
		default:
			return category
		}
	case "en":
		switch category {
		case "real_estate":
			return "real estate / land"
		case "horeca":
			return "food / restaurant"
		default:
			return strings.ReplaceAll(category, "_", " ")
		}
	default:
		switch category {
		case "real_estate":
			return "недвижимость / земля"
		case "tourism":
			return "туризм"
		case "auto":
			return "авто"
		case "fashion":
			return "одежда / fashion"
		case "horeca":
			return "еда / ресторан"
		default:
			return strings.ReplaceAll(category, "_", " ")
		}
	}
}
