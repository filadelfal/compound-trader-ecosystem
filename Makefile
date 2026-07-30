.PHONY: validate build up down logs migrate

validate:
	docker compose -f compose.yaml config

build:
	docker compose -f compose.yaml build

up:
	docker compose -f compose.yaml up -d

down:
	docker compose -f compose.yaml down

logs:
	docker compose -f compose.yaml logs -f

migrate:
	powershell -ExecutionPolicy Bypass -File scripts/run-migrations.ps1
