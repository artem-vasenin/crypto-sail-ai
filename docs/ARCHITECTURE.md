# Архитектура: фактическая реализация и целевая схема

## 1. Реализованные, независимые командные пути

```text
collect → Bybit public REST /v5/market/kline
        → только закрытые UTC свечи, сортировка, проверка OHLC
        → SQLite snapshots(kind=candles) → JSON-отчёт

surface → instruments-info [cursor pages] + tickers
        → валидация инструмента/тикера
        → Surface REJECT/WATCH/CANDIDATE + raw metrics
        → SQLite snapshots + decisions → JSON-отчёт

triage  → последние свежие Surface из одного run_id
        → ограниченный batch ≤ max_llm_batch
        → localhost llama.cpp:8080 (JSON Schema)
        → проверка snapshot_id, полного списка, вариантов ответа
        → SQLite llm_calls + decisions; для WATCH сохранить WAIT
        → НЕ запускает deep автоматически

deep    → public 5m/15m/30m/1h/4h, до 100 закрытых свечей каждого TF
        → EMA20/50, RSI14, SMA200 (missing), ATR14, pivot structure
        → SQLite deep snapshot
        → [-ai] Qwen deep proposal + проверка identity + Go hard gates
        → SQLite decision, WAIT (если применимо)
        → НЕ создаёт виртуальную позицию

watch-check → SQLite WAIT + свежий ticker → дешёвый trigger-report
            → НЕ перезапускает deep и НЕ открывает позицию

paper   → JSON synthetic closed OHLC fixture → assumed initial fill
        → Stop/TP/BE/ATR-trailing + costs → SQLite virtual_positions/events
        → НЕ обращается к рынку или LLM

benchmark → JSON synthetic analytical scenarios → Qwen deep proposals
          → format, latency, illustrative-agreement statistics
          → SQLite llm_calls; НЕ является историческим backtest
```

## 2. Ключевые пакеты

| Пакет | Контракт |
|---|---|
| `internal/market` | Самодостаточные типы и validation. Качество данных до стратегии. |
| `internal/bybit` | Только public REST GET, context/timeout/retry; типизированный market. |
| `internal/surface` | Одно решение на один тикер, исходные показатели, отдельно history class. |
| `internal/features` | Индикаторы/structure из завершённых свечей, optional values для отсутствующей истории. |
| `internal/llm` | HTTP Chat Completions + JSON Schema + строгая прикладная валидация. |
| `internal/decisions` | Go-gates поверх предложения Qwen, не автономная стратегия. |
| `internal/watch` | WAIT-идея и чистая функция решения о повторной проверке. |
| `internal/observer` | Баровая виртуальная позиция + отдельный maker-order simulator. |
| `internal/storage` | Файловая SQLite, WAL, миграция, backup. |
| `cmd/sail` | Чёткие безопасные entry points для исследователя. |

## 3. Будущая цель (НЕ реализована)

```text
Scheduled Collector → Surface Facts → Persistent Candidate Queue
 → AI Triage → Deep Job Queue → Deep Snapshot + Qwen
 → Go gates → REJECT / WAIT watcher / READY → virtual order
 → pending/partial/fill → live-tick-aware paper position manager
 → closed → timed post-exit worker → SQLite analytics → hypotheses
```

Нужны: durable jobs, идемпотентный recheck, snapshots version/UTC alignment, fee/risk portfolio layer, отдельное моделирование исполнения с публичными trade prints, восстановление позиций, таблицы post-exit checkpoints и статистика. **Не добавлять реальную торговлю в этот pipeline.**

## 4. Данные и воспроизводимость

`market.Ticker.ObservedAt` — время биржевого snapshot; `surface.Result.RunID` группирует один запуск скриннинга; `SnapshotID` — короткий SHA-256 идентификатор сохранённой сериализации. Это не криптографическая подпись биржи. Свежесть проверяется перед AI-триажом и Deep; незавершённые свечи исключаются до расчёта признаков. Выходы модели отделены от effective Go decision.

Текущая структура SQLite не хранит всех исходных книг заявок и тиков. При запросе истории сохраняется массив закрытых OHLC; при Deep сохраняются **вычисленные** признаки и surface, не полный исходный candle history каждого TF. Поэтому полный bit-for-bit пересчёт всех Deep-признаков пока невозможен из одного Deep snapshot — важная будущая задача M5.

## 5. Потенциальные проблемы, которые нельзя скрывать

- REST 5 разных TF вызывается последовательно; нет доказательства идентичной `as-of` границы. Пока каждый TF должен быть свежим, но snapshot не полностью time-aligned.
- `triage` выбирает небольшой свежий батч по обороту; это начальный механизм, не справедливый round-robin по всему рынку.
- `llm_calls` фиксирует сырой ответ и usage, но сейчас не сохраняет целиком отправленное тело промпта отдельно: исходный Surface/Deep snapshot сохранён, код схемы/помпта версии не закреплены per-call. Нужны prompt version и deterministic replay metadata.
- `Deep` пока без day/week, подтверждений BTC, полноценного OI change, orderbook depth и fees-aware ready risk precheck; READY — **только логический исследовательский статус**.
- Виртуальный баровый `paper` трактует вход уже filled. Даже если результат положителен, это не доказательство исполнимости PostOnly лимитной заявки.
- Из одних OHLC нельзя установить истинный внутрибара порядок SL/TP. Применена консервативная эвристика, указанная в отчёте.
