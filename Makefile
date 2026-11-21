.PHONY: up down logs run test verify integration race bench

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

bench:
	@test -n "$(PROFILE)" || (echo "usage: make bench PROFILE={baseline|allow-hot|allow-many|mixed|deny-heavy|algo-token|algo-fixed|algo-sliding}" >&2; exit 2)
	bash scripts/bench/run-profile.sh "$(PROFILE)"
