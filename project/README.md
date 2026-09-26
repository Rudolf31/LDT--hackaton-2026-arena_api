# Арена переговоров — бэкенд портала HR

Один процесс на Go (модульный монолит) поверх PostgreSQL 16. Обслуживает `/api/portal/*`
(веб-портал HR) и `/api/trainer/*` (клиент-тренажёр). Полная документация проекта,
архитектура и план реализации по этапам лежат уровнем выше, вне этого репозитория —
см. `../CLAUDE.md` и `../plans/backend/`.

## Требования

- Docker и Docker Compose (для запуска — это единственный обязательный способ).
- Для локальной разработки без контейнера — Go 1.26+.

Зависимости не вендорятся: сборка образа качает их с `proxy.golang.org` (нужна сеть на
момент сборки). Если это неприемлемо для вашего окружения — см. `plans/backend/decisions.md`,
D-12.

## Быстрый старт

```bash
cp .env.example .env
# заполнить .env: POSTGRES_SUPERUSER_PASSWORD, ARENA_APP_PASSWORD — любые строки;
# ARENA_MASTER_KEY и ARENA_CODE_HMAC_SECRET — по одному вызову на каждую:
openssl rand -base64 32
# без них ни Postgres, ни портал не стартуют; портал ещё и проверяет формат (D-15) —
# не base64 или не ровно 32 байта после декодирования — откажется стартовать и скажет, что не так

make up
curl -i http://localhost:8080/что-угодно   # 404 problem+json — портал жив
```

Первый `make up` разворачивает схему `arena-db.sql` ролью-владельцем в Postgres (init-скрипт
контейнера), затем портал подключается под ограниченной ролью `arena_app` (без прав DDL).

## Первый вход

Если в базе нет ни одного пользователя портала, при старте портал сам заводит базовый набор
из `db/seed/base.json` (D-23): двух пользователей, демо-группу «Отдел продаж (демо)» с доступом
методолога и четырёх вымышленных сотрудников. Это делается **независимо от `ARENA_DEMO`** —
иначе в портал было бы некому войти. В лог при этом пишется предупреждение.

| Логин | Пароль | Роль |
|---|---|---|
| `admin` | `arena-admin-2026` | администратор |
| `methodologist` | `arena-method-2026` | методолог |

**Пароли опубликованы здесь, поэтому на любом стенде, кроме локального, смените их сразу
после первого входа** (`PATCH /api/portal/users/{id}` с `new_password`).

```bash
curl -i -c cookies.txt -H 'Content-Type: application/json' \
  -d '{"login":"admin","password":"arena-admin-2026"}' http://localhost:8080/api/portal/auth/login
curl -b cookies.txt http://localhost:8080/api/portal/auth/me
```

Вход ставит cookie `arena_session` (httpOnly, SameSite=Strict, 8 часов без действий, продлевается
с каждым запросом). По умолчанию у неё флаг `Secure`: браузер вернёт её только по HTTPS или на
`localhost`. Если стенд открыт по обычному http не на localhost — поставьте
`ARENA_COOKIE_SECURE=false`, иначе вход молча не сработает. `curl` флаг `Secure` на http
не соблюдает, так что пример выше работает при любом значении.

```bash
make down   # остановить
```

## Команды (Makefile)

| Команда | Что делает |
|---|---|
| `make up` | Собрать образ и поднять `postgres` + `portal` (`docker compose up -d --build`). |
| `make down` | Остановить и убрать контейнеры (том с данными Postgres остаётся). |
| `make test` | `go test ./...` — модульные тесты, без базы. |
| `make test-integration` | Интеграционные тесты (тег `integration`) — нужен поднятый `postgres` (`make up` или `docker compose -f deploy/docker-compose.yml --env-file .env up -d postgres`) и адрес роли-владельца в `ARENA_TEST_ADMIN_DSN` (по умолчанию `postgres://postgres:postgres@localhost:5432/postgres`; с паролем из `.env` — `postgres://postgres:<POSTGRES_SUPERUSER_PASSWORD>@localhost:5432/postgres?sslmode=disable`). Каждый тест поднимает одноразовую базу и дропает её по завершении. |
| `make lint` | Линтер границ модулей (`golangci-lint` + `depguard`) — падает, если `store.go` или `transport.go` модуля импортируют другой модуль напрямую (D-18); `contract.go`, `module.go`, `service.go` — импортируют контракты других модулей свободно, как и требует архитектура 3.3. |
| `make gen` | Перегенерировать HTTP-типы (`internal/api/gen`) из `api/arena-api.yaml`. Коммитить результат. |
| `make tidy` | `go mod tidy`. |

## Структура

```
cmd/portal/            точка входа: конфигурация, пул, сборка модулей, HTTP, мягкое завершение
internal/modules/      девятнадцать модулей — каждый: contract.go, module.go, transport.go,
                        service.go, store.go (устройство модуля — CLAUDE.md)
internal/platform/      общий слой: config, pg, crypto, ai, httpx, ratelimit, log, actor
internal/api/gen/       сгенерированный HTTP-код (oapi-codegen), закоммичен
api/arena-api.yaml      контракт — копия источника правды, не редактируется вручную
api/gen/                производная копия контракта для кодогена + скрипт-препроцессор
                        (см. api/gen/preprocess.py — почему она нужна отдельно от контракта)
db/schema/arena-db.sql  схема базы — побайтная копия источника правды, не редактируется
db/seed/                данные первичной заливки (пользователи, группа, сотрудники); заливает их
                        модуль demo при пустой базе
deploy/                 Dockerfile, docker-compose.yml, init-скрипт роли приложения
scripts/                статические проверки инвариантов (например, I-2 — две оценки
                        нигде не складываются)
```

## Переменные окружения

Полный список с описанием — в `.env.example`. Обязательные без значения по умолчанию:
`ARENA_MASTER_KEY`, `ARENA_CODE_HMAC_SECRET`, плюс для docker-compose —
`POSTGRES_SUPERUSER_PASSWORD`, `ARENA_APP_PASSWORD`. Модели авторства сценария
(`ARENA_STT_*`, `ARENA_GEN_*`) необязательны — без них соответствующие адреса отвечают 503,
остальной портал работает как обычно.

`ARENA_COOKIE_SECURE` (по умолчанию `true`) — флаг `Secure` у cookie входа, см. «Первый вход».

`ARENA_MASTER_KEY` и `ARENA_CODE_HMAC_SECRET` — строго base64, ровно 32 байта после
декодирования (D-15): `openssl rand -base64 32` даёт то, что нужно. Неверный формат или
длина — портал не стартует и называет переменную и что именно с ней не так.

## Регенерация HTTP-слоя

Контракт (`api/arena-api.yaml`) — версии OpenAPI 3.1, но не проходит через `oapi-codegen`
как есть (несуществующие `$ref`, несовместимые объединения типов) — препроцессор
`api/gen/preprocess.py` строит производную копию только для кодогена, исходный файл не
трогает. Подробности — `plans/backend/decisions.md`, D-09. После правки контракта:

```bash
make gen
git diff internal/api/gen/api.gen.go   # проверить, что изменилось
```

## Этот README

Держим в актуальном состоянии при любой правке, которая меняет способ запуска, переменные
окружения или структуру каталогов — не только на этапе 00.
