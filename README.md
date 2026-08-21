# Trip-Pip Backend

Backend Trip-Pip на Go. Реализованы карточки туристов и заявки турагентства (первый этап) и финансовый учёт — платежи, агентское вознаграждение, базовые отчёты (второй этап), с изоляцией данных между агентствами, авторизацией по email и паролю и историей изменений. Хранилище — PostgreSQL.

## Требования

- Go 1.26;
- PostgreSQL 15+ (расширения `citext` и `pg_trgm` устанавливаются миграцией автоматически);
- Docker и Docker Compose — для контейнерного запуска.

## Локальный запуск

Без базы данных сервис поднимается и отвечает на `/api/ping` и `/api/version`, но все остальные маршруты возвращают `503`:

```bash
go run ./cmd/api
curl http://127.0.0.1:8080/api/ping
curl http://127.0.0.1:8080/api/version
```

С базой данных — миграции применяются автоматически при старте, а если задать `BOOTSTRAP_*`, создаётся первое агентство и пользователь (только если в базе ещё нет ни одного):

```bash
DATABASE_URL='postgres://trip-pip:trip-pip@localhost:5433/trip-pip?sslmode=disable' \
SECURE_COOKIES=false \
BOOTSTRAP_AGENCY_NAME='Моё агентство' \
BOOTSTRAP_USER_EMAIL='admin@example.com' \
BOOTSTRAP_USER_PASSWORD='ваш-пароль-от-12-символов' \
go run ./cmd/api
```

`SECURE_COOKIES=false` нужен только для локальной работы по `http://`; в проде cookie сессии всегда `Secure`.

Ответы диагностических эндпоинтов:

```json
{"message":"pong"}
{"commit":"dev"}
```

`GET /api/ping` — проба живости, её использует `HEALTHCHECK` образа. `GET /api/version` возвращает коммит, из которого собран бинарник. `GET /api/health` дополнительно проверяет доступность базы данных и возвращает `503`, если её нет или она недоступна; этот эндпоинт не участвует в `HEALTHCHECK` и в проверках деплоя намеренно — если бы участвовал, временная недоступность БД откатывала бы релиз.

## Переменные окружения

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `HTTP_ADDR` | `:8080` | адрес HTTP-сервера |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `DATABASE_URL` | пусто | строка подключения к PostgreSQL; пусто — сервис работает без БД |
| `DB_MAX_CONNS` | `10` | размер пула соединений |
| `DB_CONNECT_TIMEOUT` | `10s` | таймаут первого подключения к БД |
| `RUN_MIGRATIONS` | `true` | применять миграции при старте |
| `SESSION_TTL` | `24h` | срок жизни сессии |
| `SECURE_COOKIES` | `true` | флаг `Secure` у cookie сессии |
| `ALLOWED_ORIGINS` | пусто | список разрешённых origin через запятую |
| `APP_TIMEZONE` | `Europe/Moscow` | часовой пояс для «сегодня» в напоминаниях |
| `BOOTSTRAP_AGENCY_NAME`, `BOOTSTRAP_USER_EMAIL`, `BOOTSTRAP_USER_PASSWORD` | пусто | первое агентство и пользователь; срабатывает один раз, пока в базе нет ни одного пользователя; после первого запуска эти переменные стоит убрать |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | `SMTP_PORT=587`, остальные пусто | релей для писем подтверждения регистрации; `SMTP_HOST`/`SMTP_FROM` — обязательная пара, без них `POST /api/auth/register` отвечает `503` |
| `PUBLIC_BASE_URL` | пусто | адрес фронтенда для ссылки в письме подтверждения; обязателен, если задан SMTP |

## Модель данных и авторизация

Каждая доменная таблица содержит `agency_id`, и каждый запрос к хранилищу получает его из сессии, а не из тела запроса. Связи между таблицами объявлены составными внешними ключами `(id, agency_id)`, поэтому привязать запись одного агентства к записи другого невозможно на уровне базы данных, даже если обработчик забудет добавить фильтр. Запись, принадлежащая другому агентству, всегда выглядит как `404 not_found` — не `403`, чтобы не подтверждать её существование.

Авторизация — сессионная, через `HttpOnly`-cookie (`trip_pip_session`, `SameSite=Lax`). Пароли хешируются `argon2id` (`golang.org/x/crypto/argon2`) с параметрами по рекомендации OWASP, минимальная длина — 12 символов. Дальнейших сотрудников агентства создаёт `POST /api/users` любой уже вошедший сотрудник — на первом этапе все сотрудники агентства имеют одинаковый уровень доступа. По той же причине `PATCH /api/users/{id}` может поменять отображаемое имя как себе, так и коллеге; email и пароль этот эндпоинт не затрагивает. Данные самого агентства (название, ИНН, часовой пояс, статус) читает `GET /api/agency` — id берётся из сессии, отдельного параметра для него нет.

Новые агентства заводятся двумя способами. Переменные `BOOTSTRAP_*` — операционный запасной вход для самой первой установки, срабатывает один раз, пока в базе нет ни одного пользователя, и не зависит от настроенной почты. Обычный путь — открытая саморегистрация: `POST /api/auth/register` создаёт агентство и пользователя с `email_verified_at = NULL`, письмо со ссылкой шлётся через `SMTP_*` (без него эндпоинт отвечает `503` — незачем заводить аккаунт, который некому подтвердить), `POST /api/auth/verify-email` активирует учётную запись и сразу открывает сессию. До подтверждения `POST /api/auth/login` с верным паролем отвечает `403 email_not_verified`, а не создаёт сессию — `POST /api/auth/resend-verification` перевыпускает ссылку, если письмо потерялось или истекло (действительна 24 часа); он всегда отвечает одинаково успешно независимо от того, существует адрес или уже подтверждён, чтобы не превращаться в способ проверить чужую почту.

Каждое изменение туриста, заявки и справочника пишется в журнал (`entity_changes`) в той же транзакции, что и само изменение. Чувствительные поля — паспортные данные, дата рождения, телефон, email — попадают в журнал как факт изменения (`***` → `***`), без самих значений.

## API

Открытые маршруты: `GET /api/ping`, `GET /api/version`, `GET /api/health`, `POST /api/auth/login`, `POST /api/auth/register`, `POST /api/auth/verify-email`, `POST /api/auth/resend-verification`.

Остальные требуют cookie сессии:

```
POST   /api/auth/logout
GET    /api/auth/session

GET    /api/agency

GET    /api/tourists                 ?q=&channelId=&partnerId=&expiringInDays=&sort=&limit=&offset=
POST   /api/tourists
GET    /api/tourists/{id}
PATCH  /api/tourists/{id}
DELETE /api/tourists/{id}
GET    /api/tourists/{id}/applications
GET    /api/tourists/{id}/history

GET    /api/applications             ?q=&status=&touristId=&tourOperatorId=&channelId=&departFrom=&departTo=&paymentStatus=&sort=
POST   /api/applications
GET    /api/applications/{id}
PATCH  /api/applications/{id}
DELETE /api/applications/{id}
POST   /api/applications/{id}/status         {"status": "...", "cancelReason": "..."}
PUT    /api/applications/{id}/tourists       {"touristIds": ["..."]}
GET    /api/applications/{id}/history
GET    /api/applications/{id}/deadlines
POST   /api/applications/{id}/deadlines
PATCH  /api/applications/{id}/deadlines/{deadlineId}
DELETE /api/applications/{id}/deadlines/{deadlineId}

GET    /api/applications/{id}/finance
GET    /api/applications/{id}/transactions
POST   /api/applications/{id}/transactions   {"kind": "...", "amount": "...", "payerId"/"tourOperatorId": "...", "paymentMethod": "...", "occurredAt": "...", "feeAmount": "...", "note": "..."}
DELETE /api/applications/{id}/transactions/{transactionId}

GET/POST/PATCH/DELETE  /api/partners
GET/POST/PATCH/DELETE  /api/tour-operators
GET/POST/PATCH/DELETE  /api/payers
GET/POST/PATCH/DELETE  /api/acquisition-channels

GET    /api/transactions             ?kind=&applicationId=&payerId=&tourOperatorId=&occurredFrom=&occurredTo=&limit=&offset=
GET    /api/reports/revenue          ?unit=month|quarter|year&from=&to=
GET    /api/reports/applications     ?from=&to=
GET    /api/reports/directions       ?from=&to=&limit=10
GET    /api/reports/tour-operators   ?from=&to=&limit=10
GET    /api/reports/channels         ?from=&to=&limit=10
GET    /api/reports/repeat-customers ?from=&to=&limit=10

GET    /api/reminders                ?withinDays=90
GET    /api/references
GET/POST/PATCH /api/users
```

### Статусная модель заявки

Заявка проходит семь стадий жизненного цикла:

| `status` | Стадия |
|---|---|
| `inquiry` | первичное обращение |
| `selection` | подбор |
| `approval` | согласование |
| `booked` | бронирование |
| `preparation` | подготовка к поездке |
| `completed` | завершение |
| `cancelled` | отмена |

Статус меняется отдельным эндпоинтом `POST /api/applications/{id}/status`, а не через `PATCH /api/applications/{id}`: так каждый переход проверяется на допустимость и попадает в журнал изменений отдельной записью (`entity_changes.action = "status_change"`), а не растворяется среди правок остальных полей заявки.

Правила переходов:

- Менеджер может выбрать любую другую стадию напрямую, в том числе вернуться из `completed` или восстановить заявку из `cancelled`.
- **Отмена** (`cancelled`) разрешена из любой стадии, но обязательно с `cancelReason` в теле запроса — без причины запрос отклоняется как `validation_failed`.
- При восстановлении отменённой заявки причина отмены очищается; само изменение остаётся в журнале истории.

Список не захардкожен на фронтенде: `GET /api/references` отдаёт `applicationStatuses` в порядке стадий и вычисленную карту доступных переходов `statusTransitions`. Для каждого статуса карта содержит все остальные статусы.

Статус оплаты (`paymentStatus`) — отдельная, независимая производная величина: не поле заявки и не часть этой статусной модели, а результат сравнения баланса `payment_transactions` со стоимостью поездки (см. «Финансовый учёт» ниже). Заявка может быть, например, в стадии `booked` и одновременно `paymentStatus: "partial"` — стадии жизненного цикла и состояние оплаты меняются независимо друг от друга.

### Финансовый учёт

Заявка сама по себе несёт только `priceTotal`/`currency` — общую стоимость поездки. Факт движения денег — отдельная сущность, `payment_transactions`, доступная только через вложенные маршруты `/api/applications/{id}/transactions` (создание) и общий журнал агентства `GET /api/transactions` (для сверки и поиска «куда делась оплата»). Транзакции не редактируются: ошибочную можно только аннулировать (`DELETE .../transactions/{transactionId}`, мягкое удаление), исходная запись остаётся в истории.

`kind` транзакции — один из четырёх видов, счёт-фактурных сущностей у него ровно две: плательщик (`payerId`) или туроператор (`tourOperatorId`), никогда оба сразу:

| `kind` | Кто на другом конце | Смысл |
|---|---|---|
| `receipt` | `payerId` | поступление от плательщика (в т.ч. частичная оплата, доплата) |
| `refund` | `payerId` | возврат плательщику |
| `operator_transfer` | `tourOperatorId` | перечисление туроператору |
| `bonus_income` | `tourOperatorId` | дополнительная выгода от туроператора (бонус, кешбэк) |

Агентское вознаграждение отдельной строкой не хранится и вручную не вводится — это агрегат `priceTotal − Σ(operator_transfer)` по заявке, пересчитывается на лету при каждом запросе. `paymentMethod` — `cash`/`bank_transfer`/`card_acquiring`; при `card_acquiring` можно указать `feeAmount` — комиссию банка, она не вычитается из дохода агентства автоматически, а выводится отдельной строкой (`acquiringFees` в ответе `.../finance`), чтобы агентство решало само.

`GET /api/applications/{id}/finance` отдаёт баланс заявки: `received`/`refunded`/`netReceived`/`transferred`/`bonusIncome`/`acquiringFees`, а также производные `commission`, `agencyIncome` (`commission + bonusIncome`) и `paymentStatus` (`unpaid`/`partial`/`paid`/`overpaid` — сравнение `received − refunded` с `priceTotal`). Если заявка полностью оплачена, открытые дедлайны вида `payment` (`application_deadlines`) закрываются автоматически той же транзакцией — единственная связь между платежами и дедлайнами; аннулирование поступления их обратно не открывает.

`GET /api/reports/revenue` — базовый отчёт по периодам (без расчёта налога и выгрузки для КУДиР — это задел на будущее). Оборот (`receipts`/`refunds`/`transferred`/`bonusIncome`) группируется по дате самой транзакции (`occurredAt`); `commission` — не факт движения денег и своей даты не имеет, поэтому относится к периоду **последнего** `operator_transfer` по каждой заявке. Заявка без единого перевода туроператору в разбивку по периодам не попадает. Диапазон `[from, to]` ограничен пятью годами за один запрос.

Финансовый учёт пока работает только для заявок в рублях (`currency = "RUB"`) — попытка провести транзакцию по заявке в другой валюте отклоняется как `validation_failed`.

Списки возвращаются в конверте `{"items": [...], "total": N, "limit": 25, "offset": 0}`. `PATCH` — по образцу «загрузить карточку → изменить нужные поля → отправить»: поле, отсутствующее в теле, не меняется, `null` — очищает необязательное поле. Ошибки — в виде `{"error": {"code": "...", "message": "...", "fields": {...}}}`.

Пример:

```bash
curl -c cookies.txt -X POST http://127.0.0.1:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"..."}'

curl -b cookies.txt http://127.0.0.1:8080/api/tourists
```

Списки туристов маскируют номера документов (`****56`); полный номер отдаёт только `GET /api/tourists/{id}`.

### Аналитика

Дашборды поверх данных, которые уже есть после этапов «Туристы и заявки» и «Финансовый учёт» — новых таблиц под них не заводилось. Все пять эндпоинтов фильтруются по дате создания заявки/туриста (`created_at`), диапазон `[from, to]` — с тем же дефолтом (с начала текущего года по сегодня) и тем же лимитом в пять лет на один запрос, что и у `GET /api/reports/revenue`.

- `GET /api/reports/applications` — статусная сводка (число заявок в каждой из семи стадий) плюс две метрики эффективности агентства: `conversionRate` (`completed / (completed + cancelled)`) и `averageCheck` (средний `priceTotal` по `completed`). Обе — `null`, если в периоде нет ни одной `completed`/`cancelled` заявки.
- `GET /api/reports/directions` — топ стран по числу заявок и сумме `priceTotal`; заявки без указанной страны не входят.
- `GET /api/reports/tour-operators` — то же самое в разрезе туроператора; заявки, оформленные через уже архивный (`archived`) операторский профиль, из отчёта не пропадают.
- `GET /api/reports/channels` — источники клиентов: сколько новых туристов привёл канал (`tourists.createdAt`) и сколько заявок/выручки он принёс (`applications.createdAt`) — считаются раздельно, поэтому канал с активностью только по одной из метрик всё равно попадает в отчёт.
- `GET /api/reports/repeat-customers` — доля повторных клиентов среди заказчиков, оформивших хотя бы одну заявку за период. «Повторный» — заказчик с 2+ **неотменёнными** заявками за всё время (не только за период) — единственная заявка, оставшаяся после вычета отмен, повторным клиентом не считается, даже если формально заявок было две.

`limit`/`offset`-эндпоинты «топ-N» (`directions`/`tour-operators`/`channels`/`repeat-customers`) принимают `limit` (по умолчанию 10, не больше 50); `applications`/`repeat-customers` отдают агрегат-объект напрямую, без конверта `items`/`total`.

## Логирование

Логи структурные, в формате JSON, пишутся в stdout. Каждый обработанный запрос даёт одну запись:

```json
{"time":"2026-08-12T22:44:51+03:00","level":"INFO","msg":"request","method":"GET","path":"/api/version","status":200,"duration_ms":0,"bytes":17,"request_id":"965a5df73445059b498fb9de3b46c113","user_id":"...","agency_id":"..."}
```

`user_id` и `agency_id` добавляются только для запросов через `requireAuth` — это непрозрачные идентификаторы, не персональные данные.

Уровень задаётся переменной `LOG_LEVEL` (`debug`, `info`, `warn`, `error`), по умолчанию `info`; нераспознанное значение молча трактуется как `info`.

Уровень записи зависит от результата: ответы 5xx пишутся как `ERROR`, 4xx — как `WARN`, остальные — как `INFO`. Исключение — `/api/ping`: его опрашивает `HEALTHCHECK` каждые 30 секунд, поэтому проба пишется на уровне `DEBUG` и при обычных настройках в журнал не попадает. Чтобы увидеть пробы, запустите сервис с `LOG_LEVEL=debug`.

`request_id` берётся из заголовка `X-Request-Id`, а при его отсутствии генерируется; в ответе тот же идентификатор возвращается в этом же заголовке. Query-строка и тела запросов в логи **не** пишутся намеренно: в них будут параметры поиска по туристам и паспортные данные.

## Проверки

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test -race ./...
go build ./cmd/api
```

Тесты хранилища и HTTP-слоя, обращающиеся к настоящей базе данных (изоляция агентств, авторизация, миграции), пропускаются, если не задана `TEST_DATABASE_URL`:

```bash
TEST_DATABASE_URL='postgres://trip-pip:trip-pip@localhost:5433/trip-pip?sslmode=disable' go test -race ./...
```

Каждый такой тест открывает транзакцию и откатывает её по завершении, поэтому тесты не оставляют данных и могут выполняться параллельно.

## Docker

Локальная сборка и запуск:

```bash
docker build -t trip-pip-backend:local .
docker run --rm -p 127.0.0.1:8080:8080 trip-pip-backend:local
```

Образ собирается под архитектуру хоста: на arm64-машине получается arm64-образ, в CI — linux/amd64. Собрать серверный вариант локально можно так:

```bash
docker buildx build --platform linux/amd64 -t trip-pip-backend:amd64 --load .
```

`docker-compose.yml` предназначен для образа из Docker Hub. Перед запуском скопируйте `.env.example` в `.env` и заполните его — помимо имени пользователя Docker Hub и tag образа, там же `DATABASE_URL` и переменные `SMTP_*`/`PUBLIC_BASE_URL`, без которых `POST /api/auth/register` отвечает `503` (см. «Переменные окружения» выше).

Порт на хосте задаётся переменной `HOST_PORT`, по умолчанию `8077`: на боевом сервере 8080 занят другим сервисом. Внутри контейнера сервис всегда слушает 8080, поэтому `HEALTHCHECK` образа от этой переменной не зависит. Compose-файл рассчитан на сервер и подключается к внешней сети `trip-pip-network`, поэтому для локальной работы используйте `go run ./cmd/api` или `docker run`.

Контейнер работает с `read_only: true` файловой системой — миграции встроены в бинарник через `//go:embed`, поэтому база данных не требует записи на диск в контейнере.

**На этом этапе `docker-compose.yml` и деплой не подключены к PostgreSQL** — боевой сервер пока не имеет базы данных, а сервис специально умеет стартовать без неё (см. переменные окружения выше), чтобы деплой не откатывался. Подключение боевой базы данных, сеть `trip-pip-network` для неё и секрет `DATABASE_URL` в GitHub Actions — отдельная задача.

## CI/CD

Pull request в `master` запускает форматирование, `go vet`, тесты и сборку. Push в `master` выполняет те же проверки, публикует Docker-образ и развёртывает его на Ubuntu-сервере. Workflow также поддерживает ручной запуск.

В GitHub Actions необходимо создать Secrets:

- `DOCKER_USERNAME` — имя пользователя Docker Hub;
- `DOCKER_TOKEN` — access token Docker Hub;
- `DEPLOY_HOST_IP` — адрес Ubuntu-сервера;
- `DEPLOY_HOST_USERNAME` — SSH-пользователь;
- `DEPLOY_HOST_KEY` — приватный SSH-ключ;
- `DEPLOY_HOST_PROJECT_PATH` — каталог проекта на сервере.

Сервис не публикует порт на хосте вовсе — он доступен только изнутри Docker-сети `trip-pip-network`, по имени контейнера `backend`. Внешний HTTPS-доступ обеспечивает reverse proxy (Caddy), развёрнутый вместе с фронтендом в репозитории `trip-pip-frontend`, который подключается к той же сети.

## База данных

PostgreSQL, драйвер — `github.com/jackc/pgx/v5`. Миграции — самописный раннер (`internal/pg/migrate.go`, ~150 строк) поверх `embed.FS`: файлы `internal/pg/migrations/*.sql` применяются по одному в транзакции, в лексикографическом порядке, с блокировкой `pg_advisory_lock` на время всего прогона — так несколько одновременно стартующих реплик не гонятся за одними и теми же таблицами. Таблица `schema_migrations` хранит версию и контрольную сумму файла; если уже применённый файл изменится, сервис откажется стартовать. Миграции идут только вперёд: откат — это новая миграция, а не правка старой.

Идентификаторы — `uuid`. Тенантность обеспечена составными внешними ключами `(id, agency_id)` на каждой связи между доменными таблицами — см. «Модель данных и авторизация» выше.

Номера паспортов и загранпаспортов хранятся в открытом виде: шифрование столбца сломало бы и поиск, и проверку дублей, а корректное решение (отдельный ключ, ротация) — самостоятельная задача следующего этапа. Защита сейчас — доступ к базе данных, шифрование тома и то, что эти значения никогда не попадают в логи.

### Таблицы

| Таблица | Что хранит |
|---|---|
| `agencies` | Турагентства — арендаторы системы. Данные разных агентств изолированы друг от друга. |
| `agency_sequences` | Счётчики для нумерации заявок отдельно по каждому агентству. |
| `users` | Сотрудники агентств: email, пароль, привязка к агентству, `email_verified_at` (NULL — самостоятельно зарегистрировавшийся пользователь ещё не подтвердил адрес). |
| `sessions` | Активные сессии входа в систему (по хешу токена из cookie). |
| `email_verification_tokens` | Токены подтверждения email при саморегистрации (по хешу, как `sessions`); использованный или просроченный токен недействителен. |
| `acquisition_channels` | Каналы привлечения клиента: сайт, реклама, Telegram, VK и другие, свои для каждого агентства. |
| `partners` | Партнёры и рекомендатели, приводящие клиентов агентству. |
| `tour_operators` | Туроператоры, с которыми работает агентство. |
| `tourists` | Карточки туристов: ФИО, контакты, внутренний и загранпаспорт, источник привлечения. |
| `payers` | Плательщики по заявкам — физическое лицо или организация. |
| `applications` | Заявки на поездку: статус, туроператор и его номер заявки, направление, даты, стоимость. |
| `application_tourists` | Связь заявок с туристами, которые в них участвуют. |
| `application_deadlines` | Важные сроки по заявке: оплата, документы, виза, вылет и т.д. |
| `payment_transactions` | Журнал фактов движения денег по заявке: поступления, возвраты, перечисления туроператору, дополнительная выгода. Агентское вознаграждение — не строка этой таблицы, а вычисляемая разница `priceTotal − Σ(operator_transfer)`. |
| `entity_changes` | Журнал изменений — кто, что и когда поменял (для истории и аудита доступа). |

`application_balances` — не таблица, а `VIEW` поверх `applications` и `payment_transactions`: агрегирует суммы по видам транзакций одной заявки одним запросом (`received`/`refunded`/`transferred`/`bonus_income`/`acquiring_fees`), на нём построены `GET /api/applications/{id}/finance` и фильтр `paymentStatus` в `GET /api/applications`.
