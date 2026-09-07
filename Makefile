.PHONY: bootstrap check infra-up migrate api-run worker-run api-test api-integration-test runner-build runner-run runner-test agent-run agent-test web-dev web-check compose-validate contracts-validate

bootstrap:
	cd services/agent-runtime && python3.12 -m venv .venv && .venv/bin/pip install -e '.[dev]'
	cd apps/web && npm ci

check: api-test runner-test agent-test web-check compose-validate contracts-validate

infra-up:
	docker compose up -d postgres redis

migrate:
	cd services/api && go run ./cmd/migrate

api-run:
	cd services/api && go run ./cmd/api

worker-run:
	cd services/api && go run ./cmd/worker

api-test:
	cd services/api && test -z "$$(gofmt -l .)" && go vet ./... && go test ./...

api-integration-test:
	cd services/api && DEVPILOT_TEST_DATABASE_URL="$${DEVPILOT_DATABASE_URL:-postgres://devpilot:devpilot_local@localhost:5432/devpilot?sslmode=disable}" go test -count=1 ./...

runner-build:
	docker build -t devpilot-workspace-inspector:local services/workspace-runner

runner-run:
	cd services/workspace-runner && go run ./cmd/workspace-runner

runner-test:
	cd services/workspace-runner && test -z "$$(gofmt -l .)" && go vet ./... && go test ./...

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

contracts-validate:
	python3 -m json.tool packages/contracts/workspace-request.schema.json >/dev/null
	python3 -m json.tool packages/contracts/workspace-result.schema.json >/dev/null
	python3 -m json.tool packages/contracts/task-run-start-inspection.v1.schema.json >/dev/null
