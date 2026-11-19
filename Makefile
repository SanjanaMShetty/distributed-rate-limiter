.PHONY: up down logs run test verify integration race

up:
	docker compose -f deploy/compose/docker-compose.yml up --build

down:
	docker compose -f deploy/compose/docker-compose.yml down

logs:
	docker compose -f deploy/compose/docker-compose.yml logs -f gateway redis

run:
	go run ./cmd/gateway

test:
	go test ./...

verify: test
	go vet ./...

race:
	go test -race ./...

integration:
	REDIS_INTEGRATION_ADDR=localhost:6379 go test ./internal/engine -run TestRealRedisSharedQuota -count=1
