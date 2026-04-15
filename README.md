# Pay Service — деплой на сервер с лендингом

## Быстрый старт

### 1. Загрузить файлы на сервер

```bash
# На сервере с xstreamka.dev
mkdir -p /opt/pay-service
# Скопировать все файлы из этой папки в /opt/pay-service
```

### 2. Настроить .env

```bash
cd /opt/pay-service
cp .env.example .env
nano .env
```

Заполнить:
- `DB_PASS` — придумать пароль для PostgreSQL
- `ROBOKASSA_LOGIN` — MerchantLogin из ЛК Робокассы
- `ROBOKASSA_PASS1` — Пароль #1 из технических настроек
- `ROBOKASSA_PASS2` — Пароль #2 из технических настроек
- `WEBHOOK_SECRET` — сгенерировать: `openssl rand -hex 32`
- `ROBOKASSA_TEST_MODE` — `true` для тестов, `false` для боевого режима

### 3. Запустить

```bash
cd /opt/pay-service
docker compose up -d --build
```

Проверить:
```bash
docker compose logs -f pay
# Должно быть: "Pay service started on :8090"

# Проверить что порт слушает
curl -s http://127.0.0.1:8090/payments/result
# Ответ: 405 или "bad sign" — значит работает
```

### 4. Настроить Nginx

Добавить в server-блок xstreamka.dev (`/etc/nginx/sites-available/xstreamka.dev`):

```nginx
# Pay service — проксирование платёжных endpoint-ов
location /pay/ {
    proxy_pass http://127.0.0.1:8090;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}

location /payments/ {
    proxy_pass http://127.0.0.1:8090;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

```bash
sudo nginx -t && sudo systemctl reload nginx
```

### 5. Настроить Робокассу (ЛК)

Технические настройки магазина:
- **Result URL:** `https://xstreamka.dev/payments/result` → метод **POST**
- **Success URL:** `https://xstreamka.dev/payments/success` → метод **GET**
- **Fail URL:** `https://xstreamka.dev/payments/fail` → метод **GET**
- **Алгоритм хеша:** MD5

### 6. Тестовый платёж

Открыть в браузере (подставить свои значения):
```
https://xstreamka.dev/pay/checkout?product_type=test&plan_id=test_1&amount=10.00&description=Test+Payment&user_ref=1&email=test@test.com&callback_url=&return_url=&metadata={}&ts=TIMESTAMP&sig=SIGNATURE
```

Или проще — через make:
```bash
make gen-secret  # показать текущий секрет для подписи
```

## Команды

```bash
make up        # запустить
make down      # остановить
make logs      # логи pay-сервиса
make restart   # перезапустить
make build     # пересобрать без кеша
make db-shell  # зайти в psql
make gen-secret # сгенерировать секрет
```

## Структура

```
pay-service/
├── cmd/server/main.go          # точка входа
├── internal/
│   ├── config/config.go        # конфиг из .env
│   ├── database/db.go          # PostgreSQL + миграции
│   ├── models/payment.go       # модель Payment (универсальная)
│   ├── payment/
│   │   ├── robokassa.go        # подпись, URL, Receipt
│   │   └── webhook.go          # отправка webhook (HMAC, retry)
│   ├── handlers/payment.go     # Checkout, ResultURL, SuccessURL, FailURL
│   └── templates/pay/
│       ├── checkout.html       # страница оплаты
│       ├── success.html        # fallback "оплата успешна"
│       └── fail.html           # fallback "оплата не прошла"
├── docker-compose.yml
├── Dockerfile
├── .env.example
├── Makefile
└── nginx-snippet.conf
```
