.PHONY: up down build logs restart

up:
	docker compose up -d --build

down:
	docker compose down

build:
	docker compose build --no-cache

logs:
	docker compose logs -f pay

restart:
	docker compose restart pay

# Проверка БД
db-shell:
	docker compose exec db psql -U payservice -d payservice

# Генерация webhook secret
gen-secret:
	@openssl rand -hex 32
