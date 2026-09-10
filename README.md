# Wangga: Baby & Mom Spa

Go + HTMX booking app for **Wangga: Baby & Mom Spa** — PostgreSQL, Docker Compose, Midtrans QRIS, Frontdesk Staff Portal, and Role-Based Access (Customer, CS, Admin).

## Quick start

```bash
docker compose up --build
```

- App: http://localhost:8088
- Caddy proxy: http://localhost:8089
- Mailpit UI: http://localhost:8025
- Admin: `admin@spa.local` / `admin12345`

(Host port 8080 is often busy; Compose maps the app to **8088**.)

Without Midtrans keys in local dev, payment pages show a **Simulate successful payment** button.

## Local Go run

```bash
docker compose up -d postgres mailpit
cp .env.example .env
make run
```

## User Roles & Capabilities

### 1. Customer (Mom / Parents)
- Self-registration & login.
- Book spa appointments (select location, service, therapist/bidan, date, and slot).
- Pay booking fees or full services via Midtrans QRIS.
- View booking history and status.

### 2. Customer Service (CS / Frontdesk) — `/portal`
- **Live Clinic Dashboard**: Real-time queue (`Confirmed`, `Checked-in`, `In-service`, `Completed`, `No-show`).
- **1-Click WhatsApp Slip**: Open pre-filled appointment confirmation directly to the mother's WhatsApp.
- **Manage Bookings**: View, filter, edit details, reschedule appointments, and execute clinic workflow transitions.
- **Register Customer**: Phone-first customer registration capturing baby age/DOB, maternal condition (pregnancy trimester/postpartum recovery), and medical/skin sensitivities.
- **Register Walk-in Bookings**: Create bookings on behalf of customers.
- **Payment Business Rules**:
  - **QRIS**: Can be used to pay booking fees and services.
  - **Cash**: Strictly accepted for services only (at clinic counter).
- **Bidan Schedules & Leave**: Add time-off blockouts for sick leave, doctor visits, or holidays to dynamically prevent booking conflicts.
- **Service Availability**: Instantly toggle services active/inactive.

### 3. Admin — `/admin`
- Everything CS can do.
- **CS Staff Management** (`/admin/cs`): Create CS staff accounts, toggle active/deactivate (instantly revoking sessions), and reset CS passwords.
- **Customer Password Reset** (`/admin/customers/{id}/reset-password`).
- **Master Catalog & Locations**: Manage branch locations, business hours, and base service pricing.

## Stack

- Go 1.26 + Chi + `html/template` (embedded) + HTMX
- PostgreSQL 16 + native session storage (no Redis required)
- Embedded migrations via Goose (`internal/db/migrations`)
- Midtrans QRIS + webhook (`/webhooks/midtrans`)
- SMTP email delivery (Mailpit in Compose)

## Production notes

- Set strong `SESSION_SECRET`, production `DATABASE_URL`, Midtrans keys, SMTP
- Set `APP_ENV=production` (enables Secure cookies)
- Point Midtrans notification URL to `https://your-domain/webhooks/midtrans`
- Keep a single app replica unless shared session storage is added
