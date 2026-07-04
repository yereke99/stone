# Аудит WhatsApp-бота Stone Production

## Статус после исправлений 2026-07-04

Ниже остался исходный аудит как история root cause. После исправлений основные причины закрыты:

- Живой путь подтверждён: GreenAPI polling -> `Service.ProcessIncomingWhatsAppMessage` -> understanding -> state/action validation -> GreenAPI send.
- Добавлен paired context для OpenAI: текущее сообщение, quoted-контекст, последние сообщения, pending questions, question-answer pairs, known state и official packages.
- Strict schema и analyzer prompt приведены к одному контракту: новые intent'ы, `extracted_fields`, `answered_questions`, `missing_fields`, `reply_text`, `next_action`, `needs_human`, `confidence`.
- LLM reply layer подключён за флагами `BOT_LLM_REPLY_ENABLED`, `BOT_LLM_REPLY_DRY_RUN`, `BOT_LLM_REPLY_TIMEOUT`, `BOT_LLM_REPLY_MODEL`, `BOT_LLM_REPLY_MAX_TOKENS`. При ошибке используется безопасный state-machine fallback.
- Добавлен hard gate перед отправкой ответа: не задавать уже закрытые вопросы по нише, цели, аудитории, ссылкам, количеству, voice/style и понравившимся форматам; небезопасные скидки/celebrity-обещания заменяются безопасным текстом.
- URL-only Instagram/Reels/TikTok/YouTube больше не превращаются в `goal`; ссылки сохраняются как business/reference links.
- `пример` больше не матчится внутри `примерно`; вопросы "можете сделать похоже?" идут в `feasibility_question`.
- Длинные multiline/paragraph/customer-rich ответы лучше извлекают product/service, niche, target_audience, campaign context, hook idea, quantity и goal без повторения уже известных вопросов.
- Активные бизнес-сообщения больше не проваливаются в тихое молчание: добавлены ветки confusion, feasibility, niche-specific case request, format preference, voice question, copyright question.
- Voice/copyright ответы безопасны: без обещания клонировать голос/образ Вин Дизеля или других публичных людей без прав.
- Группы, suppression, STOP/admin takeover, dedupe и media-sending guard сохранены; LLM не вызывается для suppressed/group сообщений.

Проверка после фикса:

```bash
go test ./...
go build ./...
```

Оба запуска зелёные.

Дата: 2026-07-04. Ветка: `main` (8f3a1fd). Все выводы проверены чтением кода и эмпирической трассировкой реальных сообщений из скриншотов через `AnalyzeCustomerMessage` (временный тест, удалён после проверки). `go build ./...` и `go test ./...` — зелёные.

---

## 0. Главный вывод (TL;DR)

**Бот НЕ генерирует ответы через LLM.** Все ответы клиенту — жёсткие шаблонные строки, выбираемые rule-based state machine. OpenAI используется только как *анализатор* входящего сообщения (JSON-экстракция полей и intent), и даже этот анализатор:

1. молча падает в детерминированный fallback при любой ошибке/таймауте (8 сек);
2. работает по strict JSON-схеме, которая **противоречит собственному системному промпту** (промпт требует интенты и поля, которые схема запрещает);
3. его результат не влияет на *текст* ответа — только на выбор ветки из ~40 захардкоженных шаблонов.

Красивый «человеческий» системный промпт `SystemPrompt` в [prompts.go:5](internal/bot/prompts.go#L5) («Ты — премиальный WhatsApp-менеджер…») — **мёртвый код**: метод `GenerateSalesReply` определён в интерфейсе и клиенте, но не вызывается нигде в живом пути. Это и есть корень «бот отвечает как анкета, а не как продажник».

Все три бага со скриншотов воспроизводятся детерминированно (см. раздел 4).

---

## 1. Карта архитектуры

### 1.1. Компоненты

```
GreenAPI (long polling, НЕ webhook)
        │ receiveNotification / deleteNotification
        ▼
cmd/main.go ──────────────── runPolling → processNotification
        │                       фильтры: направление, возраст, группы,
        │                       suppression-список, dedupe
        ▼
internal/bot/Service.ProcessIncomingWhatsAppMessage   ← ЕДИНАЯ точка обработки
        │
        ├─ understandCustomerMessage (understanding.go)
        │    ├─ AnalyzeCustomerMessage — детерминированные regex/keyword-правила (analysis.go)
        │    └─ openai.AnalyzeCustomerMessage — LLM-анализатор JSON (только анализ, НЕ ответ)
        │
        ├─ handleSalesState (service.go:354) — rule-based state machine
        │    выбирает один из ~40 шаблонов из prompts.go / dialogue.go / approved_flow.go
        │
        ├─ sendAndRemember → GreenAPI sendMessage
        ├─ sendVideosWithCaptions → GreenAPI sendFileByUpload (video/*.mp4)
        └─ ConversationStore (store.go + sqlite_store.go) — SQLite + in-memory кэш
```

### 1.2. Файлы и роли

| Файл | Роль |
|---|---|
| [cmd/main.go](cmd/main.go) | polling GreenAPI, фильтры входящих, dedupe, ручной STOP от админа |
| [internal/bot/service.go](internal/bot/service.go) | ядро: `ProcessIncomingWhatsAppMessage`, `handleSalesState`, отправка текста/видео, уведомления админам |
| [internal/bot/understanding.go](internal/bot/understanding.go) | вызов OpenAI-анализатора, merge с fallback, системный промпт анализатора |
| [internal/bot/analysis.go](internal/bot/analysis.go) | детерминированный анализатор: `AnalyzeCustomerMessage`, экстракторы niche/goal/deadline/links, `LeadState.ApplyAnalysis` |
| [internal/bot/dialogue.go](internal/bot/dialogue.go), [prompts.go](internal/bot/prompts.go), [approved_flow.go](internal/bot/approved_flow.go), [flow.go](internal/bot/flow.go) | шаблоны всех ответов (RU/KK/EN), FAQ-ответы, `detectFAQIntent` |
| [internal/bot/qualification.go](internal/bot/qualification.go) | валидаторы полей (`isValidNiche`, `isValidGoal`), missing fields, sanitize |
| [internal/bot/active_flow.go](internal/bot/active_flow.go) | FAQ-ветка, бриф-ветка (`handleBriefRequested`, `recordBriefMessage`) |
| [internal/bot/store.go](internal/bot/store.go), [sqlite_store.go](internal/bot/sqlite_store.go) | `Conversation` (весь state чата), SQLite-персистентность, dedupe, лимит истории 10 сообщений |
| [internal/bot/state.go](internal/bot/state.go) | константы стадий и статусов лида |
| [internal/bot/offers.go](internal/bot/offers.go) | 3 пакета (Test/Basic/Standard) + видео `video_level_1..4.mp4`, подписи |
| [internal/bot/reply_package.go](internal/bot/reply_package.go) | определение пакета из quoted-reply клиента |
| [internal/bot/followup_actions.go](internal/bot/followup_actions.go), [delayed_packages.go](internal/bot/delayed_packages.go) | отложенные фоллоу-апы: пакеты через 15 мин, анкета через 1 ч, напоминание 24 ч, скидка 7 дн |
| [internal/bot/history_guard.go](internal/bot/history_guard.go) | защита от автостарта воронки в старых чатах (**не подключена**, см. F5) |
| [internal/bot/manual_stop.go](internal/bot/manual_stop.go), [suppression.go](internal/bot/suppression.go), [whatsapp_safety.go](internal/bot/whatsapp_safety.go), [safety.go](internal/bot/safety.go) | STOP-команда админа, suppression-список в SQLite, запрет групп |
| [internal/openai/client.go](internal/openai/client.go) | OpenAI Responses API: `AnalyzeCustomerMessage` (используется), `GenerateSalesReply` (**мёртвый**), `ClassifyHistoryGuard` (не подключён) |
| [internal/greenapi/client.go](internal/greenapi/client.go), [types.go](internal/greenapi/types.go) | GreenAPI клиент, парсинг notification (text/extended/quoted/media) |
| [internal/http/*](internal/http/), [internal/meta/*](internal/meta/), [internal/bot/manager.go](internal/bot/manager.go), [internal/storage/memory.go](internal/storage/memory.go) | **легаси Meta Cloud API webhook-путь, в `main.go` не запускается** (HTTP-сервер не стартует) |

### 1.3. БД (SQLite, [sqlite_store.go](internal/bot/sqlite_store.go))

- `whatsapp_clients` — весь `Conversation` (state, lead, флаги, followup) одной строкой на чат;
- `whatsapp_messages` — журнал входящих/исходящих + dedupe-ключи;
- `whatsapp_automation_suppression` — чёрный список телефонов/чатов.

### 1.4. Модель состояния чата (`Conversation`, [store.go:43](internal/bot/store.go#L43))

Стадии: `neutral_new → awaiting_qualification → packages_presented → awaiting_questionnaire_confirmation → brief_requested → handed_off` (+ `stopped`, `opt_out`, легаси-стадии). Лид (`LeadState`, [analysis.go:47](internal/bot/analysis.go#L47)): niche, goal, deadline, platform(s), city, budget, product_or_service, strong_side, target_audience, offer, website/reference_links, selected_package, brief-флаги, lead_status. Плюс `AskedFields` / `CompletedFields` / `SentVideos` / `SentVideoFiles` — защита от повторов.

---

## 2. Полный flow-мануал (входящее сообщение → ответ)

1. **Polling** ([cmd/main.go:117](cmd/main.go#L117)): `ReceiveNotification` (long-poll 60 с) → `processNotification` → в конце `DeleteNotification`.
2. **Направление**: `outgoingMessageReceived` (ручное сообщение с телефона владельца) → журнал + проверка STOP-команды (`стоп`/`stop`, [manual_stop.go:28](internal/bot/manual_stop.go#L28)) → `MarkManualStop` (бот замолкает в этом чате). `outgoingAPIMessageReceived` — игнор.
3. **Фильтры входящего** (`shouldProcessNotification`, [cmd/main.go:330](cmd/main.go#L330)): авто-ответ включён; webhook `incomingMessageReceived`; timestamp свежий (≤120 c и не старше старта − 2 мин); не группа `@g.us`; тип text/extended/quoted/media; непустой chatID/текст.
4. **Suppression** ([suppression.go](internal/bot/suppression.go)): телефон/чат в чёрном списке → тихий skip.
5. **Dedupe** (`BeginIncomingMessageProcessing`): по `chatID|idMessage` (или sha256-fallback), защита от повторов и параллельной обработки.
6. **`ProcessIncomingWhatsAppMessage`** ([service.go:93](internal/bot/service.go#L93)): лок на чат; язык (auto ru/kk/en); медиа без текста → `NonTextFallbackText` («напишите текстом»); quoted-reply → `detectPackageFromReplyContext` (выбор пакета ответом на видео).
7. **Сохранение**: `AppendMessage` (история, лимит 10), `MarkIncoming` (`LastIncomingText`).
8. **Guard-ы**: `isConversationClosedForAutomation` (handed_off/stopped/opt_out → молчание); history guard (**фактически выключен**, см. F5).
9. **Анализ** (`understandCustomerMessage`, [understanding.go:135](internal/bot/understanding.go#L135)):
   - всегда сначала детерминированный `AnalyzeCustomerMessage` (fallback);
   - затем OpenAI-анализатор с таймаутом 8 с; payload = текст + quoted-контекст + полный `conversation_state` JSON + последний вопрос бота + missing fields. **История диалога в payload входит** (recent_messages из последних 10);
   - при ошибке/невалидном ответе — **молча** используется fallback (только warn-лог);
   - merge: AI-поля перекрывают fallback, intent AI побеждает, если не `other`.
10. **Пост-обработка intent'а**: локальный `detectFAQIntent` может перезаписать intent на FAQ; quoted-пакет форсирует `package_selection`; бриф-эвристики.
11. **Фильтр релевантности** `isBusinessRelevantMessage` ([approved_flow.go:167](internal/bot/approved_flow.go#L167)): если intent = `other`, нет бизнес-сигнала, нет FAQ и текст не содержит ключевых слов — **сообщение молча игнорируется** (вообще без ответа). ⚠️ Источник бага №2.
12. **Merge лида**: `lead.ApplyAnalysis(analysis)` → `UpdateLead` (валидные значения не затираются мусором — тут защита хорошая).
13. **`handleSalesState`** ([service.go:354](internal/bot/service.go#L354)) — приоритетная цепочка: defer → opt-out → frustration → negative → closed-guard → human request → handoff → FAQ → food examples → more options → **cases request** → brief → format advice → business link → package selection → … → switch по стадии.
14. **Ответ**: `sendAndRemember` — анти-повтор (тот же текст ≤2 мин — skip), guard закрытых чатов, перед «Какой формат вам понравился?» принудительно досылаются видео 1–3 (`ensurePortfolioExamplesSentBeforeFormatQuestion`); отправка; журнал; `MarkAskedFields`; `UpdateState`; уведомление админам при handoff.
15. **Видео** (`sendVideosWithCaptions`): дедуп по `SentVideoFiles`, задержка 1.5 с между файлами, подпись = описание пакета с ценой, `MarkVideoSent`.
16. **Фоллоу-апы** ([delayed_packages.go](internal/bot/delayed_packages.go)): тикер 1 мин; после приветствия без ответа через 15 мин — авто-отправка 3 видео + «Какой формат вам понравился?»; далее анкета (1 ч), напоминание (24 ч), скидочный ролик `video_level_4` (7 дн). Любое входящее сообщение отменяет фоллоу-апы.

---

## 3. Аудит AI/CPS-роутинга (ответы на вопросы C)

| # | Вопрос | Ответ |
|---|---|---|
| C1 | Каждое ли сообщение уходит в LLM? | В **анализатор** — да (кроме пустых/медиа без текста). В **генератор ответа** — никогда: `GenerateSalesReply` не вызывается. |
| C2 | Есть ли ветки с hardcoded-ответами без AI? | **Все** ответы hardcoded. AI влияет только на выбор ветки. |
| C3 | Пропускают ли короткие/ссылки AI? | Нет, анализатор вызывается; но его результат часто перетирается локальными эвристиками (`detectFAQIntent`, quoted-package). |
| C4 | История в промпте? | Да, последние 10 сообщений в `conversation_state.recent_messages`. Но история обрезается до 10 и живёт 24 ч (`defaultMessageTTL`). |
| C5 | Последнее сообщение как есть? | Да, `incoming.text` без изменений. |
| C6 | State в промпте? | Да, полный JSON. |
| C7 | Старый кэш? | In-memory кэш синхронен с SQLite; проблtask нет. Но `Snapshot` вызывается до 6 раз за одно сообщение — состояние может «уехать» между шагами (мелкий риск гонки, есть chat-lock). |
| C8 | Устаревший промпт? | Да: `SystemPrompt` (генерация) — мёртвый; промпт анализатора противоречит схеме (см. F2). |
| C9 | Разные промпты для разных типов? | Один промпт анализатора для всего. |
| C10 | Ошибки AI молча глотаются? | **Да** — warn-лог + детерминированный fallback, который не умеет извлекать нишу из свободного текста. Это ключевой механизм деградации. |
| C11 | Retry/fallback вызывает повторные вопросы? | Да: fallback не находит нишу → `qualificationFollowupText` задаёт тот же вопрос. Анти-повтор действует только 2 минуты и только на дословно тот же текст. |
| C12 | Ответ не в тот чат/сообщение? | Не обнаружено; chat-lock и dedupe корректные. |
| C13 | Потеря контекста после видео? | Частично: после отправки видео стадия = `packages_presented`, и `handlePackagesPresented` по умолчанию снова шлёт «Какой формат вам понравился?» на любой неопознанный ответ. |

---

## 4. Root cause report — баги со скриншотов

Все три подтверждены прогоном реальных текстов через код (детерминированный слой; в проде добавляется фактор молчаливого падения OpenAI-анализатора).

### Баг 1а. Instagram-ссылка → «Понял, цель — контент для продвижения. Что продаёте / какая ниша?»

**Трассировка** (фактический вывод анализатора):
```
вход:  https://www.instagram.com/reel/DaV3Xfsl6kh/?igsh=...
итог:  goal="контент для продвижения", platforms=["Instagram"], intent=business_link, missing=[niche]
```

- **Причина A (главная):** `extractGoal` → `normalizeGoal` ([analysis.go:1525](internal/bot/analysis.go#L1525)) матчит подстроки `instagram`, `reels`, `контент` в **самом URL** и присваивает `goal = "контент для продвижения"`. Ссылка-референс становится «целью». Дальше `handleBusinessLink` ([service.go:2870](internal/bot/service.go#L2870)) видит missing=[niche] и отвечает `"Ссылку получил, спасибо. " + qualificationFollowupText` → ветка «goalKnown && !nicheKnown» ([service.go:2552](internal/bot/service.go#L2552)) = дословно текст со скриншота.
- **Причина B:** для reels-ссылки нет отдельной обработки «референс» — она неотличима от бизнес-ссылки, и бот немедленно продолжает анкету вместо естественного ответа про «можем сделать похожее».

### Баг 1б. «Делаете примерно такое видео» → снова вопрос про нишу

```
вход:  Делаете примерно такое видео
итог:  intent=portfolio_request, missing=[niche]
```

- **Причина:** `containsPortfolioRequest` ([manager.go:260](internal/bot/manager.go#L260)) ищет подстроку `"пример"`, которая содержится в слове **«примерно»**. Вопрос о выполнимости («можете сделать похожее?») классифицируется как запрос кейсов → `handleCasesRequest` ([service.go:2707](internal/bot/service.go#L2707)) → missing=[niche] → «Да, кейсы можем отправить прямо сюда… что продаёте / какая у вас ниша?». Интента «feasibility question» нет ни в детерминированном слое, ни в enum LLM-схемы.

### Баг 1в. Длинное сообщение про чай/носки/розыгрыш → «Понял, цель — рост продаж. Что продаёте / какая у вас ниша?»

```
вход:  Завтра откроем продажи на чай и на носки (отечественный бренд), будет разыгрывать
       квартиру 3 блогера. Для хук нужно примерно такое видео, где выходят на вышку...
итог:  niche=NULL, goal="рост продаж", deadline="завтра", intent=portfolio_request, missing=[niche]
```

- **Причина A:** детерминированный экстрактор **не умеет извлекать нишу из свободного длинного текста**: `shortProductOrNicheLine` требует ≤5 слов в строке, `nicheFromProductEnumeration` требует ≤3 слов на элемент перечисления, `knownNicheFromText` не знает «чай»/«носки». «Чай и носки (отечественный бренд)» теряется полностью.
- **Причина B:** OpenAI-анализатор, который мог бы извлечь нишу, в проде упал/таймаут (8 с) или вернул null — и это **молча** проглотилось ([understanding.go:148](internal/bot/understanding.go#L148)); дальше работал только слабый fallback.
- **Причина C:** снова «примерно» → `portfolio_request` → `handleCasesRequest` → тот же шаблон «Да, кейсы можем отправить прямо сюда. Понял, цель — рост продаж. Подскажите… ниша?» — дословно скриншот.
- **Побочный дефект:** `deadline="завтра"` — «завтра откроем продажи» сохранено как срок производства ролика.

### Баг 2а. «Чет суть не уловил» → нет внятного ответа

```
итог:  intent=other, isBusinessRelevantMessage(stage=packages_presented) = false
```

- **Причина:** непонимание/замешательство не имеет интента ни в одном слое (`isFrustrationComplaint` ищет только «читай внимательно»-паттерны). Intent=`other`, бизнес-ключевых слов нет → `isBusinessRelevantMessage` возвращает false → сообщение **молча игнорируется** ([service.go:302](internal/bot/service.go#L302), лог `"incoming message ignored because it is outside stone production flow"`). Клиент не получает ничего. Даже если бы фильтр пропустил — дефолт `handlePackagesPresented` повторил бы «Какой формат вам понравился?».

### Баг 2б. «С запчастями есть образцы?» → нет ответа / «Пример уже отправлял выше»

```
итог:  intent=other, business relevant = false  → молчание
```

- **Причина A:** слова **«образцы»** нет в `containsPortfolioRequest`, «запчасти» нет в `knownNicheFromText`, а `isNonNicheCandidateText` блокирует нишу из-за `"?"` в тексте. Fallback даёт intent=`other` → фильтр релевантности молча гасит сообщение.
- **Причина B:** если OpenAI-анализатор успел и вернул `asks_examples` → `IntentPortfolioRequest`, то в стадии `packages_presented` ветка [service.go:588](internal/bot/service.go#L588) отвечает `portfolioAlreadySentText` («Пример уже отправлял выше») — вопрос про запчасти всё равно проигнорирован, ниша «автозапчасти» не подтверждена.
- **Причина C (продуктовая):** нишевых кейсов физически нет — в `video/` только 4 ролика по *форматам* (test/basic/standard/discount). Бот не может отправить «кейс по запчастям» — надо либо честно говорить «пришлю ближайшие по формату», либо завести библиотеку кейсов по нишам.

### Баг 3. Прямые вопросы о цене

«Какая минимальная цена?» детектится корректно (`price_question`) и в большинстве стадий отвечается `PriceText`/`packagePriceText`. Но в стадии `awaiting_qualification` ([service.go:521](internal/bot/service.go#L521)) price-вопрос ведёт к `presentPortfolioAndPackages` (3 видео + «Какой формат понравился?») — цены есть только в подписях к видео, прямого текстового ответа «от 35 000 тг» нет. Слова «минимальная» в детекторах нет, но `сколько`/`цена` покрывают.

### Системные root cause (сводно)

| ID | Дефект | Где |
|---|---|---|
| F1 | Ответы никогда не генерируются LLM; `GenerateSalesReply` + `SystemPrompt` — мёртвый код | [service.go:58](internal/bot/service.go#L58), [prompts.go:5](internal/bot/prompts.go#L5) |
| F2 | Strict-схема анализатора противоречит промпту: промпт требует интенты `provide_link`, `provide_reference`, `frustration`, `stop`, `negative_reaction`, `choose_package`, `request_manager` и поля `strong_side`, `offer`, `budget`, `reference_links`, `city`, `confidence`-зависимые ветки — **схема их запрещает** (`additionalProperties:false`, enum из 13 интентов) | [openai/client.go:484](internal/openai/client.go#L484) vs [understanding.go:15](internal/bot/understanding.go#L15) |
| F3 | Ошибки OpenAI молча деградируют в слабый regex-fallback; ни retry, ни алерта | [understanding.go:148](internal/bot/understanding.go#L148) |
| F4 | `normalizeGoal` извлекает «цель» из URL/любого текста с `instagram`/`контент`/`продаж`; ссылки загрязняют `goal` | [analysis.go:1525](internal/bot/analysis.go#L1525) |
| F5 | History guard не подключён: `SetHistoryGuard` нигде не вызывается — все env `HISTORY_GUARD_*` мертвы | [history_guard.go:81](internal/bot/history_guard.go#L81), [cmd/main.go](cmd/main.go) |
| F6 | `isBusinessRelevantMessage` молча гасит непонятые сообщения — клиент остаётся без ответа | [approved_flow.go:167](internal/bot/approved_flow.go#L167) |
| F7 | `containsPortfolioRequest("пример")` ложно срабатывает на «примерно»; нет слова «образц»; нет интентов feasibility/confusion | [manager.go:260](internal/bot/manager.go#L260) |
| F8 | Экстракция ниши требует коротких строк — длинные «богатые» сообщения теряют нишу/продукт/кампанию | [analysis.go:1129](internal/bot/analysis.go#L1129) |
| F9 | Мёртвый легаси-код вводит в заблуждение: `Manager`, meta-webhook, `handleLocalCommand` (не вызывается), `buildLeadReply` (только легаси) | [manager.go](internal/bot/manager.go), [internal/http/](internal/http/), [service.go:1020](internal/bot/service.go#L1020) |
| F10 | «завтра/сегодня» из контекста запуска продаж сохраняется как production-deadline | [analysis.go:1553](internal/bot/analysis.go#L1553) |

---

## 5. Аудит state-менеджмента (ответы на D)

- **Хранение:** SQLite `whatsapp_clients` + in-memory map, ключ = chatID. Поля — см. §1.4.
- **Перезапись полей:** защита хорошая — `ApplyAnalysis` заменяет валидное значение только валидным; `isNonNicheCandidateText` блокирует приветствия/«да»/«завтра»/вопросы как нишу (покрыто тестами `message_understanding_test.go`). Главная проблема — **не перезапись, а не-извлечение** (F8) и **загрязнение goal ссылками** (F4).
- **Повторные вопросы:** `AskedFields` ведётся, но `qualificationFollowupText` вызывается из множества веток без проверки `AskedFields` — вопрос «какая ниша?» задаётся повторно, пока ниша не извлечена. Анти-дубль — только дословный текст в окне 2 мин.
- **Различение intent'ов:** прямой вопрос/ссылка/кейсы/цена — частично; feasibility («делаете такое?») и confusion («не понял») — **отсутствуют полностью** в обоих слоях.

## 6. Аудит промпта (E)

Промпт анализатора ([understanding.go:15](internal/bot/understanding.go#L15)) сам по себе неплох (запрет повторных вопросов, приоритет прямых вопросов, примеры), но:
1. **противоречит strict-схеме** (F2) — половина инструкций невыполнима, модель вынуждена «впихивать» ответ в 13 интентов;
2. missing_fields в схеме включает `deadline`, хотя промпт запрещает его спрашивать;
3. нет интентов `feasibility_question`, `confusion`, `case_request_for_niche`;
4. модель не генерирует reply — поэтому все требования промпта «ответь сначала на вопрос» ни на что не влияют: отвечает state machine.

Требования E1–E11 к «промпту генерации ответа» на сегодня не применимы — генерации нет. Предложение production-промпта — в §9.

## 7. Аудит медиа/кейсов (F)

- Кейсы = 4 локальных mp4 по **форматам**, не по нишам ([offers.go](internal/bot/offers.go)). Нишевых кейсов нет → запросы «по запчастям/еде/одежде» физически нечем удовлетворить (кроме `asks_for_food_examples` — костыль под один прошлый кейс с едой).
- Порядок/подписи/дедуп/логи отправки — корректные (`sendVideosWithCaptions`): caption с ценой, задержка 1.5 с, `MarkVideoSent`, `RecordOutgoingPackageMessage` (для quoted-reply выбора пакета), warn при отсутствии файла.
- После видео стадия `packages_presented`; следующий ответ контекстно-слабый (дефолт — повтор format-вопроса или молчание, F6).

## 8. Наблюдаемость (H)

Логирование zap хорошее: dedupe-решение, intent, extracted/missing fields, state before/after, openai_analyzer_used, отправки видео, причины молчания. Телефоны хэшируются (`chat_hash`). Пробелы:
- нет лога **сырого ответа OpenAI** при невалидном парсинге (только confidence);
- нет метрики/алерта на долю fallback'ов (C10) — деградация невидима;
- warn `openai customer understanding failed` не содержит тип ошибки (timeout vs 4xx vs 5xx) в структурированном поле;
- нет prompt-версионирования.

---

## 9. План исправлений (безопасный, по шагам)

### Этап 1 — маленькие точечные фиксы (низкий риск, чинят 80% скриншотов)

1. **F4:** в `extractGoal`/`normalizeGoal` — не извлекать goal из текста, состоящего из ссылки (если после удаления URL остаётся <2 значимых слов). Ссылка = только `reference_links`.
2. **F7:** `containsPortfolioRequest` — матчить по границам слова («пример», «примеры», «образц», «кейс»), исключить «примерно»; добавить интент feasibility: «делаете (примерно) такое», «можете как тут/в видео» → ответ «да, такой формат делаем» + вопрос только о недостающем.
3. **F6:** убрать молчание — если сообщение не распознано, вместо игнора отвечать коротким уточнением с учётом стадии (для `packages_presented`: «объясню проще: делаем короткие AI-ролики под вашу нишу… какая у вас ниша?»). Игнорить только явный оффтоп.
4. **Confusion-intent:** «не понял», «суть не уловил», «что это значит» → пере-объяснение ценности (уже готовый текст можно собрать из FAQ) + один вопрос.
5. **Цена в `awaiting_qualification`:** сначала текстовый `PriceText` («от 35 000 тг»), потом — видео.
6. **F10:** не сохранять deadline из фраз «завтра откроем продажи/запуск» (deadline только при явном «нужно к/до/срок»).

Тесты: юнит на каждый пункт + регресс `go test ./internal/bot/`.

### Этап 2 — починка AI-анализатора (средний риск)

7. **F2:** синхронизировать strict-схему с промптом: добавить интенты `provide_reference`, `feasibility_question`, `confusion`, `frustration`, `negative_reaction`, `request_manager`, `choose_package`; добавить поля `reference_links`, `strong_side`, `offer`, `budget`, `city`. Обновить маппинг в `customerUnderstandingToAnalysis`.
8. **F3:** 1 быстрый retry при timeout; структурированный лог `ai_analyzer_result=ok|timeout|http_error|invalid`; счётчик fallback-доли.
9. Поднять `customerUnderstandingTimeout` с 8 до 12–15 с (модель со строгой схемой на длинных сообщениях не успевает — это прямой источник бага 1в).

### Этап 3 — LLM-генерация ответа (по решению бизнеса)

10. Включить `GenerateSalesReply` как **опциональный** слой: state machine остаётся источником фактов/действий (видео, стадии, guard-ы), LLM формулирует reply по обновлённому `SystemPrompt` с правилами: сначала ответ на прямой вопрос → подтверждение извлечённого → максимум один вопрос → никаких выдуманных цен/кейсов (цены только из конфига) → escalate to manager, если не знает. Фича-флаг `BOT_LLM_REPLY_ENABLED=false` по умолчанию, канареечно включать. Санитизация: длина, запрет ссылок не из state, запрет упоминания несуществующих кейсов.
11. **F5:** либо подключить `SetHistoryGuard(greenClient, cfg.HistoryGuard…)` в `main.go`, либо удалить env-переменные из README — сейчас это ложное чувство защиты.
12. **F9:** удалить/изолировать легаси (`Manager`, meta-webhook, `handleLocalCommand`) — они не работают, но путают при доработках.
13. Нишевые кейсы: каталог `video/cases/<niche>/*` + маппинг ниш; при отсутствии точного кейса честный ответ «пришлю ближайшие по формату».

### Rollback-план
Каждый этап — отдельный коммит; фиксы этапа 1 чисто аддитивные к детекторам (словари/условия), откат = revert коммита. Этап 3 за фича-флагом — откат выключением env. Схема БД не меняется (кроме этапа 13 — новые файлы, без миграций).

---

## 10. Риски (что нельзя сломать)

| Область | Риск | Защита |
|---|---|---|
| GreenAPI отправка | `sendAndRemember`/`sendVideosWithCaptions` вызываются из 30+ мест; сигнатуры не менять | не трогать транспорт, только тексты/ветки |
| STOP админа | `outgoingMessageReceived` + `IsAdminStopCommand` — единственный ручной тормоз | не менять `manual_stop.go`; регресс `manual_stop_test.go` |
| Suppression-список | тихий skip до всей логики | не менять |
| Анти-повтор видео | `SentVideoFiles`/`ShouldSendVideo` — иначе спам видео | сохранять `MarkVideoSent`-путь |
| Handoff | `handed_off` = полное молчание бота; ошибка в guard-ах = бот пишет поверх менеджера | регресс `approved_flow_test.go`, `history_guard_test.go` |
| Прод-стейт SQLite | поля `Conversation` сериализуются как есть | только добавлять поля, не переименовывать |
| Фоллоу-апы | 15-мин авто-видео зависит от стадии `awaiting_qualification` | не менять условия `shouldSendPackageFollowup` без тестов |

---

## 11. Тест-план

**Юнит (internal/bot):**
- URL-only сообщение → goal НЕ извлечён, reference сохранён, ответ подтверждает референс (баг 1а);
- «Делаете примерно такое видео» → intent=feasibility, ответ «да, делаем», без повторного вопроса о цели (баг 1б);
- длинное сообщение (чай/носки/розыгрыш/хук) → niche извлечена (после этапа 2 — через AI-мок), deadline НЕ «завтра», ни один вопрос о уже известном (баг 1в);
- «Чет суть не уловил» → intent=confusion, ответ-объяснение, не молчание (баг 2а);
- «С запчастями есть образцы?» → intent=case_request, ниша-кандидат «автозапчасти», честный ответ про доступные форматы (баг 2б);
- «Какая минимальная цена?» в каждой стадии → первым предложением цена;
- повторная квалификация: goal+niche известны → `qualificationFollowupText` не вызывается ни из одной ветки;
- регресс: STOP, opt-out, defer, группы, dedupe, quoted-package — существующие тесты зелёные.

**Интеграционные:** мок GreenAPI + мок OpenAI (успех/timeout/invalid) → полный `ProcessIncomingWhatsAppMessage` по сценариям скриншотов; проверка последовательности исходящих.

**Ручные (прод-чеклист):** оба сценария скриншотов; отправка видео; ручное сообщение владельца + «стоп»; сообщение из группы; повтор одного сообщения (dedupe).

---

## 12. Приложение: почему бот ответил именно так (дословная механика)

```
Клиент: <instagram reel url>
  └─ normalizeGoal(url) = "контент для продвижения"      ← F4
  └─ intent=business_link → handleBusinessLink
  └─ missing=[niche] → "Ссылку получил, спасибо. Понял, цель — контент для
     продвижения. Подскажите, пожалуйста, что продаёте / какая у вас ниша?"

Клиент: "Делаете примерно такое видео"
  └─ "пример" ⊂ "примерно" → intent=portfolio_request     ← F7
  └─ handleCasesRequest, missing=[niche] → тот же вопрос о нише

Клиент: "Завтра откроем продажи на чай и на носки..."
  └─ niche не извлечена (длинный текст, F8; OpenAI молча упал, F3)
  └─ goal="рост продаж" ("продажи" в тексте), deadline="завтра" (F10)
  └─ "примерно" → portfolio_request → handleCasesRequest
  └─ "Да, кейсы можем отправить прямо сюда. Понял, цель — рост продаж.
     Подскажите, пожалуйста, что продаёте / какая у вас ниша?"

Клиент: "Чет суть не уловил"        → intent=other → isBusinessRelevantMessage=false → МОЛЧАНИЕ (F6)
Клиент: "С запчастями есть образцы?" → intent=other → МОЛЧАНИЕ (F6, F7)
```
