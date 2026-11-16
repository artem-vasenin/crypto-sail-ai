# Полное руководство по тестированию Crypto Sail AI на Ubuntu (на русском)

**Версия:** v0.1.1-research. **Важно:** ниже нет операций с реальными деньгами, приватными ключами или выставлением заявок. Любое успешное прохождение тестов означает проверку кода, а не доказательство доходности.

## 1. Подготовка

Распакуйте ZIP в отдельный каталог. **Не** распаковывайте поверх старого Crypto Sail AI или `llm-agent`, не удаляйте их исходники, модели и БД.

```bash
cd ~/crypto-sail-ai
sudo apt update
sudo apt install -y build-essential libsqlite3-dev make curl
GOTOOLCHAIN=local go version
```

Требуется Go >= 1.23.2 (минимум из `go.mod`); пользователь уже протестировал Go 1.27.1/1.27.4. Если Go отсутствует, его нужно установить отдельно. `golang-go` из apt на Ubuntu 24.04 не гарантирует именно 1.23.2; не доверяйте номеру пакета. Для сборки нужны `gcc`, `libsqlite3-dev` и `CGO_ENABLED=1`. Проверьте:

```bash
gcc --version
ls /usr/include/sqlite3.h
GOTOOLCHAIN=local go env CGO_ENABLED
```

Если компилятор Go другого поколения, сообщите его точную версию; не правьте `go.mod` наугад.

## 2. Офлайн-тесты (не требуют Bybit/Qwen)

```bash
make check
```

Команда выполнит:

```bash
GOTOOLCHAIN=local CGO_ENABLED=1 go test ./...
GOTOOLCHAIN=local CGO_ENABLED=1 go test -race ./...
GOTOOLCHAIN=local CGO_ENABLED=1 go vet ./...
GOTOOLCHAIN=local CGO_ENABLED=1 go build -o bin/sail ./cmd/sail
```

**Успех:** нет строк `FAIL`, каждый тестовый пакет имеет `ok`, бинарник `bin/sail` создан. Поддельные HTTP-серверы (`httptest`) проверяют сериализацию и обработку ошибок Bybit/Qwen **без внешнего интернета**. Race detector тестирует многопоточные обращения к SQLite.

Проверить покрытие:

```bash
GOTOOLCHAIN=local CGO_ENABLED=1 go test -cover ./...
```

Покрытие измеряется по пакетам, не обещает отсутствие ошибок. `go test` запускается при каждом серьёзном изменении кода.

## 3. Offline doctor и SQLite

```bash
./bin/sail doctor
./bin/sail db-check
./bin/sail status
```

Ожидаются: `paper_only: true`, `go_runtime` с установленной версией Go, `sqlite_integrity: ok`; в `status` появятся счётчики. Если БД ещё не существовала, будет создан файл `data/crypto-sail.db` и SQLite WAL. Директория `data/` не включена в ZIP и не должна попадать в Git.

**Создать независимую БД для эксперимента:**

```bash
./bin/sail doctor -db data/test-research.db
```

Не удаляйте основную БД для «сброса ошибок», если в ней есть экспериментальные решения.

## 4. Виртуальный тест входа и управления риском

```bash
./bin/sail paper -db data/test-research.db -file testdata/paper-example.json
./bin/sail status -db data/test-research.db
```

Фикстура содержит **придуманные цены** для проверки математической логики. В JSON-выводе должны быть `status: CLOSED`, `exit_reason: TP`, `initial_risk_price: 2`, события `VIRTUAL_FILL_ASSUMED`, `STOP_IMPROVED` и `VIRTUAL_CLOSE`. `NetPnL` меньше gross PnL из-за комиссий и принятого проскальзывания. После движения SL исходный R **не меняется**.

Симуляция сознательно предполагает, что виртуальный вход был исполнен. Она **не моделирует фактический maker-fill по ордербуку** для этой команды. Отдельная модель pending/partial fills покрыта `internal/observer/order_test.go`.

Запустить повторно — в БД добавится ещё одна **независимая** виртуальная позиция. Не трактуйте пример как доходность BTCUSDT.

## 5. Подключение публичного Bybit (сеть)

```bash
./bin/sail collect -symbol BTCUSDT -tf 60 -limit 100
```

В успешном ответе: `symbol=BTCUSDT`, `tf=60`, `count` закрытых свечей, их `first` и `last`, `snapshot_id`. Если API недоступен/заблокирован — будет ошибка; тест **не выполнен**. Ничего не подставляйте вручную в качестве якобы живых цен.

Затем:

```bash
./bin/sail surface -show 10
./bin/sail status
```

Проверьте, что в JSON есть `instruments`, `ticker_rows`, `counts`, `run_id`, `top`; суммарное число `CANDIDATE+WATCH+REJECT` должно равняться `evaluated`. Для отсутствующего тикера будет `REJECT/missing_ticker`, а не исчезнувшая монета. `WATCH` тоже может попасть в triage. Абсолютная цена НЕ фильтруется.

## 6. Проверить, действительно ли работает локальная Qwen

Только на **том же Ubuntu-сервере**, где уже запущена Qwen3-8B:

```bash
systemctl status llama-server --no-pager
curl -sS --max-time 10 http://127.0.0.1:8080/v1/models
```

Убедитесь, что в ответе нужная модель, порт не открыт наружу, GPU не перегружена. Ничего не переустанавливайте. Затем:

```bash
./bin/sail benchmark -file testdata/llm-benchmark.json -limit 6
```

В ответе — число корректных схем (`valid_count`), время и принятое решение в каждом сценарии, а также `illustrative_label_agreement_count`. **Низкая доля совпадения с иллюстративным классом требует содержательного разбора** — не переписывайте ожидаемый класс ради высокой метрики. Даже 6/6 не доказывает качество стратегии.

Если тест падает: сохраните ошибку, `systemctl status llama-server`, `journalctl -u llama-server -n 80`, текущее `nvidia-smi`. Не изменяйте `--reasoning off`, `-c 16384` и модель в ходе первого теста: нам нужен baseline.

### Проверить triage и Deep

Непосредственно после `surface` (до истечения `max_ticker_age_seconds`, по умолчанию 180 секунд):

```bash
./bin/sail triage -limit 5
./bin/sail deep -symbol BTCUSDT -side LONG
./bin/sail deep -symbol BTCUSDT -side LONG -ai
./bin/sail watch-check
```

`triage` не выставляет заявки и не запускает Deep. `deep` без `-ai` возвращает рассчитанные Go признаки. `deep -ai` возвращает предложение модели, **effective_decision** после Go-gates и причины. `READY` на данном этапе — только исследовательский статус. Если `watch-check` пишет `trigger=true`, это не означает готовность открыть позицию; нужен новый Deep-анализ.

## 7. Проверить надёжность БД и резервное копирование

```bash
./bin/sail db-check
./bin/sail backup -out data/backup-$(date -u +%Y%m%dT%H%M%SZ).db
```

Бэкап создаётся средствами SQLite Online Backup API и остаётся консистентным с активным WAL. Нельзя перезаписать уже существующий файл бэкапа через команду. **Восстановление** — только после завершения всех процессов бота: перенесите исходный `.db` вместе с существующими `-wal/-shm` в архивное место, затем замените `.db` проверенным бэкапом и выполните `db-check`. Не копируйте один живой `.db` без WAL из-под запущенных процессов.

## 8. Что считается успешным окончанием текущего этапа

- `make check` завершился успешно на Ubuntu с Go 1.23.2.
- `db-check` возвращает `ok`, backup можно открыть и проверить отдельно.
- Bybit отдаёт свежие закрытые свечи и полный постраничный universe.
- Surface сохраняет все проверенные инструменты и причины.
- Qwen на Ubuntu выдерживает benchmark и triage; не выдумывает отсутствующие значения на контрольных кейсах.
- Любая ошибка LLM не превращается в READY.
- Виртуальные тесты SL/TP/fees/BE/trailing проходят.

Если все эти проверки пройдены, **это подтверждает исследовательскую основу**, но не готовый 24/7 Paper Trader и не торговую прибыльность. Следующие этапы: очередь и планировщик; см. `docs/ROADMAP.md`.

## 9. Частые ошибки

| Симптом | Возможная причина / действие |
|---|---|
| `go.mod requires ...` | Не установлен точный Go 1.23.2; `GOTOOLCHAIN=local go version` |
| `sqlite3.h not found` | `sudo apt install build-essential libsqlite3-dev`, `CGO_ENABLED=1` |
| `LLM HTTP 500`, timeout | Состояние llama-server, VRAM/контекст; сохранить ошибку и журнал |
| `LLM response is not JSON` | Ответ без structured JSON; сохранить контекст и завершение generation |
| `no fresh Surface items` | Запустить `surface` и сразу `triage`, без долгой паузы |
| `no closed candles` | Неверный TF/инструмент/ответ API или формирующаяся свеча |
| `stale ticker` | Время источника или машины; не вводить фиктивные метки |
| `duplicate snapshot` | Повтор того же идентификатора в БД; не удалять историю, исследовать источник |
| Не исполняется `make check` | Проверить go/gcc/SQLite, приложить точный stdout/stderr |

## 8. Инцидент triage, восстановление из полного ZIP и Git

Если Qwen возвратила `reason_codes: []`, прежняя версия завершала `triage` общей ошибкой `invalid triage content`. Исправление v0.1.1 отклоняет этот ответ более точно (`symbol=... field=reason_codes`) и сохраняет сырой контент в `llm_calls`. Принятых решений и ордеров при ошибке нет. Проверка регрессий:

```bash
GOTOOLCHAIN=local CGO_ENABLED=1 go test -count=1 ./internal/llm ./cmd/sail
make check
./bin/sail surface -show 5
./bin/sail triage -limit 5
```

Реальную Qwen после обновления нужно протестировать на пользовательском сервере. Для установки ZIP без потери SQLite и создания первой истории Git см. `docs/UPGRADE_AND_GIT_RU.md`.
