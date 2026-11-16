# SQLite: схема, WAL, безопасность данных и миграции

## Технический выбор

Файловая SQLite (`data/crypto-sail.db`), `journal_mode=WAL`, `synchronous=FULL`, `foreign_keys=ON`, `busy_timeout=5000 ms`. Пакет `internal/storage` — CGO обёртка над системной `libsqlite3`, без внешних Go-драйверов/модулей. Доступ к одному соединению сериализуется `sync.Mutex` — консистентность и простота важнее масштабной записи на раннем этапе.

На Ubuntu: `sudo apt install build-essential libsqlite3-dev`; компиляция только `CGO_ENABLED=1`. `data/` исключено из Git. Основная БД должна быть на локальном надёжном диске, **не** на сетевом mount с сомнительной поддержкой file locks.

## Таблицы миграции №1

| Таблица | Ключевые поля | Смысл |
|---|---|---|
| `snapshots` | id (PK), kind, symbol, at_ms, payload JSON | Неизменяемые Surface/Deep/исторические снимки |
| `decisions` | id (PK), snapshot_id FK, phase, symbol, status, payload | Результат Go Surface/AI triage/Deep gates |
| `llm_calls` | id (PK), snapshot_id, phase, model, ok, latency, tokens, raw, error | Все ответы и диагностика модели |
| `watch_ideas` | (symbol,side) PK, at_ms, payload | Текущее WAIT-состояние с recheck, TTL, зоной |
| `virtual_positions` | id PK, at_ms, payload | Результаты офлайн виртуальных позиций |
| `position_events` | id PK, position_id FK, at_ms, payload | Move SL, предполагаемый fill, exit |
| `post_exit_checkpoints` | id PK, position_id FK, at_ms, payload | Заранее зарезервирована под будущий post-exit worker |

`PRAGMA user_version=1`. **Не** удалять таблицы, не перезаписывать уже существующий snapshot_id, не очищать БД «для теста»: для экспериментов используйте другую `-db`.

**Текущее ограничение:** JSON-поля удобны для сохранения богатого snapshot, но для тяжёлой статистики и индексированных feature-запросов понадобятся нормализованные таблицы `features`, `experiments`, `outcomes` и schema migrations. Пустая `post_exit_checkpoints` ещё не означает запущенный post-exit worker.

## Операции

```bash
./bin/sail status
./bin/sail db-check
./bin/sail backup -out data/backup-$(date -u +%Y%m%dT%H%M%SZ).db
```

Backup использует `sqlite3_backup_init/step/finish` и не допускает перезаписи существующего файла. Безопасный snapshot при WAL — именно так, а не `cp` живого основного `.db`. Храните резервные копии на отдельном носителе с контролем доступа, периодически проверяйте `db-check -db FILE` и возможность восстановления в тестовом каталоге.

## Восстановление

1. Остановить процессы, использующие базу, убедиться в отсутствии активных writers.
2. Сохранить существующие `.db`, `-wal`, `-shm` отдельно **как единый набор** на случай отката.
3. Скопировать консистентный backup в путь `.db`; не оставлять старые `-wal/-shm` рядом с восстановленным backup.
4. Выполнить `./bin/sail db-check -db restored.db`, затем `status` и проверить сохранённые snapshots.
5. Не включать непрерывный scheduler до успешной сверки состояния WAIT/виртуальных позиций.

## Тесты

`internal/storage/sqlite_test.go` проверяет WAL-миграцию, повторное открытие, FK, уникальность snapshots, upsert WAIT, 12 конкурентных записей, online-backup и integrity_check; запускается обычным `go test` и `-race`.
