# Money Manager Server

Go backend API for the Money Manager Android application.

Compatible with:
- https://github.com/GameZebra/money-manager-android

## Features

- JWT authentication
- Register/login
- PostgreSQL storage
- Transaction CRUD
- Monthly summaries
- Docker Compose setup
- Health endpoint

## Run Locally

```bash
cp .env.example .env
docker compose up --build
```

Health check:

```bash
curl http://localhost:8080/health
```

Expected response:

```text
ok
```

## Android Emulator URL

The Android emulator should connect to:

```text
http://10.0.2.2:8080
```

## Endpoints

### Auth

- POST /auth/register
- POST /auth/login

### Transactions

- GET /transactions
- POST /transactions
- PUT /transactions/{id}
- DELETE /transactions/{id}
- GET /transactions/summary
