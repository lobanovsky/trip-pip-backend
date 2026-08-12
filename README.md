# Trip-Pip Backend

Минимальный backend Trip-Pip на Go. Сейчас приложение предоставляет один диагностический API и не использует базу данных.

## Требования

- Go 1.26;
- Docker и Docker Compose — для контейнерного запуска.

## Локальный запуск

```bash
go run ./cmd/api
curl http://127.0.0.1:8080/api/v1/ping
curl http://127.0.0.1:8080/api/v1/version
```

Адрес сервера задаётся переменной `HTTP_ADDR` и по умолчанию равен `:8080`.

Ответы API:

```json
{"message":"pong"}
{"commit":"dev"}
```

`GET /api/v1/ping` — проба живости, её использует `HEALTHCHECK` образа. `GET /api/v1/version` возвращает коммит, из которого собран бинарник; для локальных сборок это `dev`, для образов из CI — SHA коммита. Скрипт развёртывания сверяет это значение и откатывается, если на сервере остался старый образ.

## Проверки

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test -race ./...
go build ./cmd/api
```

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

`docker-compose.yml` предназначен для образа из Docker Hub. Перед запуском скопируйте `.env.example` в `.env` и укажите имя пользователя Docker Hub и tag образа.

## CI/CD

Pull request в `master` запускает форматирование, `go vet`, тесты и сборку. Push в `master` выполняет те же проверки, публикует Docker-образ и развёртывает его на Ubuntu-сервере. Workflow также поддерживает ручной запуск.

В GitHub Actions необходимо создать Secrets:

- `DOCKER_USERNAME` — имя пользователя Docker Hub;
- `DOCKER_TOKEN` — access token Docker Hub;
- `DEPLOY_HOST_IP` — адрес Ubuntu-сервера;
- `DEPLOY_HOST_USERNAME` — SSH-пользователь;
- `DEPLOY_HOST_KEY` — приватный SSH-ключ;
- `DEPLOY_HOST_PROJECT_PATH` — каталог проекта на сервере.

Сервис публикуется только на `127.0.0.1:8080`; внешний HTTPS-доступ должен предоставлять reverse proxy.

## База данных

Для следующих этапов выбрана PostgreSQL: предметная область содержит тесно связанные сущности и финансовые операции, которым нужны ограничения целостности и транзакции. PostgreSQL будет добавлена вместе с первой сохраняемой моделью данных, а не в ping/pong-релизе.
