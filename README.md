# Stone Production WhatsApp Sales Bot

Go-бот для WhatsApp на базе GreenAPI long polling. Бот консультирует клиентов Stone production по ИИ-рекламным роликам, показывает три формата портфолио и закрывает на короткий бриф.

## Возможности

- Принимает входящие сообщения через `receiveNotification`.
- Удаляет обработанные уведомления через `deleteNotification`.
- Отвечает через OpenAI Responses API строго в JSON-формате.
- Обрабатывает частые запросы локально: цена, портфолио, форматы, анкета, возражение.
- Отправляет видео из локальной папки `./video` через GreenAPI `sendFileByUpload`.
- Хранит последние сообщения и дедупликацию `idMessage` в памяти.

## Настройка env

Создайте `.env` из примера:

```bash
cp .env.example .env
```

Заполните значения:

```env
GREEN_API_URL=https://7107.api.greenapi.com
GREEN_MEDIA_API_URL=https://media.green-api.com
GREEN_ID_INSTANCE=your_instance_id
GREEN_API_TOKEN=your_green_api_token
OPENAI_API_KEY=your_openai_api_key
OPENAI_MODEL=gpt-5.5
OPENAI_TEMPERATURE=0.3
PORTFOLIO_VIDEO_DIR=./video
PORTFOLIO_TEST_URL=
PORTFOLIO_BASIC_URL=
PORTFOLIO_STANDARD_URL=
RECEIVE_TIMEOUT_SECONDS=60
HTTP_CLIENT_TIMEOUT_SECONDS=75
BOT_REPLY_LANGUAGE_MODE=auto
BOT_AUTO_REPLY_ENABLED=false
BOT_MAX_MESSAGE_AGE_SECONDS=120
MAX_OPENAI_OUTPUT_TOKENS=350
ADMIN_CHAT_IDS=77000000000@c.us
APP_ENV=local
```

`BOT_AUTO_REPLY_ENABLED=false` держит бота в безопасном режиме: приложение не начнёт polling и не отправит сообщения клиентам. Для боевого запуска явно поставьте `BOT_AUTO_REPLY_ENABLED=true`. `BOT_MAX_MESSAGE_AGE_SECONDS` защищает от старой очереди GreenAPI: уведомления старше этого возраста будут удалены без ответа.

`ADMIN_CHAT_IDS` опционален. Укажите один или несколько WhatsApp chatID через запятую, например `77000000000@c.us`. Когда бот соберёт бриф и переведёт лида в `handoff_required`, он один раз отправит менеджеру текстовое резюме заявки.

## Видео

Положите файлы в папку `./video`:

```text
video/video_level_1.mp4
video/video_level_2.mp4
video/video_level_3.mp4
```

Если файл отсутствует, приложение продолжит работать, покажет warning в логах и отправит клиенту текстовый fallback.

## Локальный запуск

```bash
set -a
source .env
set +a

go run ./cmd
```

Проверка сборки:

```bash
go build ./...
```

## Проверка GreenAPI

1. Убедитесь, что инстанс GreenAPI авторизован в WhatsApp.
2. Проверьте, что `GREEN_API_URL`, `GREEN_MEDIA_API_URL`, `GREEN_ID_INSTANCE` и `GREEN_API_TOKEN` заполнены.
3. Включите входящие уведомления для инстанса: `incomingWebhook=yes`.
4. Запустите бота и отправьте текстовое сообщение на подключенный WhatsApp с другого номера.
5. В логах не должно быть регулярных `receiveNotification failed`.

Настроить входящие уведомления можно через кабинет GreenAPI или API:

```bash
set -a
source .env
set +a

curl -sS -X POST \
  -H "Content-Type: application/json" \
  -d '{"incomingWebhook":"yes"}' \
  "$GREEN_API_URL/waInstance$GREEN_ID_INSTANCE/setSettings/$GREEN_API_TOKEN"
```

Бот удаляет входящие уведомления после успешной обработки или безопасного skip. Если отправка ответа не удалась, receipt не удаляется, чтобы GreenAPI мог повторить доставку.

## Если видео не отправляется

- Проверьте, что `PORTFOLIO_VIDEO_DIR=./video`.
- Проверьте имена файлов: `video_level_1.mp4`, `video_level_2.mp4`, `video_level_3.mp4`.
- Убедитесь, что GreenAPI media endpoint доступен: `GREEN_MEDIA_API_URL=https://media.green-api.com`.
- Посмотрите warning в логах: там будет только имя файла и ошибка, без токенов и приватных данных клиента.

## Если OpenAI не отвечает

- Проверьте `OPENAI_API_KEY`, `OPENAI_MODEL` и сетевой доступ.
- Увеличьте `HTTP_CLIENT_TIMEOUT_SECONDS`, если запросы регулярно не успевают завершиться.
- При временной ошибке OpenAI бот отправит короткий fallback и удалит notification, чтобы не зациклить очередь.

## Архитектура

- `cmd/main.go` — long polling loop, graceful shutdown, backoff, дедупликация.
- `internal/config` — env-конфигурация.
- `internal/greenapi` — receive/delete/send GreenAPI client.
- `internal/openai` — OpenAI Responses API client.
- `internal/bot` — sales service, офферы, prompt, in-memory conversation store.
- `internal/logger` — structured zap logger.
