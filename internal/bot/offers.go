package bot

import "fmt"

const (
	VideoLevel1 = "video_level_1.mp4"
	VideoLevel2 = "video_level_2.mp4"
	VideoLevel3 = "video_level_3.mp4"
	VideoLevel4 = "video_level_4.mp4"
)

var ExpectedVideoFiles = []string{
	VideoLevel1,
	VideoLevel2,
	VideoLevel3,
	VideoLevel4,
}

const (
	PackageNameTest     = "Test"
	PackageNameBasic    = "Basic"
	PackageNameStandard = "Standard"
)

type Offer struct {
	Level         int
	Name          string
	TitleRU       string
	DescriptionRU string
	TitleKK       string
	DescriptionKK string
	FileName      string
}

var offers = map[int]Offer{
	1: {
		Level:   1,
		Name:    PackageNameTest,
		TitleRU: "Тестовый формат",
		DescriptionRU: `Подходит, если хотите быстро проверить, зайдет ли формат.

— 1 ИИ-генерация сцены спикера 🤖
— перебивочные кадры со стороны клиента 🎬
— монтаж под ваш формат
— срок сдачи 2 раб. дня

Это формат для теста: чтобы оценить качество и понять, есть ли смысл масштабировать в серию 📈

💰 35 000 тг`,
		TitleKK: "Тестілік формат",
		DescriptionKK: `Форматтың қаншалықты тиімді екенін тез тексеруге ыңғайлы.

— спикер сахнасының 1 ИИ-генерациясы 🤖
— клиент тарапынан қосымша кадрлар 🎬
— сіздің форматыңызға сай монтаж
— дайын болу мерзімі: 2 жұмыс күні

Бұл сынаққа арналған формат: сапаны бағалап, серияға масштабтаудың мәні бар-жоғын түсінуге көмектеседі 📈

💰 35 000 ₸`,
		FileName: VideoLevel1,
	},
	2: {
		Level:   2,
		Name:    PackageNameBasic,
		TitleRU: "Базовый формат",
		DescriptionRU: `Подходит, если уже есть понимание и нужна более динамичная подача.

— Полностью ИИ-сцены (входит до 5-6 генерации) 🎥
— структурный сценарий 🧠
— более проработанный монтаж и подача
— срок сдачи до 3 раб. дней

Ролик уже выглядит как полноценный рекламный креатив и лучше удерживает внимание 👀

💰 50 000 тг`,
		TitleKK: "Базалық формат",
		DescriptionKK: `Егер бағыт түсінікті болып, динамикалық әрі толыққанды жарнамалық ролик керек болса, осы формат ыңғайлы.

— толық ИИ-сахналар (5-6 генерацияға дейін) 🎥
— құрылымды сценарий 🧠
— тереңірек монтаж және ұсыну
— дайын болу мерзімі: 3 жұмыс күніне дейін

Ролик толыққанды жарнамалық креатив сияқты көрінеді және назарды жақсы ұстайды 👀

💰 50 000 ₸`,
		FileName: VideoLevel2,
	},
	3: {
		Level:   3,
		Name:    PackageNameStandard,
		TitleRU: "Стандарт (премиум подача)",
		DescriptionRU: `Подходит, если нужен сильный ролик под рекламу и масштабирование.

— сцены с продуманной драматургией (до 9 генерации) 🎭
— визуал уровня рекламы ✨
— детальный монтаж, ритм и акценты 🎞️
— срок сдачи до 3 раб. дней

Такой формат можно масштабировать в серию и использовать как системный инструмент для привлечения клиентов 🚀

💰 от 75 000 тг. Зависит от количества сцен, просчитывается индивидуально.`,
		TitleKK: "Стандарт (премиум ұсыну)",
		DescriptionKK: `Жарнамаға және масштабтауға арналған мықты ролик керек болса, осы формат дұрыс.

— ойластырылған драматургиясы бар сахналар (9 генерацияға дейін) 🎭
— жарнама деңгейіндегі визуал ✨
— детальді монтаж, ритм және акценттер 🎞️
— дайын болу мерзімі: 3 жұмыс күніне дейін

Бұл форматты серияға масштабтап, клиент тартудың жүйелі құралы ретінде қолдануға болады 🚀

💰 75 000 ₸ден бастап. Сахна санына байланысты жеке есептеледі.`,
		FileName: VideoLevel3,
	},
}

func OfferByLevel(level int) (Offer, bool) {
	offer, ok := offers[level]
	return offer, ok
}

func OfferByVideo(fileName string) (Offer, bool) {
	for _, offer := range offers {
		if offer.FileName == fileName {
			return offer, true
		}
	}
	return Offer{}, false
}

func OfferCaptionByVideo(fileName string, language string) string {
	offer, ok := OfferByVideo(fileName)
	if !ok {
		return "Stone production"
	}
	return offer.Caption(language)
}

func (o Offer) Caption(language string) string {
	if language == "kk" {
		return fmt.Sprintf("%s\n\n%s", o.TitleKK, o.DescriptionKK)
	}
	return fmt.Sprintf("%s\n\n%s", o.TitleRU, o.DescriptionRU)
}
