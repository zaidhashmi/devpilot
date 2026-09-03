.PHONY: bootstrap check infra-up migrate api-run api-test api-integration-test agent-run agent-test web-dev web-check compose-validate

bootstrap:
	cd services/agent-runtime && python3.12 -m venv .venv && .venv/bin/pip install -e '.[dev]'
	cd apps/web && npm ci

check: api-test agent-test web-check compose-validate

infra-up:
	docker compose up -d postgres redis

migrate:
	cd services/api && go run ./cmd/migrate

api-run:
	cd services/api && go run ./cmd/api

api-test:
	cd services/api && test -z "$$(gofmt -l .)" && go vet ./... && go test ./...

api-integration-test:
	cd services/api && DEVPILOT_TEST_DATABASE_URL="$${DEVPILOT_DATABASE_URL:-postgres://devpilot:devpilot_local@localhost:5432/devpilot?sslmode=disable}" go test -count=1 ./...

agent-run:
	cd services/agent-runtime && .venv/bin/devpilot-agent-runtime

agent-test:
	cd services/agent-runtime && .venv/bin/ruff check . && .venv/bin/mypy src tests && .venv/bin/pytest

web-dev:
	cd apps/web && npm run dev

web-check:
	cd apps/web && npm run lint && npm run typecheck && npm run build

compose-validate:
	docker compose config --quiet
