.PHONY: up down logs web test lint example sqlc

up:        ## Build and start postgres, cad, agent and api
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f api agent cad

web:       ## Start the web UI on http://localhost:5173
	cd web && npm install && npm run dev

test:      ## Run every test suite
	cd cad && uv run pytest -q
	cd agent && uv run pytest -q
	cd api && go test ./...
	cd web && npx tsc -b

lint:
	cd cad && uv run ruff check . && uv run ruff format --check .
	cd agent && uv run ruff check . && uv run ruff format --check .
	cd api && go vet ./... && test -z "$$(gofmt -l .)"
	cd web && npm run lint

example:   ## Build the hand-written 3U reference design without the LLM
	cd cad && uv run cad build examples/3u_imaging.json -o out

sqlc:      ## Regenerate Go query code after editing SQL
	cd api && sqlc generate
