COMPOSE := docker compose -f deploy/docker-compose.yml --env-file .env

.PHONY: gen lint test test-integration tidy up down

gen:
	go generate ./...

lint:
	go tool golangci-lint run ./...

test:
	go test ./...

test-integration:
	go test -tags=integration ./...

tidy:
	go mod tidy

up:
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) down
