# Stone Production WhatsApp Sales Bot

Go-бот для WhatsApp на базе GreenAPI long polling. Бот консультирует клиентов Stone production по ИИ-рекламным роликам, показывает три формата портфолио и закрывает на короткий бриф.

## Возможности

- Принимает входящие сообщения через `receiveNotification`.
- Удаляет обработанные уведомления через `deleteNotification`.
- Ведёт диалог по сохранённой state machine и не начинает воронку заново после рестарта.
- Обрабатывает частые запросы локально: цена, портфолио, форматы, анкета, возражение.
- Отправляет видео из локальной папки `./video` через GreenAPI `sendFileByUpload`.
- Хранит состояние клиентов, сообщения и дедупликацию в SQLite.

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
DATABASE_PATH=./data/stone.sqlite3
PORTFOLIO_VIDEO_DIR=./video
PORTFOLIO_TEST_URL=
PORTFOLIO_BASIC_URL=
PORTFOLIO_STANDARD_URL=
RECEIVE_TIMEOUT_SECONDS=60
HTTP_CLIENT_TIMEOUT_SECONDS=75
BOT_REPLY_LANGUAGE_MODE=auto
BOT_AUTO_REPLY_ENABLED=false
BOT_MAX_MESSAGE_AGE_SECONDS=120
HISTORY_GUARD_ENABLED=true
HISTORY_GUARD_LOOKBACK_COUNT=10
HISTORY_GUARD_TIMEOUT_SECONDS=8
HISTORY_GUARD_FAIL_CLOSED=true
HISTORY_GUARD_AI_ENABLED=false
HISTORY_GUARD_AI_MESSAGE_LIMIT=3
HISTORY_GUARD_AI_MAX_CHARS_PER_MESSAGE=400
HISTORY_GUARD_AI_MAX_TOTAL_CHARS=1200
NEW_LEAD_AUTO_PACKAGES_AFTER_MINUTES=15
NEW_LEAD_AUTO_PACKAGES_ENABLED=true
MAX_OPENAI_OUTPUT_TOKENS=350
ANALYZER_MAX_OUTPUT_TOKENS=1500
BOT_LLM_PRIMARY_REPLY_ENABLED=true
BOT_LLM_REPLY_ENABLED=true
BOT_LLM_REPLY_DRY_RUN=false
LLM_REPLY_MAX_OUTPUT_TOKENS=1000
AUDIO_TRANSCRIPTION_ENABLED=true
FFMPEG_PATH=/usr/bin/ffmpeg
OPENAI_TRANSCRIPTION_MODEL=gpt-4o-mini-transcribe
AUDIO_MAX_DOWNLOAD_MB=25
AUDIO_DOWNLOAD_TIMEOUT_SECONDS=20
AUDIO_CONVERT_TIMEOUT_SECONDS=30
AUDIO_TRANSCRIPTION_TIMEOUT_SECONDS=60
AI_WORK_EXAMPLES_LIMIT=3
OWNER_WA_CHAT_ID=
ADMIN_CHAT_IDS=77000000000@c.us
APP_ENV=local
```

`BOT_AUTO_REPLY_ENABLED=false` держит бота в безопасном режиме: приложение не начнёт polling и не отправит сообщения клиентам. Для боевого запуска явно поставьте `BOT_AUTO_REPLY_ENABLED=true`. `BOT_MAX_MESSAGE_AGE_SECONDS` защищает от старой очереди GreenAPI: уведомления старше этого возраста будут удалены без ответа.

`DATABASE_PATH` задаёт SQLite-файл для состояния клиентов и журналов сообщений. На старте приложение только открывает хранилище, применяет миграции и начинает принимать новые GreenAPI notifications; оно не обходит контакты WhatsApp и не отправляет скрипт старым чатам.

`OWNER_WA_CHAT_ID` и `ADMIN_CHAT_IDS` опциональны. Укажите один или несколько WhatsApp chatID через запятую, например `77000000000@c.us`. Когда клиент соглашается на анкету или просит менеджера, бот один раз отправит менеджеру резюме лида.

`BOT_LLM_PRIMARY_REPLY_ENABLED=true` (значение по умолчанию) делает ответ OpenAI-анализатора основным ответом клиенту в обычном диалоге: модель получает последние 10 сообщений, сохранённые факты лида и официальные цены, а backend проверяет ответ, выполняет действия (подбор и отправку локальных примеров, цены, handoff) и хранит журнал. `BOT_LLM_PRIMARY_REPLY_ENABLED=false` мгновенно возвращает диалог на детерминированные шаблоны без отката кода; они же остаются рабочим fallback при недоступности OpenAI. Брифы, выбор пакета, STOP, suppression и handoff всегда остаются детерминированными.

`BOT_LLM_REPLY_ENABLED=false` мгновенно возвращает финальные ответы к backend-шаблонам без отката кода. `BOT_LLM_REPLY_DRY_RUN=true` вызывает OpenAI и пишет кандидат в логи, но клиенту отправляется backend-шаблон.

Для голосовых WhatsApp сообщений нужен FFmpeg на сервере, не Docker:

```bash
sudo apt update
sudo apt install -y ffmpeg
which ffmpeg
ffmpeg -version
```

Ожидаемый путь для production env: `/usr/bin/ffmpeg`. Если нужно отключить распознавание аудио без отката кода, поставьте `AUDIO_TRANSCRIPTION_ENABLED=false` и перезапустите сервис.

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

## Production deploy

Проект запускается как systemd binary, не Docker:

```bash
cd /home/stone && git pull --ff-only && go build -o /home/stone/bin/stone-bot ./cmd && sudo systemctl restart stone && sudo systemctl status stone --no-pager -l
```

Логи:

```bash
journalctl -u stone -n 200 --no-pager
journalctl -u stone -f
```

## Проверка GreenAPI

1. Убедитесь, что инстанс GreenAPI авторизован в WhatsApp.
2. Проверьте, что `GREEN_API_URL`, `GREEN_MEDIA_API_URL`, `GREEN_ID_INSTANCE` и `GREEN_API_TOKEN` заполнены.
3. Включите входящие уведомления для инстанса: `incomingWebhook=yes`.
4. Для ручной остановки бота из WhatsApp включите уведомления о сообщениях, отправленных с телефона: `outgoingMessageWebhook=yes`.
   Это нужно именно для `outgoingMessageReceived`; `outgoingAPIMessageReceived` относится к сообщениям, отправленным через API, и бот не использует его для ручного `stop`.
5. Запустите бота и отправьте текстовое сообщение на подключенный WhatsApp с другого номера.
6. В логах не должно быть регулярных `receiveNotification failed`.

Настроить входящие уведомления можно через кабинет GreenAPI или API:

```bash
set -a
source .env
set +a

curl -sS -X POST \
  -H "Content-Type: application/json" \
  -d '{"incomingWebhook":"yes","outgoingMessageWebhook":"yes"}' \
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
- `internal/bot` — sales service, офферы, prompt, SQLite-backed conversation store.
- `internal/logger` — structured zap logger.
