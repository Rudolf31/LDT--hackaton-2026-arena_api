COMPOSE := docker compose -f deploy/docker-compose.yml --env-file .env

.PHONY: gen lint test test-integration tidy up down

gen:
	go generate ./...

lint:
	go tool golangci-lint run ./...

test:
	go test ./...

# .env подхватывается, чтобы тесты знали пароли владельца и arena_app
# (internal/platform/pgtest); явно заданные ARENA_TEST_* важнее.
test-integration:
	@set -a; if [ -f .env ]; then . ./.env; fi; set +a; go test -tags=integration ./...

tidy:
	go mod tidy

up:
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) down
