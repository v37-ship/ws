# Wangga: Baby & Mom Spa

Go + HTMX booking app for **Wangga: Baby & Mom Spa** — Postgres, Docker Compose, Midtrans QRIS (mockable in local dev).

## Quick start

```bash
docker compose up --build
```

- App: http://localhost:8088
- Caddy proxy: http://localhost:8089
- Mailpit UI: http://localhost:8025
- Admin: `admin@spa.local` / `admin12345`

(Host port 8080 is often busy; Compose maps the app to **8088**.)

Without Midtrans keys, payment pages show a **Simulate successful payment** button.

## Local Go run

```bash
docker compose up -d postgres mailpit
cp .env.example .env
go run ./cmd/server
```

## Stack

- Chi + `html/template` + HTMX
- Postgres sessions (no Redis)
- Midtrans QRIS + webhook `/webhooks/midtrans`
- SMTP email (Mailpit in Compose)

## Production notes

- Set strong `SESSION_SECRET`, real `DATABASE_URL`, Midtrans keys, SMTP
- Set `APP_ENV=production` (enables Secure cookies)
- Point Midtrans notification URL to `https://your-domain/webhooks/midtrans`
- Keep a single app replica unless you add shared session storage later
