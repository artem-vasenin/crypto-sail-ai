# Roadmap, статус реализации и критерии готовности

Обозначения: `[x]` — реально присутствует в коде и покрыто локальной проверкой; `[~]` — только часть функции; `[ ]` — ещё нет. Текущий релиз — **research foundation**.

## M0: чистый проект

- [x] Go module `go 1.23.2`, no ChatGPT/OAuth, no live execution.
- [x] `make check`: unit, race, vet, build.
- [x] `AGENTS.md`, пользовательская документация на русском, техконтекст для ИИ.

## M1: публичный Bybit Collector

- [x] Типизированный REST и `/v5/market/kline` для одного символа/TF.
- [x] Завершённые свечи, ascending, basic OHLC validation, таймауты/ошибки.
- [x] Пагинация `instruments-info` и набор линейных USDT perpetuals, тикеры.
- [~] Ограничение API (есть retry/backoff, но нет глобального rate limiter/cache/конкурентного collection).
- [ ] Подтвердить сетевую работу на пользовательском Ubuntu.

## M2: SQLite и Surface

- [x] SQLite/WAL/миграция v1, событийные таблицы, `integrity_check`, backup.
- [x] Surface REJECT/WATCH/CANDIDATE, причины и исходные цифры, без абсолютной цены.
- [x] Категория ограниченной истории, missing ticker cause, run_id.
- [~] Разделение технических hard rejects/soft/watch; простые thresholds — гипотезы.
- [ ] Качественный ранжировщик с контролем полноты/сэмплированием отказов/устойчивым history sampler.

## M3: локальная Qwen и аналитическая проверка

- [x] Прямой клиент к Chat Completions и JSON Schema с прикладной проверкой.
- [x] Batch triage и глубокий запрос, `benchmark` на 6 синтетических сценариях.
- [x] Unit-тесты на HTTP, errors, no-tools, truncated/invalid JSON.
- [ ] **Проверить реальную установленную Qwen на Ubuntu** (сейчас только поддельный HTTP-сервер в тестах).
- [ ] Thinking A/B, latency under load, 24/7, grounding/consistency score и evidence extractor.

## M4: AI Triage

- [x] Ручная CLI `triage` после сохранённого Surface.
- [x] Обработка и запись `REQUEST_DEEP_ANALYSIS/WATCH/REJECT`.
- [~] Ограничение числа запросов одной командой; нет полной persistent queue/cooldown/dedup нагрузки.
- [ ] Scheduler, batch aggregation и автономный запуск Deep по request.

## M5: Deep Screener/Deep Analyst

- [x] 5 TF, SMA/EMA/RSI/ATR, простой pivot HH/HL/LH/LL/EQ/UNKNOWN.
- [x] JSON Deep + идентичность снимка/символа + независимые Go gates.
- [ ] MACD, EMA100/200 с полноценным warmup, 1d/1w/1m, уровни с касаниями, BTC as-of alignment, точное OI change, order-book depth, fee/risk-gated READY.
- [ ] Проверить исторические кейсы `trend_quality=0/33` из старого ZIP против реальных свечей.

## M6: WAIT/REJECT/READY lifecycle

- [x] WAIT в SQLite, entry zone, invalidation, TTL, next check, синхронная CLI `watch-check`.
- [ ] Сервис jobs, автоматический новый Deep при триггере, unique idempotent event history, завершение/архивирование идей.

## M7: Virtual Observer

- [x] Баровая симуляция **уже заполненной** виртуальной позиции, `R`, SL/TP, fees, slippage, BE, ATR-trailing, conservative ambiguous bars и gap.
- [x] Отдельный PostOnly order-simulator с PENDING/PARTIALLY_FILLED/FILLED/EXPIRED/CANCELLED и trade-through.
- [ ] Объединённый непрерывный order→position engine по реальным потокам, risk budget портфеля, margin/funding schedule, точный sizing по exchange steps, полное восстановление активных позиций.

## M8: Post-Exit/Statistics

- [~] Чистая функция формирования checkpoints по явно переданным наблюдаемым ценам.
- [ ] Background worker после закрытия, checkpoint write в SQLite, MFE/MAE post-exit по рыночному потоку, SQL отчёты и сравнение baseline vs Qwen.

## M9: Ubuntu 24/7

- [ ] Systemd unit и monitor (нельзя выдавать за готовый сервис сейчас).
- [ ] Восстановление прерванных задач, queue management, replay determinism, длительные soak-тесты.

## M10: REAL TRADER

- [ ] **Не входит** в исследовательский релиз. Перед ним поддерживаемый Go, новые механизмы риска, Testnet, reconciliation с Bybit, ручное отдельное решение пользователя.

### Рекомендуемый следующий маленький шаг

На Ubuntu: `make check`, затем `collect`/`surface`, `benchmark`; зафиксировать реальные ответы Qwen и сетевые ограничения. После этого реализовать **узкую интеграционную очередь** `Surface → AI Triage → Deep` на нескольких символах с версией prompt/schema и единым моментом as-of, не переходя к торговле.
