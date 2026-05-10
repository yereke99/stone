# Stone Production WhatsApp AI Sales Manager

Premium Go backend template for an AI sales manager chatbot prepared for the official Meta WhatsApp Cloud API.

## Business Rules

- Offer: AI advertising videos in 48 hours without filming.
- Ready for ad launch.
- Prices: 35,000 KZT, 50,000 KZT, 75,000 KZT.
- Languages: RU, KZ, EN with Russian fallback.
- Every bot message is validated to stay between 5 and 35 words.
- The bot uses only configured portfolio links and the business context above.

## Endpoints

- `GET /health` - health check.
- `GET /webhook` - Meta webhook verification.
- `POST /webhook` - Meta webhook receiver.

## Configuration

Copy `.env.example` to `.env` and fill values:

```bash
cp .env.example .env
```

Required runtime variables:

- `APP_PORT`
- `ENV`
- `META_API_BASE_URL`
- `META_WEBHOOK_VERIFY_TOKEN`

Meta credentials are intentionally empty in the example file. Set these when Meta provides real values:

- `META_ACCESS_TOKEN`
- `META_PHONE_NUMBER_ID`
- `META_BUSINESS_ACCOUNT_ID`
- `META_APP_SECRET`

Portfolio URLs are configurable:

- `PORTFOLIO_TEST_URL`
- `PORTFOLIO_BASIC_URL`
- `PORTFOLIO_STANDARD_URL`

## Local Run

```bash
export $(grep -v '^#' .env | xargs)
go run ./cmd
```

## Docker

```bash
docker compose up --build
```

## Meta Webhook Setup

Use this callback URL:

```text
https://your-domain.com/webhook
```

Use the exact value from `META_WEBHOOK_VERIFY_TOKEN` as the Meta verify token.

If `META_APP_SECRET` is configured, incoming webhook requests must include a valid `X-Hub-Signature-256` header.

## Architecture

- `internal/bot` - conversation flow, language detection, message policy.
- `internal/meta` - WhatsApp Cloud API client and webhook payload types.
- `internal/storage` - in-memory state store, replaceable with PostgreSQL or Redis.
- `internal/http` - router and HTTP middleware.
- `internal/config` - environment-based configuration.
- `internal/logger` - structured Zap logging.
