package bot

import (
	"path/filepath"
	"sort"
	"strings"
)

type PortfolioCase struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Description     string   `json:"description,omitempty"`
	Niches          []string `json:"niches,omitempty"`
	Products        []string `json:"products,omitempty"`
	Goals           []string `json:"goals,omitempty"`
	Audiences       []string `json:"audiences,omitempty"`
	Formats         []string `json:"formats,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	VideoPath       string   `json:"video_path"`
	CaptionTemplate string   `json:"caption_template,omitempty"`
	IsActive        bool     `json:"is_active"`
}

func portfolioCaseCatalogue() []PortfolioCase {
	cases := make([]PortfolioCase, 0, 64)
	for _, category := range sortedAIWorkCategories() {
		for _, fileName := range aiWorksByCategory[category] {
			video := aiWorkVideo(category, fileName)
			cases = append(cases, PortfolioCase{
				ID:        portfolioCaseID(category, fileName),
				Title:     portfolioCaseTitle(category, fileName),
				Niches:    portfolioCaseNiches(category, video.Tags),
				Products:  portfolioCaseProducts(video.Tags),
				Goals:     portfolioCaseGoals(video.Tags),
				Formats:   []string{"ai_ad_video"},
				Tags:      video.Tags,
				VideoPath: video.Path,
				IsActive:  true,
			})
		}
	}
	sort.SliceStable(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases
}

func portfolioCaseID(category string, fileName string) string {
	base := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	base = strings.ReplaceAll(base, " ", "_")
	return strings.TrimSpace(category) + "/" + base
}

func portfolioCaseIDByVideoPath(videoPath string) string {
	videoPath = normalizeAIWorkVideoPath(videoPath)
	if videoPath == "" {
		return ""
	}
	parts := strings.Split(videoPath, "/")
	if len(parts) != 3 {
		return ""
	}
	return portfolioCaseID(parts[1], parts[2])
}

func portfolioCaseTitle(category string, fileName string) string {
	base := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	base = strings.ReplaceAll(base, "_", " ")
	base = strings.Join(strings.Fields(base), " ")
	if base == "" {
		base = strings.ReplaceAll(category, "_", " ")
	}
	return strings.TrimSpace(strings.ReplaceAll(category, "_", " ") + ": " + base)
}

func portfolioCaseNiches(category string, tags []string) []string {
	values := append([]string{category}, tags...)
	return normalizePortfolioTags(values)
}

func portfolioCaseProducts(tags []string) []string {
	products := make([]string, 0, 4)
	for _, tag := range normalizePortfolioTags(tags) {
		switch tag {
		case "product", "fmcg", "grocery", "dairy", "fashion", "auto", "furniture", "chemicals", "equipment":
			products = append(products, tag)
		}
	}
	return normalizePortfolioTags(products)
}

func portfolioCaseGoals(tags []string) []string {
	goals := make([]string, 0, 3)
	for _, tag := range normalizePortfolioTags(tags) {
		switch tag {
		case "sales", "lead_generation", "awareness":
			goals = append(goals, tag)
		}
	}
	if len(goals) == 0 {
		goals = append(goals, "advertising")
	}
	return normalizePortfolioTags(goals)
}

func missingPortfolioCaseDescriptions() []string {
	var missing []string
	for _, item := range portfolioCaseCatalogue() {
		if strings.TrimSpace(item.Description) == "" {
			missing = append(missing, item.ID)
		}
	}
	return missing
}
