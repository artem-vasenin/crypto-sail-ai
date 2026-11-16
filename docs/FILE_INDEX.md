# Карта проекта: назначение каждого файла

Документ предназначен **пользователю и следующему ИИ-агенту**. Весь код написан заново, в старую кодовую базу не вносились изменения. При несовпадении файлов с этим списком см. актуальный репозиторий и обновите документ.

## Корень проекта

| Файл | Назначение |
|---|---|
| `go.mod` | Единственный Go-модуль `crypto-sail-ai`, версия языка `1.23.2`, **без внешних Go-модулей**. |
| `Makefile` | Команды `build`, `test`, `race`, `vet`, `check`, `demo`, `doctor`. `make check` собирает и выполняет тесты. |
| `.gitignore` | Не включать runtime SQLite, бинарники, журналы, ключи и `.env` в Git. |
| `AGENTS.md` | **Обязательные** правила для кодовых ИИ-агентов: безопасность, тесты, порядок работы. |
| `README.md` | Главная инструкция на русском для сборки/запуска/тестирования и границ готовности. |
| `configs/config.example.json` | Проверяемый безопасный конфиг: Bybit public URL, localhost Qwen, путь SQLite, пороги Surface, лимиты, `paper_only:true`. |

## CLI

| Файл | Назначение |
|---|---|
| `cmd/sail/main.go` | Точка входа, команды `doctor`, `collect`, `surface`, `triage`, `deep`, `benchmark`, `watch-check`, `paper`, `status`, `db-check`, `backup`. Ордеров не отправляет. |
| `cmd/sail/main_test.go` | Unit/CLI integration через поддельный Bybit HTTP: collect/surface/deep, SQLite, backup, offline paper. |
| `cmd/sail/ai_integration_test.go` | Сквозной тест с поддельными Bybit и Qwen: Surface → triage → deep WAIT → SQLite, watch-check и benchmark; **никаких сетевых вызовов к реальной Qwen**. |
| `cmd/sail/triage_regression_test.go` | Сквозная регрессия: при нарушении triage-schema LLM-вызов журналируется, новые решения и позиции не создаются. |

## Источники рынка и конфигурация

| Файл | Назначение |
|---|---|
| `internal/market/types.go` | Типы `Candle`, `Ticker`, `Instrument`; UTC/series/freshness/OHLC/finite validation; поддерживаемые таймфреймы. |
| `internal/market/types_test.go` | Некорректные свечи, незакрытые, дубли, weekly alignment, spread, invalid timestamp. |
| `internal/bybit/client.go` | Только PUBLIC Bybit V5: GET instruments-info с cursor, tickers, kline, retry/timeouts. Без приватных endpoints. |
| `internal/bybit/client_test.go` | Мок Bybit с пагинацией, ретраями, неверными числами и OHLC, сортировкой закрытых свечей, фильтром инструментов. |
| `internal/config/config.go` | Defaults, JSON decoding с неизвестными полями как ошибкой, `paper_only=true` и локальный LLM URL. |
| `internal/config/config_test.go` | Запрет небезопасных/неверных значений и лишних полей конфигурации. |

## Аналитика

| Файл | Назначение |
|---|---|
| `internal/surface/surface.go` | Лёгкие исходные метрики, статусы REJECT/WATCH/CANDIDATE и причины; монеты с короткой историей не отсекаются автоматически. |
| `internal/surface/surface_test.go` | Номинально дешёвая монета, wide spread, low turnover, stale/instrument errors. |
| `internal/features/features.go` | SMA/EMA, Wilder RSI, Wilder ATR, swing pivots, HH/HL/LH/LL/EQ/UNKNOWN, optional missing values. |
| `internal/features/features_test.go` | Формулы SMA/EMA/RSI/ATR, отсутствие истории, структуры на искусственной последовательности. |
| `internal/decisions/decision.go` | DTO Deep Proposal, проверка snapshot/symbol/side и независимые Go gates для REJECT/WAIT/READY/NEED_DATA. |
| `internal/decisions/decision_test.go` | READY при условно полном наборе, downgrade на WAIT, Reject, wrong side/zone, missing TF. |
| `internal/llm/client.go` | HTTP-клиент только localhost, системные аналитические инструкции, JSON Schema, `finish_reason`, строгая структура, triage/deep DTO. |
| `internal/llm/client_test.go` | HTTP-моки для triage/deep, неверный snapshot/symbol, truncated response, unknown fields, bad JSON, не локальный URL. |
| `internal/llm/triage_regression_test.go` | Регрессия пяти пустых `reason_codes`, сохранение сырого ответа, диагностика полей и JSON Schema `minItems`. |
| `internal/watch/watch.go` | WAIT Idea, entry zone, invalidation, scheduled recheck, TTL; триггер не равен приказу торговать. |
| `internal/watch/watch_test.go` | Invalidation, cooldown, entry zone, срок идеи, неверный диапазон. |

## Виртуальная торговля и хранение

| Файл | Назначение |
|---|---|
| `internal/observer/observer.go` | Позиция, условный filled entry, initial-R, SL/TP, net PnL, cost-aware BE, ATR trailing, MFE/MAE, stop-first bars, PostExit (чистая функция). |
| `internal/observer/observer_test.go` | Initial-R, fee/slippage/funding BE, SHORT/LONG зеркальность, gap, ambiguous SL/TP, trailing monotonicity, post-exit. |
| `internal/observer/order.go` | Отдельная виртуальная PostOnly-заявка с Pending/Partial/Filled/Expired/Cancelled и условным trade-through fill. |
| `internal/observer/order_test.go` | Нельзя считать одно касание fill; maker side, qty, частичный fill, TTL, tick rounding. |
| `internal/storage/sqlite.go` | CGO к системной SQLite; WAL, schema v1, transactions migrations, params binding, сохранение всех исследовательских сущностей, online backup. |
| `internal/storage/sqlite_test.go` | Открытие/повторное открытие, FK, неподменяемые snapshots, WAL online backup, конкурентные записи, recovery, integrity. |

## Тестовые данные и вспомогательные скрипты

| Файл | Назначение |
|---|---|
| `testdata/paper-example.json` | Синтетический LONG replay с несколькими закрытыми OHLC барами. **Не настоящие BTC котировки**. |
| `testdata/llm-benchmark.json` | 6 синтетических случаев для первого baseline-теста Qwen. Метки иллюстративны; нет истинной доходности. |
| `scripts/verify_offline.sh` | Безопасный офлайн `make check` + coverage + doctor. Не обращается к API и не создаёт ордеров. |
| `scripts/check_qwen_readonly.sh` | Только чтение состояния `llama-server`, `/v1/models` и `nvidia-smi`; не меняет службу. |

## Документация (все файлы на русском)

| Файл | Назначение |
|---|---|
| `docs/AI_CONTEXT.md` | История проекта/старого скриннера, роль локальной Qwen, текущий и будущий статус; передавать новому ИИ-чату. |
| `docs/ARCHITECTURE.md` | Фактический pipeline CLI и схема будущего 24/7 контура, ограничения времени данных. |
| `docs/DECISIONS.md` | ADR: Go 1.23.2, CGO SQLite, LLM direct, data quality, риск, запрет реальных ордеров. |
| `docs/ROADMAP.md` | Чеклист M0–M10, честно отмечены реализованные и отсутствующие части, criteria/done. |
| `docs/TESTING_RU.md` | Подробная пошаговая инструкция для пользователя Ubuntu, пример вывода и диагностика. |
| `docs/UPGRADE_AND_GIT_RU.md` | Безопасное обновление исходников из исправленного ZIP, резервная копия SQLite и первый Git-коммит на Ubuntu. |
| `docs/LLM_PROTOCOL.md` | Вход/выход Qwen, JSON Schema, причины отказа, контрольные сценарии. |
| `docs/STORAGE.md` | Таблицы SQLite, WAL, backup/restore и ограничения схемы. |
| `docs/RISK_AND_PAPER.md` | Формулы Net R, BE, слippage, trailing, maker-fill и честные ограничения симулятора. |
| `docs/VALIDATION.md` | Какие команды реально выполнены при сборке артефакта, полученные показатели coverage. |
| `docs/CHANGELOG.md` | История первого самостоятельного релиза. |

## Что **не** нужно искать в ZIP

- Старых `cmd/bot`/`internal/execution`, Bybit private endpoints, ChatGPT OAuth/Keychain и `llm-agent` — их **намеренно нет**.
- Файлов базы `data/*.db`, ключей `.env`, старых screening JSON, бинарников `bin/sail` — runtime/собранные артефакты не входят в исходный ZIP. Соберите на своём Ubuntu через `make build`.
- Готового systemd unit для 24/7 бота — до непрерывного event-loop его нет. Не запускать `surface` каждые 15 секунд через cron без rate-limits/архитектуры.
